package core

import (
	"sync"
	"time"
)

// KeyStatusType Key状态枚举
type KeyStatusType int

const (
	KeyStatusAvailable KeyStatusType = iota
	KeyStatusCooldown
	KeyStatusDead
)

// KeyState Key的状态信息
type KeyState struct {
	Status    KeyStatusType
	UnlockTime time.Time
}

// KeyStateManager Key状态管理器 (线程安全)
type KeyStateManager struct {
	states map[string]KeyState // Key -> State
	mutex  sync.RWMutex
}

// GlobalKeyManager 全局Key管理器单例
var GlobalKeyManager = NewKeyStateManager()

func NewKeyStateManager() *KeyStateManager {
	return &KeyStateManager{
		states: make(map[string]KeyState),
	}
}

// MarkCooldown 标记Key为冷却状态
func (m *KeyStateManager) MarkCooldown(key string, duration time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.states[key] = KeyState{
		Status:    KeyStatusCooldown,
		UnlockTime: time.Now().Add(duration),
	}
}

// MarkDead 标记Key为失效
func (m *KeyStateManager) MarkDead(key string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.states[key] = KeyState{
		Status: KeyStatusDead,
	}
}

// MarkAvailable 标记Key为可用 (通常不需要显式调用，IsAvailable 会自动处理过期的 Cooldown)
func (m *KeyStateManager) MarkAvailable(key string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.states, key)
}

// IsAvailable 检查Key是否可用
func (m *KeyStateManager) IsAvailable(key string) bool {
	m.mutex.RLock()
	state, exists := m.states[key]
	m.mutex.RUnlock()

	if !exists {
		return true // 默认可用
	}

	if state.Status == KeyStatusDead {
		return false
	}

	if state.Status == KeyStatusCooldown {
		if time.Now().After(state.UnlockTime) {
			// 冷却结束，懒惰清理
			m.MarkAvailable(key)
			return true
		}
		return false
	}

	return true
}
