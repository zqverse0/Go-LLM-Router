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

	// 3. 初始化 Router
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	router, err := NewStatelessModelRouter(db, logger)
	assert.NoError(t, err)

	// 4. 设置 Key 状态
	// Key A -> Cooldown
	router.keyManager.MarkCooldown("sk-A", 60*time.Second)
	// Key B -> Dead
	router.keyManager.MarkDead("sk-B")
	// Key C -> Normal

	// 5. 验证 Failover
	// 无论调用多少次，都应该返回 Key C 的索引 (即 2)
	// 注意：GetInitialKeyIndex 返回的是 Key 在列表中的索引
	// 数据库中插入顺序是 A(0), B(1), C(2)
	
	// 这里的测试逻辑依赖于 Router 加载 Key 的顺序，通常是 ID 顺序
	loadedKeys, err := router.GetModelKeys(model.ID)
	assert.NoError(t, err)
	assert.Equal(t, "sk-A", loadedKeys[0])
	assert.Equal(t, "sk-B", loadedKeys[1])
	assert.Equal(t, "sk-C", loadedKeys[2])

	// 尝试多次获取
	for i := 0; i < 10; i++ {
		idx := router.GetInitialKeyIndex(model.ID)
		assert.Equal(t, 2, idx, "Should always select Key C (index 2)")
	}

	// 6. 验证恢复
	// 清除 A 的 Cooldown
	router.keyManager.MarkAvailable("sk-A")
	
	// 现在应该可以在 A 和 C 之间轮询 (B 仍然 Dead)
	// Round Robin 可能会从 A 或 C 开始
	seenA := false
	seenC := false
	
	// 由于 Round Robin 计数器是全局的，我们需要多次调用来覆盖所有情况
	for i := 0; i < 20; i++ {
		// 必须调用 GetInitialModelIndex 来推进全局计数器
		router.GetInitialModelIndex(group.GroupID)
		
		idx := router.GetInitialKeyIndex(model.ID)
		if idx == 0 {
			seenA = true
		} else if idx == 2 {
			seenC = true
		} else if idx == 1 {
			t.Fatal("Should never select Dead Key B")
		}
	}
	
	assert.True(t, seenA, "Should eventually select Key A after recovery")
	assert.True(t, seenC, "Should eventually select Key C")
}
