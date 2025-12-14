package models

import (
	"crypto/rand"
	"encoding/hex"
	"time"
	"gorm.io/gorm"
)

// GatewaySettings 网关全局设置
type GatewaySettings struct {
	gorm.Model
	Port    int    `gorm:"default:8000" json:"port"`
}

// AdminKey 管理员密钥
type AdminKey struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`                                    // 备注，如 "MacBook Pro"
	Key       string    `gorm:"uniqueIndex:idx_admin_key_deleted" json:"key"` // 实际的 sk-admin-xxx
	CreatedAt time.Time `json:"created_at"`
}

// ModelConfig 模型配置
type ModelConfig struct {
	gorm.Model
	ProviderName   string `gorm:"not null" json:"provider_name"`
	UpstreamURL    string `gorm:"not null" json:"upstream_url"`
	UpstreamModel  string `gorm:"not null" json:"upstream_model"`
	Timeout        int    `gorm:"default:60" json:"timeout"`
	ModelGroupID   uint   `json:"model_group_id"`

	// 关联关系
	ModelGroup     ModelGroup  `gorm:"foreignKey:ModelGroupID" json:"model_group,omitempty"`
	APIKeys        []APIKey    `gorm:"foreignKey:ModelConfigID" json:"api_keys,omitempty"`
	Stats          *ModelStats `gorm:"foreignKey:ModelConfigID" json:"stats,omitempty"` // 一对一关系
}

// APIKey API密钥
type APIKey struct {
	gorm.Model
	KeyValue      string `gorm:"not null" json:"key_value"`
	ModelConfigID uint   `json:"model_config_id"`

	// 关联关系
	ModelConfig ModelConfig `gorm:"foreignKey:ModelConfigID" json:"model_config,omitempty"`
}

// ModelGroup 模型组配置，支持策略配置
type ModelGroup struct {
	gorm.Model
	GroupID  string `gorm:"uniqueIndex:idx_group_id_deleted;not null" json:"group_id"`
	Strategy string `gorm:"default:fallback" json:"strategy"` // "fallback" 或 "round_robin"

	// 关联关系
	Models []ModelConfig `gorm:"foreignKey:ModelGroupID" json:"models,omitempty"`
	Stats  []ModelStats  `gorm:"foreignKey:ModelGroupID" json:"stats,omitempty"`
}

// ModelStats 模型统计信息
type ModelStats struct {
	gorm.Model
	ModelGroupID  uint  `json:"model_group_id"`
	ModelConfigID uint  `json:"model_config_id"` // 新增：关联到具体的模型配置
	ModelIndex    int   `json:"model_index"`      // 模型在组中的索引
	Success       int   `gorm:"default:0" json:"success"`
	Error         int   `gorm:"default:0" json:"error"`
	TotalLatency  float64 `gorm:"default:0" json:"total_latency"` // 毫秒
	RequestCount  int   `gorm:"default:0" json:"request_count"`
	TotalRequests int64 `gorm:"default:0" json:"total_requests"`  // 新增：总请求数（用于前端显示）

	// 关联关系
	ModelGroup  ModelGroup  `gorm:"foreignKey:ModelGroupID" json:"model_group,omitempty"`
	ModelConfig ModelConfig `gorm:"foreignKey:ModelConfigID" json:"model_config,omitempty"`
}

// RoutingInfo 路由信息（不存储到数据库）
type RoutingInfo struct {
	GroupID        string `json:"group_id"`
	ModelIndex     *int   `json:"model_index,omitempty"`     // nil 表示使用默认策略
	KeyIndex       *int   `json:"key_index,omitempty"`       // nil 表示使用默认策略
	IsPinned       bool   `json:"is_pinned"`                 // 是否为锁定模式（定向路由）
}

// AutoMigrate 自动迁移数据库结构
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&GatewaySettings{},
		&AdminKey{},
		&ModelGroup{},
		&ModelConfig{},
		&APIKey{},
		&ModelStats{},
	)
}

// GenerateAdminKey 生成管理员密钥
func GenerateAdminKey() string {
	// 生成 16 字节的随机字符串
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return "sk-admin-" + hex.EncodeToString(bytes)
}

// GenerateGatewayToken 生成网关访问令牌

// InitializeDefaultData 初始化默认数据
func InitializeDefaultData(db *gorm.DB) (string, error) {
	// 检查是否已有网关设置
	var count int64
	db.Model(&GatewaySettings{}).Count(&count)
	if count == 0 {
		// 创建默认网关设置
		gateway := GatewaySettings{
			Port:   8000,
		}
		if err := db.Create(&gateway).Error; err != nil {
			return "", err
		}
	}

	// 检查是否已有管理员密钥
	var adminCount int64
	db.Model(&AdminKey{}).Count(&adminCount)
	if adminCount == 0 {
		// 生成初始管理员密钥
		adminKey := AdminKey{
			Name: "Initial Root Key",
			Key:  GenerateAdminKey(),
		}
		if err := db.Create(&adminKey).Error; err != nil {
			return "", err
		}
		return adminKey.Key, nil
	}

	return "", nil
}