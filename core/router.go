package core

import (
	"context"
	"fmt"
	"llm-gateway/models"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// 全局单例计数器，永不销毁
var (
	// 全局轮询计数器，跨请求持久化
	globalRRCounters = make(map[string]*uint64)
	// 保护全局计数器的读写锁
	globalRRMutex sync.RWMutex
)

// StatelessModelRouter 无状态模型路由器 - 只读数据提供者
type StatelessModelRouter struct {
	db              *gorm.DB
	logger          *logrus.Logger
	mutex           sync.RWMutex
	// 内存缓存，提高查询性能（只读）
	modelGroups     map[string]*models.ModelGroup
	modelConfigMap  map[string][]*models.ModelConfig    // group_id -> models
	keyMap          map[string][]string                 // model_config_id -> keys (直接使用数据库ID)
	stats           map[string]map[int]*models.ModelStats // group_id -> model_index -> stats
	// 网关设置
	gatewaySettings *models.GatewaySettings
	// Key 管理器引用
	keyManager *KeyStateManager
}

// NewStatelessModelRouter 创建新的无状态模型路由器
func NewStatelessModelRouter(db *gorm.DB, logger *logrus.Logger) (*StatelessModelRouter, error) {
	router := &StatelessModelRouter{
		db:             db,
		logger:         logger,
		modelGroups:    make(map[string]*models.ModelGroup),
		modelConfigMap: make(map[string][]*models.ModelConfig),
		keyMap:         make(map[string][]string),
		stats:          make(map[string]map[int]*models.ModelStats),
		keyManager:     GlobalKeyManager, // 使用全局KeyManager
	}

	// 加载初始数据
	if err := router.loadData(); err != nil {
		return nil, err
	}

	return router, nil
}

// loadData 从数据库加载数据到内存（只读）
func (r *StatelessModelRouter) loadData() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// 加载网关设置
	var settings models.GatewaySettings
	if err := r.db.First(&settings).Error; err != nil {
		return fmt.Errorf("failed to load gateway settings: %w", err)
	}
	r.gatewaySettings = &settings

	// 加载模型组
	var groups []models.ModelGroup
	if err := r.db.Preload("Models.APIKeys").Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load model groups: %w", err)
	}

	// 清空缓存
	r.modelGroups = make(map[string]*models.ModelGroup)
	r.modelConfigMap = make(map[string][]*models.ModelConfig)
	r.keyMap = make(map[string][]string)
	r.stats = make(map[string]map[int]*models.ModelStats)

	// 构建缓存
	for i := range groups {
		group := &groups[i]
		r.modelGroups[group.GroupID] = group
		r.modelConfigMap[group.GroupID] = make([]*models.ModelConfig, len(group.Models))

		for j := range group.Models {
			model := &group.Models[j]
			r.modelConfigMap[group.GroupID][j] = model

			modelConfigID := fmt.Sprintf("%d", model.ID)
			keys := make([]string, len(model.APIKeys))
			for k := range model.APIKeys {
				keys[k] = model.APIKeys[k].KeyValue
			}
			r.keyMap[modelConfigID] = keys
		}

		// 初始化统计
		r.stats[group.GroupID] = make(map[int]*models.ModelStats)
	}

	// 创建 ID 到 GroupID 的映射
	idToGroupID := make(map[uint]string)
	for _, group := range r.modelGroups {
		idToGroupID[group.ID] = group.GroupID
	}

	// 加载统计数据
	var stats []models.ModelStats
	if err := r.db.Find(&stats).Error; err != nil {
		return fmt.Errorf("failed to load stats: %w", err)
	}

	for i := range stats {
		stat := &stats[i]
		groupID, exists := idToGroupID[stat.ModelGroupID]
		if !exists {
			r.logger.Warnf("Found stats for unknown ModelGroupID: %d", stat.ModelGroupID)
			continue
		}
		if _, exists := r.stats[groupID]; !exists {
			r.stats[groupID] = make(map[int]*models.ModelStats)
		}
		r.stats[groupID][stat.ModelIndex] = stat
	}

	r.logger.Infof("Loaded %d model groups (stateless mode)", len(r.modelGroups))
	return nil
}

// RefreshData 刷新数据（只读缓存更新）
func (r *StatelessModelRouter) RefreshData() error {
	r.logger.Info("Refreshing stateless router data...")
	return r.loadData()
}

