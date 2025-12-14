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
		// rrCounters 已移除，改用全局变量
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

	// 清空缓存（注意：不包含 rrCounters，因为现在是全局变量）
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

			// 使用 model_config_id 作为key，确保唯一性
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
	// 获取或惰性初始化全局计数器
	globalRRMutex.RLock()
	rrCounter, counterExists := globalRRCounters[groupID]
	globalRRMutex.RUnlock()

	if !counterExists {
		// 惰性初始化计数器（双重检查锁定）
		globalRRMutex.Lock()
		// 再次检查，防止并发初始化
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
		// 递增全局计数器并返回模型索引
		rrCounter := r.getGroupCounter(groupID)
		newCounter := atomic.AddUint64(rrCounter, 1) // 递增计数器
		modelIdx := int((newCounter - 1) % uint64(totalModels)) // 0-based索引

		return modelIdx
	case "fallback":
		return 0
	default:
		return 0
	}
}

// GetInitialKeyIndex 获取初始Key索引（用于 round_robin 策略的Key轮询）
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

	// 获取模型的Key信息
	modelConfigID := fmt.Sprintf("%d", modelID)
	keys, exists := r.keyMap[modelConfigID]
	if !exists {
		return 0
	}

	totalKeys := len(keys)
	if totalKeys == 0 {
		return 0
	}

	switch groupStrategy {
	case "round_robin":
		// 🔑 关键：读取同一个全局计数器的当前值（不递增）
		globalRRMutex.RLock()
		rrCounter, counterExists := globalRRCounters[groupID]
		globalRRMutex.RUnlock()

		if !counterExists {
			return 0
		}

		currentCounter := atomic.LoadUint64(rrCounter)
		keyIdx := int(currentCounter % uint64(totalKeys))

		return keyIdx
	case "fallback":
		return 0
	default:
		return 0
	}
}

// CalculateMaxRetries 计算动态最大重试次数
func (r *StatelessModelRouter) CalculateMaxRetries(groupID string) int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	models, exists := r.modelConfigMap[groupID]
	if !exists || len(models) == 0 {
		return 3 // 默认值
	}

	totalKeys := 0
	for _, model := range models {
		modelConfigID := fmt.Sprintf("%d", model.ID)
		keys, exists := r.keyMap[modelConfigID]
		if exists {
			totalKeys += len(keys)
		}
	}

	// 动态计算：总Key数 * 1.5，至少3次，最多12次
	maxRetries := int(float64(totalKeys) * 1.5)
	if maxRetries < 3 {
		maxRetries = 3
	}
	if maxRetries > 12 {
		maxRetries = 12
	}

	r.logger.Infof("Calculated max retries for group %s: %d (total keys: %d)", groupID, maxRetries, totalKeys)
	return maxRetries
}

