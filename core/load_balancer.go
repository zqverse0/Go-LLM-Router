package core

import (
	"errors"
	"fmt"
	"llm-gateway/models"
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
func (lb *LoadBalancer) Route(groupID string) (*models.RoutingInfo, error) {
	lb.mu.RLock()
	state, exists := lb.groupStates[groupID]
	lb.mu.RUnlock()

	if !exists || len(state.Models) == 0 {
		return nil, ErrGroupNotFound
	}

	// 1. 获取策略 (Task 1: Dynamic Strategy)
	strategyName := state.Config.Strategy
	if strategyName == "" {
		strategyName = "round_robin" // Default
	}
	
	strategy, ok := lb.strategies[strategyName]
	if !ok {
		// Fallback to default if strategy not found
		strategy = lb.strategies["round_robin"] 
	}

	// 2. 原子操作增加计数 (Task 2: Concurrency)
	currentCount := state.RequestCounter.Add(1)

	// 3. 执行策略选择模型
	selectedModel, err := strategy.Select(state.Models, currentCount)
	if err != nil {
		return nil, err
	}

	// 4. 选择 Key (保留原有逻辑，但从预解密的 state.Keys 中读取)
	keys := state.Keys[selectedModel.ID]
	if len(keys) == 0 {
		return nil, fmt.Errorf("no API keys for model %s", selectedModel.UpstreamModel)
	}

	// 寻找第一个可用的 Key
	var finalKey string
	// 使用相同的计数器来轮询 Key
	for i := 0; i < len(keys); i++ {
		k := keys[(int(currentCount-1)+i)%len(keys)]
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
		Provider:      selectedModel.ProviderName,
		UpstreamURL:   selectedModel.UpstreamURL,
		UpstreamModel: selectedModel.UpstreamModel,
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
