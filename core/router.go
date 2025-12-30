package core

import (
	"fmt"
	"llm-gateway/models"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var (
	globalRRCounters = make(map[string]*uint64)
	globalRRMutex    sync.RWMutex
)

type StatelessModelRouter struct {
	db              *gorm.DB
	logger          *logrus.Logger
	mutex           sync.RWMutex
	modelGroups     map[string]*models.ModelGroup
	modelConfigMap  map[string][]*models.ModelConfig
	keyMap          map[string][]string
	gatewaySettings *models.GatewaySettings
	keyManager      *KeyStateManager
}

func NewStatelessModelRouter(db *gorm.DB, logger *logrus.Logger) (*StatelessModelRouter, error) {
	router := &StatelessModelRouter{
		db:             db,
		logger:         logger,
		modelGroups:    make(map[string]*models.ModelGroup),
		modelConfigMap: make(map[string][]*models.ModelConfig),
		keyMap:         make(map[string][]string),
		keyManager:     GlobalKeyManager,
	}
	if err := router.loadData(); err != nil {
		return nil, err
	}
	return router, nil
}

func (r *StatelessModelRouter) loadData() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	var settings models.GatewaySettings
	if err := r.db.First(&settings).Error; err != nil {
		return fmt.Errorf("failed to load gateway settings: %w", err)
	}
	r.gatewaySettings = &settings

	var groups []models.ModelGroup
	r.db.Preload("Models.APIKeys").Find(&groups)

	for _, g := range groups {
		groupCopy := g
		r.modelGroups[g.GroupID] = &groupCopy
		configs := make([]*models.ModelConfig, 0)
		for i := range g.Models {
			mc := &g.Models[i]
			configs = append(configs, mc)
			keys := make([]string, 0)
			for _, k := range mc.APIKeys {
				keys = append(keys, k.KeyValue)
			}
			r.keyMap[fmt.Sprintf("%d", mc.ID)] = keys
		}
		r.modelConfigMap[g.GroupID] = configs
	}
	r.logger.Infof("Loaded %d model groups (stateless mode)", len(r.modelGroups))
	return nil
}

func (r *StatelessModelRouter) getGroupCounter(groupID string) *uint64 {
	globalRRMutex.RLock()
	counter, exists := globalRRCounters[groupID]
	globalRRMutex.RUnlock()
	if exists {
		return counter
	}
	globalRRMutex.Lock()
	defer globalRRMutex.Unlock()
	if counter, exists = globalRRCounters[groupID]; exists {
		return counter
	}
	var newCounter uint64 = 0
	globalRRCounters[groupID] = &newCounter
	return &newCounter
}

func (r *StatelessModelRouter) Route(groupID string) (*models.RoutingInfo, error) {
	r.mutex.RLock()
	_, exists := r.modelGroups[groupID]
	configs := r.modelConfigMap[groupID]
	r.mutex.RUnlock()

	if !exists || len(configs) == 0 {
		return nil, fmt.Errorf("group %s not found or has no models", groupID)
	}

	// 1. 选择模型索引 (Round Robin)
	counter := r.getGroupCounter(groupID)
	count := atomic.AddUint64(counter, 1)
	modelIdx := int((count - 1) % uint64(len(configs)))
	
	selectedModel := configs[modelIdx]
	
	// 2. 选择 Key (智能过滤)
	keys := r.keyMap[fmt.Sprintf("%d", selectedModel.ID)]
	if len(keys) == 0 {
		return nil, fmt.Errorf("no API keys for model %s", selectedModel.UpstreamModel)
	}

	// 寻找第一个可用的 Key
	var finalKey string
	for i := 0; i < len(keys); i++ {
		k := keys[(int(count-1)+i)%len(keys)]
		if r.keyManager.IsAvailable(k) {
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

func (r *StatelessModelRouter) GetGatewaySettings() *models.GatewaySettings {
	return r.gatewaySettings
}

// GetDB 返回数据库实例
func (r *StatelessModelRouter) GetDB() *gorm.DB {
	return r.db
}

// GetLogger 返回日志实例
func (r *StatelessModelRouter) GetLogger() *logrus.Logger {
	return r.logger
}

// RefreshData 重新加载数据 (线程安全)
func (r *StatelessModelRouter) RefreshData() error {
	return r.loadData()
}

// GetAllModelGroups 返回所有模型组配置
func (r *StatelessModelRouter) GetAllModelGroups() []models.ModelGroup {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	
	groups := make([]models.ModelGroup, 0, len(r.modelGroups))
	for _, g := range r.modelGroups {
		groups = append(groups, *g)
	}
	return groups
}

// GetTotalStats 返回简单的统计概览（占位实现）
func (r *StatelessModelRouter) GetTotalStats() map[string]interface{} {
	return map[string]interface{}{
		"groups_count": len(r.modelGroups),
		"uptime":       "N/A", // 简化处理
	}
}
