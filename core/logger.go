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
	
	l.logger.Infof("[Logger] Flushing %d logs to DB...", len(logs))

	// 1. 批量插入日志 (Restored for Request History UI)
	if err := l.db.CreateInBatches(logs, len(logs)).Error; err != nil {
		l.logger.Errorf("[Logger] Failed to flush logs: %v", err)
	}

	// 2. 严格清理 (Strict Pruning): 只保留最新的 100 条
	// 这保证了数据库永远不会膨胀，同时让前端有数据可看
	go func() {
		var count int64
		l.db.Model(&models.RequestLog{}).Count(&count)
		if count > 100 {
			var pivotID uint
			// 找到第 100 条最新的日志 ID
			l.db.Model(&models.RequestLog{}).Select("id").Order("id desc").Offset(100).Limit(1).Scan(&pivotID)
			if pivotID > 0 {
				// 删除比它旧的所有记录
				l.db.Where("id <= ?", pivotID).Delete(&models.RequestLog{})
			}
		}
	}()

	// 3. 聚合统计更新
	type statDelta struct {
		Success       int
		Error         int
		TotalLatency  float64
		RequestCount  int
		ModelGroupID  uint
	}
	statsMap := make(map[uint]*statDelta)

	for _, log := range logs {
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

	// 3. 执行更新 (Robust Upsert)
	for modelID, delta := range statsMap {
		// First try to find existing stat
		var stat models.ModelStats
		err := l.db.Where("model_config_id = ?", modelID).First(&stat).Error
		
		if err == nil {
			// Update existing
			stat.Success += delta.Success
			stat.Error += delta.Error
			stat.TotalLatency += delta.TotalLatency
			stat.RequestCount += delta.RequestCount
			stat.TotalRequests += int64(delta.RequestCount)
			l.db.Save(&stat)
		} else {
			// Create new
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

// Close 关闭日志记录器
func (l *AsyncRequestLogger) Close() {
	close(l.quit)
	l.wg.Wait()
	close(l.logChan)
}
