package core

import (
	"errors"
	"fmt"
	"llm-gateway/models"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var (
	ErrGroupNotFound = errors.New("group not found or has no models")
)

// GroupState 封装运行时状态 (Task 2: Architecture)
type GroupState struct {
	Config   *models.ModelGroup
	Models   []*models.ModelConfig // 预处理后的列表
	Keys     map[uint][]string     // ModelID -> Decrypted Keys
	
	// Atomic counter specific to this group
	// 替代了原本低效的全局锁 globalRRMutex
	RequestCounter atomic.Uint64 
}

// LoadBalancer (原 StatelessModelRouter)
// 实现了路由分发、状态管理和策略执行
type LoadBalancer struct {
	// 依赖注入 (Dependencies)
	db             *gorm.DB
	logger         *logrus.Logger
	keyManager     KeyManager     // 接口
	secretProvider SecretProvider // 接口
	
	// 策略注册表
	strategies map[string]Strategy

	// 内部状态
	mu              sync.RWMutex
	groupStates     map[string]*GroupState // GroupID -> State
	gatewaySettings *models.GatewaySettings
}

// NewLoadBalancer 构造函数强制要求依赖注入
func NewLoadBalancer(
	db *gorm.DB, 
	logger *logrus.Logger, 
	km KeyManager, 
	sp SecretProvider,
) (*LoadBalancer, error) {
	lb := &LoadBalancer{
		db:             db,
		logger:         logger,
		keyManager:     km,
		secretProvider: sp,
		strategies:     make(map[string]Strategy),
		groupStates:    make(map[string]*GroupState),
	}
	
	// 注册默认策略
	lb.RegisterStrategy(&RoundRobinStrategy{})
	lb.RegisterStrategy(&FallbackStrategy{})
	
	// 加载数据
	if err := lb.RefreshData(); err != nil {
		return nil, err
	}
	
	return lb, nil
}

func (lb *LoadBalancer) RegisterStrategy(s Strategy) {
	lb.strategies[s.Name()] = s
}

// RefreshData 重新加载数据
func (lb *LoadBalancer) RefreshData() error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	var settings models.GatewaySettings
	if err := lb.db.First(&settings).Error; err != nil {
		return fmt.Errorf("failed to load gateway settings: %w", err)
	}
	lb.gatewaySettings = &settings

	var groups []models.ModelGroup
	// Preload necessary data
	if err := lb.db.Preload("Models.APIKeys").Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load model groups: %w", err)
	}

	newGroupStates := make(map[string]*GroupState)

	for _, g := range groups {
		// Deep copy group to avoid reference issues
		groupCopy := g
		
		state := &GroupState{
			Config: &groupCopy,
			Models: make([]*models.ModelConfig, 0),
			Keys:   make(map[uint][]string),
		}

		for i := range g.Models {
			mc := &g.Models[i]
			state.Models = append(state.Models, mc)
			
			decryptedKeys := make([]string, 0)
			for _, k := range mc.APIKeys {
				// Decrypt key
				val, err := lb.secretProvider.Decrypt(k.KeyValue)
				if err != nil {
					lb.logger.Errorf("Failed to decrypt key for model %s: %v", mc.UpstreamModel, err)
					continue
				}
				decryptedKeys = append(decryptedKeys, val)
			}
			state.Keys[mc.ID] = decryptedKeys
		}
		newGroupStates[g.GroupID] = state
	}

	lb.groupStates = newGroupStates
	lb.logger.Infof("Loaded %d model groups (LoadBalancer mode)", len(lb.groupStates))
	return nil
}

