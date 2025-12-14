package app

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"ccLoad/internal/config"
	"ccLoad/internal/model"
	"ccLoad/internal/storage"
)

// LogService 日志管理服务
//
// 职责：处理所有日志相关的业务逻辑
// - 异步日志记录（批量写入）
// - 日志 Worker 管理
// - 日志清理（定时任务）
// - SSE 实时推送
// - 优雅关闭
//
// 遵循 SRP 原则：仅负责日志管理，不涉及代理、认证、管理 API
type LogService struct {
	store storage.Store

	// 日志队列和 Worker
	logChan      chan *model.LogEntry
	logWorkers   int
	logDropCount atomic.Uint64

	// 日志保留天数（启动时确定，修改后重启生效）
	retentionDays int

	// SSE 订阅者管理
	sseSubscribers      map[chan *model.LogEntry]struct{}
	sseSubscribersMu    sync.RWMutex
	sseDropCount        atomic.Uint64 // SSE 慢消费者丢弃计数（监控用）

	// 优雅关闭
	shutdownCh     chan struct{}
	isShuttingDown *atomic.Bool
	wg             *sync.WaitGroup
}

// NewLogService 创建日志服务实例
func NewLogService(
	store storage.Store,
	logBufferSize int,
	logWorkers int,
	retentionDays int, // 启动时确定，修改后重启生效
	shutdownCh chan struct{},
	isShuttingDown *atomic.Bool,
	wg *sync.WaitGroup,
) *LogService {
	return &LogService{
		store:          store,
		logChan:        make(chan *model.LogEntry, logBufferSize),
		logWorkers:     logWorkers,
		retentionDays:  retentionDays,
		sseSubscribers: make(map[chan *model.LogEntry]struct{}),
		shutdownCh:     shutdownCh,
		isShuttingDown: isShuttingDown,
		wg:             wg,
	}
}

// ============================================================================
// Worker 管理
// ============================================================================

// StartWorkers 启动日志 Worker
func (s *LogService) StartWorkers() {
	for i := 0; i < s.logWorkers; i++ {
		s.wg.Add(1)
		go s.logWorker()
	}
}

// logWorker 日志 Worker（后台协程）
func (s *LogService) logWorker() {
	defer s.wg.Done()

	batch := make([]*model.LogEntry, 0, config.LogBatchSize)
	ticker := time.NewTicker(config.LogBatchTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdownCh:
			// shutdown时尽量flush掉已排队的日志，避免“退出即丢日志”
			for {
				select {
				case entry, ok := <-s.logChan:
					if !ok {
						s.flushIfNeeded(batch)
						return
					}
					batch = append(batch, entry)
					if len(batch) >= config.LogBatchSize {
						s.flushLogs(batch)
						batch = batch[:0]
					}
				default:
					s.flushIfNeeded(batch)
					return
				}
			}

		case entry, ok := <-s.logChan:
			if !ok {
				// logChan已关闭，flush剩余日志并退出
				s.flushIfNeeded(batch)
				return
			}

			batch = append(batch, entry)
			if len(batch) >= config.LogBatchSize {
				s.flushLogs(batch)
				batch = batch[:0]
				ticker.Reset(config.LogBatchTimeout)
			}

		case <-ticker.C:
			// 移除嵌套select，简化定时flush逻辑
			// 设计原则：
			// - ticker触发时直接flush当前batch
			// - 如果logChan关闭，下次循环会在entry <- logChan中捕获
			// - shutdown信号在select中优先级最高，保证快速响应
			s.flushIfNeeded(batch)
			batch = batch[:0]
		}
	}
}

// flushLogs 批量写入日志
func (s *LogService) flushLogs(logs []*model.LogEntry) {
	// 为日志持久化增加超时控制，避免阻塞关闭或积压
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.LogFlushTimeoutMs)*time.Millisecond)
	defer cancel()

	// 使用批量写入接口（SQLite/MySQL均支持）
	_ = s.store.BatchAddLogs(ctx, logs)
}

// flushIfNeeded 辅助函数：当batch非空时执行flush
func (s *LogService) flushIfNeeded(batch []*model.LogEntry) {
	if len(batch) > 0 {
		s.flushLogs(batch)
	}
}

