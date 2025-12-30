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

// flush 批量写入数据库
func (l *AsyncRequestLogger) flush(logs []*models.RequestLog) {
	if len(logs) == 0 {
		return
	}
	// GORM 批量插入
	if err := l.db.CreateInBatches(logs, len(logs)).Error; err != nil {
		l.logger.Errorf("Failed to flush logs to database: %v", err)
	}
}

// Close 关闭日志记录器
func (l *AsyncRequestLogger) Close() {
	close(l.quit)
	l.wg.Wait()
	close(l.logChan)
}