// Route 执行路由逻辑
func (lb *LoadBalancer) Route(requestModel string) (*models.RoutingInfo, error) {
	// [Feature] Model Pinning: "group$index"
	// Example: "Ai-code$2" -> Use 2nd model in "Ai-code" group
	var groupID string
	var pinIndex int = -1

	if idx := strings.Index(requestModel, "$"); idx != -1 {
		groupID = requestModel[:idx]
		if i, err := strconv.Atoi(requestModel[idx+1:]); err == nil && i > 0 {
			pinIndex = i - 1 // Convert 1-based index to 0-based
		}
	} else {
		groupID = requestModel
	}

	lb.mu.RLock()
	state, exists := lb.groupStates[groupID]
	lb.mu.RUnlock()

	if !exists || len(state.Models) == 0 {
		return nil, ErrGroupNotFound
	}

	var selectedModel *models.ModelConfig
	var err error

	// 1. Select Model (Strategy vs Pinning)
	if pinIndex != -1 {
		// Bypass strategy, force select
		if pinIndex >= len(state.Models) {
			return nil, fmt.Errorf("model index %d out of bounds for group %s", pinIndex+1, groupID)
		}
		selectedModel = state.Models[pinIndex]
	} else {
		// Use Strategy
		strategyName := state.Config.Strategy
		if strategyName == "" {
			strategyName = "round_robin"
		}
		strategy, ok := lb.strategies[strategyName]
		if !ok {
			strategy = lb.strategies["round_robin"]
		}
		currentCount := state.RequestCounter.Add(1)
		selectedModel, err = strategy.Select(state.Models, currentCount)
		if err != nil {
			return nil, err
		}
	}

	// 4. 选择 Key (保留原有逻辑，但从预解密的 state.Keys 中读取)
	keys := state.Keys[selectedModel.ID]
	if len(keys) == 0 {
		return nil, fmt.Errorf("no API keys for model %s", selectedModel.UpstreamModel)
	}

	// 寻找第一个可用的 Key
	var finalKey string
	// 无论是否 Pinning，Key 都应该轮询以实现负载均衡
	// 使用 Add(1) 确保 currentCount 总是单调递增且 > 0 (只要初始不是0，但Add后肯定是正数)
	// 如果担心溢出，uint64 很大，很难溢出。即使溢出回绕，% len 依然安全。
	// 为了绝对安全，我们把 currentCount 转为 uint64 参与计算
	
	// 注意：之前这里逻辑复杂化了，导致了 currentCount=0 时 -1 的 panic
	// 统一逻辑：每次 Route 都消耗一个计数（即使是 Pinning），用来转动 Key
	count := state.RequestCounter.Add(1)

	for i := 0; i < len(keys); i++ {
		// (count + i) 可能会很大，但 % len 会将其限制在 [0, len-1]
		// 我们不需要减 1，因为 count 是任意的起始点，只要它是递增的就行
		idx := (int(count) + i) % len(keys)
		// 防止负数索引 (尽管 uint64 转 int 在极值时可能变负，但概率极低，防御一下)
		if idx < 0 { idx = -idx }
		
		k := keys[idx]
		if lb.keyManager.IsAvailable(k) {
			finalKey = k
			break
		}
	}

	if finalKey == "" {
		return nil, fmt.Errorf("all keys for model %s are in cooldown or dead", selectedModel.UpstreamModel)
	}

	return &models.RoutingInfo{
		GroupID:       groupID,
		ModelGroupID:  state.Config.ID,
		Provider:      selectedModel.ProviderName,
		UpstreamURL:   selectedModel.UpstreamURL,
		UpstreamModel: selectedModel.UpstreamModel,
		ModelConfigID: selectedModel.ID,
		APIKey:        finalKey,
		Timeout:       selectedModel.Timeout,
	}, nil
}

func (lb *LoadBalancer) GetGatewaySettings() *models.GatewaySettings {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.gatewaySettings
}

func (lb *LoadBalancer) GetDB() *gorm.DB {
	return lb.db
}

func (lb *LoadBalancer) GetLogger() *logrus.Logger {
	return lb.logger
}

func (lb *LoadBalancer) GetAllModelGroups() []models.ModelGroup {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	
	groups := make([]models.ModelGroup, 0, len(lb.groupStates))
	for _, state := range lb.groupStates {
		groups = append(groups, *state.Config)
	}
	return groups
}

func (lb *LoadBalancer) GetTotalStats() map[string]interface{} {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	
	return map[string]interface{}{
		"groups_count": len(lb.groupStates),
		"uptime":       "N/A", // 可以稍后添加 uptime
	}
}

// Encrypt 暴露加密方法供外部（如 Handler）使用
func (lb *LoadBalancer) Encrypt(plaintext string) (string, error) {
	return lb.secretProvider.Encrypt(plaintext)
}

// Decrypt 暴露解密方法供外部（如 Handler）使用
func (lb *LoadBalancer) Decrypt(ciphertext string) (string, error) {
	return lb.secretProvider.Decrypt(ciphertext)
}