// ============================================================================
// 日志记录方法
// ============================================================================

// AddLogAsync 异步添加日志
func (s *LogService) AddLogAsync(entry *model.LogEntry) {
	// shutdown时不再写入日志
	if s.isShuttingDown.Load() {
		return
	}

	// SSE 广播：实时推送给所有订阅者
	s.broadcastToSSE(entry)

	select {
	case s.logChan <- entry:
		// 成功放入队列
	default:
		// 队列满，丢弃日志（计数用于监控）
		count := s.logDropCount.Add(1)
		// 采样告警：每100次丢弃打印一次，避免日志洪水
		if count%100 == 1 {
			log.Printf("[WARN]  日志队列已满，日志被丢弃 (累计丢弃: %d)", count)
		}
	}
}

// ============================================================================
// SSE 实时推送
// ============================================================================

// Subscribe 订阅日志 SSE 推送，返回一个接收日志的 channel
// 调用方需要在使用完毕后调用 Unsubscribe 取消订阅
func (s *LogService) Subscribe() chan *model.LogEntry {
	ch := make(chan *model.LogEntry, 64) // 缓冲区避免慢消费者阻塞
	s.sseSubscribersMu.Lock()
	s.sseSubscribers[ch] = struct{}{}
	s.sseSubscribersMu.Unlock()
	return ch
}

// Unsubscribe 取消订阅日志 SSE 推送
func (s *LogService) Unsubscribe(ch chan *model.LogEntry) {
	s.sseSubscribersMu.Lock()
	delete(s.sseSubscribers, ch)
	s.sseSubscribersMu.Unlock()
	close(ch)
}

// broadcastToSSE 向所有 SSE 订阅者广播日志
func (s *LogService) broadcastToSSE(entry *model.LogEntry) {
	s.sseSubscribersMu.RLock()
	defer s.sseSubscribersMu.RUnlock()

	for ch := range s.sseSubscribers {
		select {
		case ch <- entry:
			// 成功发送
		default:
			// 订阅者缓冲区满，跳过（避免阻塞主流程）
			// 计数用于监控慢消费者
			count := s.sseDropCount.Add(1)
			if count%100 == 1 {
				log.Printf("[WARN]  SSE 订阅者缓冲区满，日志被跳过 (累计跳过: %d)", count)
			}
		}
	}
}

// ============================================================================
// 日志清理
// ============================================================================

// StartCleanupLoop 启动日志清理后台协程
// 每小时检查一次，删除3天前的日志
// 支持优雅关闭
func (s *LogService) StartCleanupLoop() {
	s.wg.Add(1)
	go s.cleanupOldLogsLoop()
}

// cleanupOldLogsLoop 日志清理后台协程（私有方法）
func (s *LogService) cleanupOldLogsLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(config.LogCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 使用带超时的context，避免日志清理阻塞关闭流程。
			// [FIX] P0-4: WithTimeout 的 cancel 必须在每次循环内执行，不能在循环里 defer 到 goroutine 退出。
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				cutoff := time.Now().AddDate(0, 0, -s.retentionDays)

				// 通过Store接口清理旧日志，忽略错误（非关键操作）
				_ = s.store.CleanupLogsBefore(ctx, cutoff)
			}()

		case <-s.shutdownCh:
			// 收到关闭信号，直接退出（不执行最后一次清理）
			return
		}
	}
}

// ============================================================================
// 优雅关闭
// ============================================================================

// Shutdown 优雅关闭日志服务
// 注意：不需要等待 Workers，因为 Server 会通过 wg.Wait() 等待
func (s *LogService) Shutdown(ctx context.Context) error {
	// 关闭所有 SSE 订阅者
	s.sseSubscribersMu.Lock()
	for ch := range s.sseSubscribers {
		delete(s.sseSubscribers, ch)
		close(ch)
	}
	s.sseSubscribersMu.Unlock()

	// 不关闭logChan：channel关闭与并发send存在天然竞态，panic只会把进程炸掉。
	// Worker通过shutdownCh退出；shutdown时的日志flush由logWorker负责。
	return nil
}
