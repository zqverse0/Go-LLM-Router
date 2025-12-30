package core

import (
	"llm-gateway/models"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSmartKeyFailover(t *testing.T) {
	// 1. 初始化内存数据库
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	assert.NoError(t, err)
	
	err = models.AutoMigrate(db)
	assert.NoError(t, err)

	// Insert GatewaySettings
	db.Create(&models.GatewaySettings{Port: 8000})

	// 2. 插入测试数据
	// 创建组
	group := models.ModelGroup{
		GroupID: "test-group",
		Strategy: "round_robin",
	}
	db.Create(&group)

	// 创建模型
	model := models.ModelConfig{
		ProviderName: "openai",
		UpstreamModel: "gpt-4",
		UpstreamURL: "https://api.openai.com",
		ModelGroupID: group.ID,
	}
	db.Create(&model)

	// 创建3个Key
	keys := []string{"sk-A", "sk-B", "sk-C"}
	for _, k := range keys {
		db.Create(&models.APIKey{
			KeyValue: k,
			ModelConfigID: model.ID,
		})
	}

	// 3. 初始化 LoadBalancer
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	
	km := NewKeyStateManager()
	sp := NewNoOpSecretProvider()
	
	lb, err := NewLoadBalancer(db, logger, km, sp)
	assert.NoError(t, err)

	// 4. 设置 Key 状态
	// Key A -> Cooldown
	km.MarkCooldown("sk-A", 60*time.Second)
	// Key B -> Dead
	km.MarkDead("sk-B")
	// Key C -> Normal

	// 5. 验证 Failover
	// 无论调用多少次，都应该返回 Key C
	
	for i := 0; i < 10; i++ {
		routing, err := lb.Route(group.GroupID)
		assert.NoError(t, err)
		assert.Equal(t, "sk-C", routing.APIKey, "Should always select Key C")
	}

	// 6. 验证恢复
	// 清除 A 的 Cooldown (通过修改时间或直接 MarkAvailable)
	km.MarkAvailable("sk-A")
	
	// 现在应该可以在 A 和 C 之间轮询 (B 仍然 Dead)
	seenA := false
	seenC := false
	
	for i := 0; i < 20; i++ {
		routing, err := lb.Route(group.GroupID)
		assert.NoError(t, err)
		
		if routing.APIKey == "sk-A" {
			seenA = true
		} else if routing.APIKey == "sk-C" {
			seenC = true
		} else if routing.APIKey == "sk-B" {
			t.Fatal("Should never select Dead Key B")
		}
	}
	
	assert.True(t, seenA, "Should eventually select Key A after recovery")
	assert.True(t, seenC, "Should eventually select Key C")
}