// GetModelByIndex 直接获取指定位置的模型
func (r *StatelessModelRouter) GetModelByIndex(groupID string, index int) (*models.ModelConfig, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	models, exists := r.modelConfigMap[groupID]
	if !exists {
		return nil, fmt.Errorf("model group '%s' not found", groupID)
	}

	if index < 0 || index >= len(models) {
		return nil, fmt.Errorf("model index %d out of bounds for group '%s' (0-%d)", index, groupID, len(models)-1)
	}

	return models[index], nil
}

// GetKeyByIndex 直接获取指定位置的Key
func (r *StatelessModelRouter) GetKeyByIndex(model *models.ModelConfig, index int) (string, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	modelConfigID := fmt.Sprintf("%d", model.ID)
	keys, exists := r.keyMap[modelConfigID]
	if !exists || len(keys) == 0 {
		return "default-key", fmt.Errorf("no keys found for model %s (ID: %d)", model.ProviderName, model.ID)
	}

	if index < 0 || index >= len(keys) {
		return "default-key", fmt.Errorf("key index %d out of bounds for model %s (0-%d)", index, model.ProviderName, len(keys)-1)
	}

	return keys[index], nil
}

// GetTotalModels 获取组内模型总数
func (r *StatelessModelRouter) GetTotalModels(groupID string) int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	models, exists := r.modelConfigMap[groupID]
	if !exists {
		return 0
	}
	return len(models)
}

// GetTotalKeys 获取模型内Key总数
func (r *StatelessModelRouter) GetTotalKeys(model *models.ModelConfig) int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	modelConfigID := fmt.Sprintf("%d", model.ID)
	keys, exists := r.keyMap[modelConfigID]
	if !exists {
		return 0
	}
	return len(keys)
}

// getGroupCounter 获取或初始化组计数器（内部方法）
func (r *StatelessModelRouter) getGroupCounter(groupID string) *uint64 {
	globalRRMutex.RLock()
	rrCounter, counterExists := globalRRCounters[groupID]
	globalRRMutex.RUnlock()

	if !counterExists {
		globalRRMutex.Lock()
		if rrCounter, counterExists = globalRRCounters[groupID]; !counterExists {
			rrCounter = new(uint64)
			globalRRCounters[groupID] = rrCounter
			r.logger.Infof("Initialized global round-robin counter for group %s", groupID)
		}
		globalRRMutex.Unlock()
	}

	return rrCounter
}

// GetInitialModelIndex 获取初始模型索引（用于无状态轮询）
func (r *StatelessModelRouter) GetInitialModelIndex(groupID string) int {
	r.mutex.RLock()
	group, exists := r.modelGroups[groupID]
	totalModels := len(r.modelConfigMap[groupID])
	r.mutex.RUnlock()

	if !exists || totalModels == 0 {
		return 0
	}

	switch group.Strategy {
	case "round_robin":
		rrCounter := r.getGroupCounter(groupID)
		newCounter := atomic.AddUint64(rrCounter, 1)
		modelIdx := int((newCounter - 1) % uint64(totalModels))
		return modelIdx
	case "fallback":
		return 0
	default:
		return 0
	}
}

// GetInitialKeyIndex 获取初始Key索引（用于 round_robin 策略的Key轮询）
// 【优化】: 增加智能过滤，尽量返回一个可用的 Key
func (r *StatelessModelRouter) GetInitialKeyIndex(modelID uint) int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// 找到模型所属的组
	var groupID string
	var groupStrategy string
	for _, group := range r.modelGroups {
		for _, model := range group.Models {
			if model.ID == modelID {
				groupID = group.GroupID
				groupStrategy = group.Strategy
				break
			}
		}
		if groupID != "" {
			break
		}
	}

	if groupID == "" {
		return 0
	}

	modelConfigID := fmt.Sprintf("%d", modelID)
	keys, exists := r.keyMap[modelConfigID]
	if !exists || len(keys) == 0 {
		return 0
	}

	totalKeys := len(keys)

	// 简单的轮询逻辑基础
	baseIdx := 0
	if groupStrategy == "round_robin" {
		globalRRMutex.RLock()
		rrCounter, counterExists := globalRRCounters[groupID]
		globalRRMutex.RUnlock()
		if counterExists {
			currentCounter := atomic.LoadUint64(rrCounter)
			baseIdx = int(currentCounter % uint64(totalKeys))
		}
	}

	// 【智能 Key 选择】
	// 从 baseIdx 开始尝试找到第一个可用的 Key
	// 如果找不到（所有都 cooldown），就 fallback 到 baseIdx，让上层 ProxyHandler 去处理具体的失败
	for i := 0; i < totalKeys; i++ {
		idx := (baseIdx + i) % totalKeys
		if r.keyManager.IsAvailable(keys[idx]) {
			return idx
		}
	}

	return baseIdx
}

