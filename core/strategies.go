package core

import (
	"errors"
	"llm-gateway/models"
)

var (
	ErrNoModelsAvailable = errors.New("no models available in group")
)

// RoundRobinStrategy 轮询策略
type RoundRobinStrategy struct{}

func (s *RoundRobinStrategy) Name() string { return "round_robin" }

func (s *RoundRobinStrategy) Select(configs []*models.ModelConfig, counter uint64) (*models.ModelConfig, error) {
	if len(configs) == 0 {
		return nil, ErrNoModelsAvailable
	}
	// 纯算法逻辑，不依赖外部状态
	// counter 从 1 开始，所以使用 (counter - 1)
	idx := int((counter - 1) % uint64(len(configs)))
	return configs[idx], nil
}

// FallbackStrategy 故障转移/优先级策略
// 假设 configs 已经按优先级排序 (Order by ID or Priority field)
type FallbackStrategy struct{}

func (s *FallbackStrategy) Name() string { return "fallback" }

func (s *FallbackStrategy) Select(configs []*models.ModelConfig, _ uint64) (*models.ModelConfig, error) {
	if len(configs) == 0 {
		return nil, ErrNoModelsAvailable
	}
	// 总是返回优先级最高的第一个 (由调用者处理失败后的重试，或在此处结合健康检查)
	return configs[0], nil
}
