package core

import (
	"llm-gateway/models"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// AsyncRequestLogger 异步请求日志记录器
type AsyncRequestLogger struct {
	db        *gorm.DB
	logChan   chan *models.RequestLog
	logger    *logrus.Logger
	batchSize int
	flushTime time.Duration
	wg        sync.WaitGroup
	quit      chan struct{}
}

// NewAsyncRequestLogger 创建新的异步日志记录器
func NewAsyncRequestLogger(db *gorm.DB, logger *logrus.Logger) *AsyncRequestLogger {
	l := &AsyncRequestLogger{
		db:        db,
		logChan:   make(chan *models.RequestLog, 1000), // 缓冲 1000 条
		logger:    logger,
		batchSize: 100,             // 批量插入大小
		flushTime: 5 * time.Second, // 最长等待时间
		quit:      make(chan struct{}),
	}
	l.startWorker()
	return l
}

// Log 提交日志到队列
func (l *AsyncRequestLogger) Log(log *models.RequestLog) {
	select {
	case l.logChan <- log:
		// Success
	default:
		// 如果队列满了，丢弃日志以防止阻塞业务，或者记录错误
		l.logger.Warn("Log channel full, dropping request log")
	}
}

// startWorker 启动后台写入 Worker
func (l *AsyncRequestLogger) startWorker() {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		l.workerLoop()
	}()
}

// workerLoop 核心循环
func (l *AsyncRequestLogger) workerLoop() {
	var batch []*models.RequestLog
	timer := time.NewTicker(l.flushTime)
	defer timer.Stop()

	for {
		select {
		case log := <-l.logChan:
			batch = append(batch, log)
			if len(batch) >= l.batchSize {
				l.flush(batch)
				batch = nil // Reset
			}
		case <-timer.C:
			if len(batch) > 0 {
				l.flush(batch)
				batch = nil
			}
		case <-l.quit:
			// 退出前刷新剩余日志
			if len(batch) > 0 {
				l.flush(batch)
			}
			return
		}
	}
}

// flush 批量写入数据库并更新统计
func (l *AsyncRequestLogger) flush(logs []*models.RequestLog) {
	if len(logs) == 0 {
		return
	}
	
	// 1. 批量插入日志
	if err := l.db.CreateInBatches(logs, len(logs)).Error; err != nil {
		l.logger.Errorf("Failed to flush logs to database: %v", err)
	}

	// 2. 聚合统计更新 (内存聚合以减少 DB 锁竞争)
	// Key: ModelConfigID
	type statDelta struct {
		Success       int
		Error         int
		TotalLatency  float64
		RequestCount  int
		ModelGroupID  uint
	}
	statsMap := make(map[uint]*statDelta)

	for _, log := range logs {
		// 跳过未关联模型的日志（如 404/401 或系统日志）
		if log.ModelConfigID == 0 {
			continue
		}

		delta, exists := statsMap[log.ModelConfigID]
		if !exists {
			delta = &statDelta{ModelGroupID: log.ModelGroupID}
			statsMap[log.ModelConfigID] = delta
		}

		delta.RequestCount++
		if log.StatusCode >= 200 && log.StatusCode < 500 && log.StatusCode != 429 {
			delta.Success++
		} else {
			delta.Error++
		}
		delta.TotalLatency += float64(log.Duration)
	}

	// 3. 执行批量更新
	// 由于 GORM 不支持完美的 Batch Update Increment，我们使用原生 SQL 或循环更新
	// 考虑到 batchSize 较小 (100)，循环更新是可以接受的，或者使用 Case When
	// 为了简单且安全，我们对涉及的模型执行原子更新 SQL
	for modelID, delta := range statsMap {
		err := l.db.Exec(`
			INSERT INTO model_stats (created_at, updated_at, model_config_id, model_group_id, success, error, total_latency, request_count, total_requests)
			VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(model_config_id) DO UPDATE SET
				updated_at = CURRENT_TIMESTAMP,
				success = success + excluded.success,
				error = error + excluded.error,
				total_latency = total_latency + excluded.total_latency,
				request_count = request_count + excluded.request_count,
				total_requests = total_requests + excluded.total_requests
		`, modelID, delta.ModelGroupID, delta.Success, delta.Error, delta.TotalLatency, delta.RequestCount, delta.RequestCount).Error

		if err != nil {
			// 如果 SQLite 不支持 ON CONFLICT (旧版本)，尝试先 Update 若无则 Create
			// 但 GORM AutoMigrate 通常不建立唯一索引 unless defined in struct
			// 我们需要在 schema.go 确保 ModelStats 对 ModelConfigID 有唯一索引，或者手动处理
			// 假设 ModelStats 与 ModelConfig 是一对一关系，我们应该先尝试 Update
			
			// Fallback Update
			res := l.db.Exec(`
				UPDATE model_stats SET 
					success = success + ?, 
					error = error + ?, 
					total_latency = total_latency + ?, 
					request_count = request_count + ?,
					total_requests = total_requests + ?
				WHERE model_config_id = ?
			`, delta.Success, delta.Error, delta.TotalLatency, delta.RequestCount, delta.RequestCount, modelID)
			
			if res.Error != nil {
				l.logger.Errorf("Failed to update stats for model %d: %v", modelID, res.Error)
			} else if res.RowsAffected == 0 {
				// Insert new record
				newStat := models.ModelStats{
					ModelConfigID: modelID,
					ModelGroupID:  delta.ModelGroupID,
					Success:       delta.Success,
					Error:         delta.Error,
					TotalLatency:  delta.TotalLatency,
					RequestCount:  delta.RequestCount,
					TotalRequests: int64(delta.RequestCount),
				}
				l.db.Create(&newStat)
			}
		}
	}
}

// Close 关闭日志记录器
func (l *AsyncRequestLogger) Close() {
	close(l.quit)
	l.wg.Wait()
	close(l.logChan)
}
