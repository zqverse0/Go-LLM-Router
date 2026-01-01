package core

import (
	"fmt"
	"os"
	"sync"
)

// LogRotator 实现带轮转的文件写入器
type LogRotator struct {
	filename   string
	maxSize    int64 // bytes
	file       *os.File
	mu         sync.Mutex
	currentSize int64
}

// NewLogRotator 创建新的日志轮转器 (maxSize in MB)
func NewLogRotator(filename string, maxSizeMB int) (*LogRotator, error) {
	r := &LogRotator{
		filename: filename,
		maxSize:  int64(maxSizeMB) * 1024 * 1024,
	}
	if err := r.openFile(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *LogRotator) openFile() error {
	file, err := os.OpenFile(r.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	r.file = file
	r.currentSize = stat.Size()
	return nil
}

func (r *LogRotator) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	writeLen := int64(len(p))
	if r.currentSize+writeLen > r.maxSize {
		if err := r.rotate(); err != nil {
			// 如果轮转失败，尝试继续写入当前文件
			fmt.Fprintf(os.Stderr, "Log rotation failed: %v\n", err)
		}
	}

	n, err = r.file.Write(p)
	r.currentSize += int64(n)
	return n, err
}

func (r *LogRotator) rotate() error {
	// 关闭当前文件
	if r.file != nil {
		r.file.Close()
	}

	// 乒乓轮转策略: 只保留一个备份
	// 1. 删除旧的备份 (gateway.log.old)
	backupName := r.filename + ".old"
	os.Remove(backupName) // 忽略错误，文件可能不存在

	// 2. 将当前日志重命名为备份 (gateway.log -> gateway.log.old)
	if err := os.Rename(r.filename, backupName); err != nil {
		return err
	}

	// 3. 重新打开新文件 (gateway.log)
	return r.openFile()
}

func (r *LogRotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}