// ParseModelRouting 解析模型路由字符串，支持定向路由功能
func (r *StatelessModelRouter) ParseModelRouting(modelInput string) *models.RoutingInfo {
	if modelInput == "" {
		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	// 检查是否包含分隔符
	if !strings.Contains(modelInput, "$") {
		// 【新增】智能查找逻辑：先尝试作为模型ID查找，再尝试作为组ID查找
		// 1. 先尝试作为模型ID查找（通过检查所有组的所有模型）
		for groupID, modelGroup := range r.GetAllModelGroups() {
			for idx, model := range modelGroup.Models {
				if model.UpstreamModel == modelInput {
					// 找到模型，使用其所属的组
					modelIndex := idx // 使用数组索引作为模型索引
					r.logger.Infof("[INFO] Smart Routing | Model: %s -> Group: %s | Index: %d", modelInput, groupID, idx)
					return &models.RoutingInfo{
						GroupID:    groupID,
						ModelIndex: &modelIndex,
						IsPinned:   true, // 直接使用特定模型
					}
				}
			}
		}

		// 2. 尝试作为组ID查找
		if _, err := r.GetModelGroup(modelInput); err == nil {
			// 找到组，使用组的第一个模型
			r.logger.Infof("[INFO] Smart Routing | Group: %s -> Using first available model", modelInput)
			return &models.RoutingInfo{
				GroupID:  modelInput,
				IsPinned: false, // 使用组的默认轮询/策略
			}
		}

		// 3. 都没找到，返回原始值（可能导致后续错误，但会记录日志）
		r.logger.Warnf("[WARN] Smart Routing | Not Found: %s | Will use as group ID (may fail)", modelInput)
		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	// 分割字符串
	parts := strings.SplitN(modelInput, "$", 2)
	if len(parts) != 2 {
		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	groupID, indexStr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	// 验证组名不为空
	if groupID == "" {
		return &models.RoutingInfo{GroupID: modelInput, IsPinned: false}
	}

	// 尝试解析索引
	userIndex, err := strconv.Atoi(indexStr)
	if err != nil {
		r.logger.Warnf("Invalid routing index '%s' (not a number), ignoring routing suffix", indexStr)
		return &models.RoutingInfo{GroupID: groupID, IsPinned: false}
	}

	if userIndex < 1 {
		r.logger.Warnf("Invalid routing index %d (must be >= 1), ignoring routing suffix", userIndex)
		return &models.RoutingInfo{GroupID: groupID, IsPinned: false}
	}

	// 转换为0-based索引
	targetIndex := userIndex - 1
	return &models.RoutingInfo{
		GroupID:    groupID,
		ModelIndex: &targetIndex,
		IsPinned:   true, // 显式指定索引时，启用锁定模式
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

	// 创建深拷贝
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
	// 检查状态码
	switch statusCode {
	case 400, 404, 405:
		return true // Bad Request, Not Found, Method Not Allowed
	}

	// 检查网络错误
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

// 统计相关方法
func (r *StatelessModelRouter) UpdateStats(groupID string, modelIndex int, success bool, latency float64) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// 获取或创建统计记录
	var stat *models.ModelStats
	if _, exists := r.stats[groupID][modelIndex]; !exists {
		r.stats[groupID][modelIndex] = &models.ModelStats{
			ModelGroupID: 0, // 将在数据库查询时设置
			ModelIndex:   modelIndex,
			Success:      0,
			Error:        0,
			TotalLatency: 0,
			RequestCount: 0,
		}
	}
	stat = r.stats[groupID][modelIndex]

	// 更新内存统计
	if success {
		stat.Success++
	} else {
		stat.Error++
	}
	stat.TotalLatency += latency
	stat.RequestCount++

	// 异步更新数据库
	go func() {
		var group models.ModelGroup
		if err := r.db.Where("group_id = ?", groupID).First(&group).Error; err != nil {
			r.logger.Errorf("Failed to find model group %s: %v", groupID, err)
			return
		}

		stat.ModelGroupID = group.ID
		if err := r.db.Save(stat).Error; err != nil {
			r.logger.Errorf("Failed to update stats in database: %v", err)
		}
	}()

	return nil
}

func (r *StatelessModelRouter) GetStats(groupID string, modelIndex int) *models.ModelStats {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	if _, exists := r.stats[groupID]; !exists {
		return &models.ModelStats{
			Success:      0,
			Error:        0,
			TotalLatency: 0,
			RequestCount: 0,
		}
	}

	stat, exists := r.stats[groupID][modelIndex]
	if !exists {
		return &models.ModelStats{
			Success:      0,
			Error:        0,
			TotalLatency: 0,
			RequestCount: 0,
		}
	}

	// 创建副本避免外部修改
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
		// 创建深拷贝
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
				Index:         i + 1, // 1-based for user display
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
			return true // 允许所有来源，生产环境应该更严格
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

	// 返回副本，避免外部修改
	result := make([]string, len(keys))
	copy(result, keys)
	return result, nil
}