// CalculateMaxRetries 计算动态最大重试次数
func (r *StatelessModelRouter) CalculateMaxRetries(groupID string) int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	models, exists := r.modelConfigMap[groupID]
	if !exists || len(models) == 0 {
		return 3
	}

	totalKeys := 0
	for _, model := range models {
		modelConfigID := fmt.Sprintf("%d", model.ID)
		keys, exists := r.keyMap[modelConfigID]
		if exists {
			totalKeys += len(keys)
		}
	}

	maxRetries := int(float64(totalKeys) * 1.5)
	if maxRetries < 3 {
		maxRetries = 3
	}
	if maxRetries > 12 {
		maxRetries = 12
	}

	return maxRetries
}

// ParseModelRouting 解析模型路由字符串，支持定向路由功能
func (r *StatelessModelRouter) ParseModelRouting(modelInput string) *models.RoutingInfo {
	if modelInput == "" {
		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	if !strings.Contains(modelInput, "$") {
		for groupID, modelGroup := range r.GetAllModelGroups() {
			for idx, model := range modelGroup.Models {
				if model.UpstreamModel == modelInput {
					modelIndex := idx
					return &models.RoutingInfo{
						GroupID:    groupID,
						ModelIndex: &modelIndex,
						IsPinned:   true,
					}
				}
			}
		}

		if _, err := r.GetModelGroup(modelInput); err == nil {
			return &models.RoutingInfo{
				GroupID:  modelInput,
				IsPinned: false,
			}
		}

		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	parts := strings.SplitN(modelInput, "$", 2)
	if len(parts) != 2 {
		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	groupID, indexStr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if groupID == "" {
		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	userIndex, err := strconv.Atoi(indexStr)
	if err != nil || userIndex < 1 {
		return &models.RoutingInfo{GroupID: groupID, IsPinned: false}
	}

	targetIndex := userIndex - 1
	return &models.RoutingInfo{
		GroupID:    groupID,
		ModelIndex: &targetIndex,
		IsPinned:   true,
	}
}

// GetModelGroup 获取指定模型组
func (r *StatelessModelRouter) GetModelGroup(groupID string) (*models.ModelGroup, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	group, exists := r.modelGroups[groupID]
	if !exists {
		return nil, fmt.Errorf("model group '%s' not found", groupID)
	}
	groupCopy := *group
	return &groupCopy, nil
}

// 状态码判断方法
func (r *StatelessModelRouter) IsClientError(statusCode int) bool {
	return statusCode >= 400 && statusCode < 500 && statusCode != 401 && statusCode != 403 && statusCode != 429
}

func (r *StatelessModelRouter) IsAuthError(statusCode int) bool {
	return statusCode == 401 || statusCode == 403 || statusCode == 429
}

func (r *StatelessModelRouter) IsServerError(statusCode int) bool {
	return statusCode >= 500
}

// IsHardError 判断是否为硬错误（配置级错误）
func (r *StatelessModelRouter) IsHardError(statusCode int, err error) bool {
	switch statusCode {
	case 400, 404, 405:
		return true
	}

	if err != nil {
		errStr := err.Error()
		hardErrorPatterns := []string{
			"connection refused",
			"no such host",
			"timeout",
			"network unreachable",
			"dns resolution failed",
			"ssl certificate",
			"tls handshake",
		}
		errLower := strings.ToLower(errStr)
		for _, pattern := range hardErrorPatterns {
			if strings.Contains(errLower, pattern) {
				return true
			}
		}
	}

	return false
}

// 【新增】ReportKeyStatus 报告Key的使用状态，触发智能冷却
func (r *StatelessModelRouter) ReportKeyStatus(key string, statusCode int) {
	if statusCode == 429 {
		// 触发 60s 冷却
		r.keyManager.MarkCooldown(key, 60*time.Second)
		r.logger.Warnf("🔥 Key %s cooldown triggered (429 Too Many Requests)", MaskKey(key))
	} else if statusCode == 401 || statusCode == 403 {
		// 标记为死亡
		r.keyManager.MarkDead(key)
		r.logger.Errorf("💀 Key %s marked as DEAD (Auth Error %d)", MaskKey(key), statusCode)
	}
}

// UpdateStats 更新统计信息
func (r *StatelessModelRouter) UpdateStats(groupID string, modelIndex int, success bool, latency float64) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, exists := r.stats[groupID][modelIndex]; !exists {
		r.stats[groupID][modelIndex] = &models.ModelStats{
			ModelGroupID: 0,
			ModelIndex:   modelIndex,
			Success:      0,
			Error:        0,
			TotalLatency: 0,
			RequestCount: 0,
		}
	}
	stat := r.stats[groupID][modelIndex]

	if success {
		stat.Success++
	} else {
		stat.Error++
	}
	stat.TotalLatency += latency
	stat.RequestCount++

	go func() {
		var group models.ModelGroup
		if err := r.db.Where("group_id = ?", groupID).First(&group).Error; err != nil {
			return
		}
		stat.ModelGroupID = group.ID
		r.db.Save(stat)
	}()

	return nil
}

// GetStats 获取统计
func (r *StatelessModelRouter) GetStats(groupID string, modelIndex int) *models.ModelStats {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	if _, exists := r.stats[groupID]; !exists {
		return &models.ModelStats{}
	}
	stat, exists := r.stats[groupID][modelIndex]
	if !exists {
		return &models.ModelStats{}
	}
	return &models.ModelStats{
		Success:      stat.Success,
		Error:        stat.Error,
		TotalLatency: stat.TotalLatency,
		RequestCount: stat.RequestCount,
	}
}

// 其他必要方法
func (r *StatelessModelRouter) GetGatewaySettings() *models.GatewaySettings {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.gatewaySettings
}

func (r *StatelessModelRouter) GetAllModelGroups() map[string]*models.ModelGroup {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	result := make(map[string]*models.ModelGroup)
	for k, v := range r.modelGroups {
		groupCopy := *v
		result[k] = &groupCopy
	}
	return result
}

func (r *StatelessModelRouter) ContextTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

func (r *StatelessModelRouter) GetDB() *gorm.DB {
	return r.db
}

func (r *StatelessModelRouter) GetLogger() *logrus.Logger {
	return r.logger
}

// GetTotalStats 获取所有组的统计信息
func (r *StatelessModelRouter) GetTotalStats() map[string]*models.AdminStatsResponse {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	result := make(map[string]*models.AdminStatsResponse)

	for groupID, group := range r.modelGroups {
		modelConfigs := r.modelConfigMap[groupID]
		adminModels := make([]models.AdminModelStats, len(modelConfigs))
		totalRequests := 0

		for i, model := range modelConfigs {
			stats := r.GetStats(groupID, i)
			avgLatency := float64(0)
			if stats.RequestCount > 0 {
				avgLatency = stats.TotalLatency / float64(stats.RequestCount)
			}

			adminModels[i] = models.AdminModelStats{
				Index:         i + 1,
				Provider:      model.ProviderName,
				UpstreamModel: model.UpstreamModel,
				Success:       stats.Success,
				Error:         stats.Error,
				AvgLatency:    avgLatency,
				TotalRequests: stats.RequestCount,
			}
			totalRequests += stats.RequestCount
		}

		result[groupID] = &models.AdminStatsResponse{
			GroupID:      groupID,
			Strategy:     group.Strategy,
			Models:       adminModels,
			TotalRequests: totalRequests,
			Timestamp:    time.Now().Unix(),
		}
	}

	return result
}

func (r *StatelessModelRouter) UpgradeToWebSocket(c *gin.Context) (*websocket.Conn, error) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	return upgrader.Upgrade(c.Writer, c.Request, nil)
}

// GetModelKeys 获取模型的所有 API Keys
func (r *StatelessModelRouter) GetModelKeys(modelID uint) ([]string, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	modelConfigID := fmt.Sprintf("%d", modelID)
	keys, exists := r.keyMap[modelConfigID]
	if !exists {
		return nil, fmt.Errorf("no keys found for model ID %d", modelID)
	}

	// 【新增】智能过滤：只返回可用的 Key
	// 注意：这里返回所有 Key，让 GetInitialKeyIndex 去决定顺序
	// 或者这里可以做一个简单的过滤？
	// 为了不破坏原有逻辑（比如轮询），这里还是返回所有 Key，
	// 但调用方应该使用 IsAvailable 来检查
	
	result := make([]string, len(keys))
	copy(result, keys)
	return result, nil
}

// MaskKey 简单的脱敏帮助函数
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
