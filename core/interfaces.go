package core

import (
	"llm-gateway/models"
	"time"
)

// Strategy 定义路由策略接口 (Task 1)
// 输入一组模型配置和状态计数器，输出一个被选中的模型
type Strategy interface {
	// Name 返回策略名称，如 "round_robin", "fallback"
	Name() string
	
	// Select 执行选择逻辑
	// models: 候选模型列表
	// counter: 该组的原子计数器快照 (用于轮询)
	Select(configs []*models.ModelConfig, counter uint64) (*models.ModelConfig, error)
}

// KeyManager 抽象密钥状态管理 (Task 2: DI)
// 原 KeyStateManager 需实现此接口
type KeyManager interface {
	IsAvailable(key string) bool
	MarkCooldown(key string, duration time.Duration)
	MarkDead(key string)
}

// SecretProvider 抽象密钥加解密 (Task 4)
// 用于读取配置时自动解密 API Key
type SecretProvider interface {
	Decrypt(ciphertext string) (string, error)
	Encrypt(plaintext string) (string, error)
}