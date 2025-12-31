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
// - 每日统计聚合（在清理前执行）
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
	// 统计数据保留天数
	statsRetentionDays int

	// SSE 订阅者管理
	sseSubscribers   map[chan *model.LogEntry]struct{}
	sseSubscribersMu sync.RWMutex
	sseDropCount     atomic.Uint64 // SSE 慢消费者丢弃计数（监控用）

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
	statsRetentionDays int, // 统计数据保留天数
	shutdownCh chan struct{},
	isShuttingDown *atomic.Bool,
	wg *sync.WaitGroup,
) *LogService {
	return &LogService{
		store:              store,
		logChan:            make(chan *model.LogEntry, logBufferSize),
		logWorkers:         logWorkers,
		retentionDays:      retentionDays,
		statsRetentionDays: statsRetentionDays,
		sseSubscribers:     make(map[chan *model.LogEntry]struct{}),
		shutdownCh:         shutdownCh,
		isShuttingDown:     isShuttingDown,
		wg:                 wg,
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
	defer func() {
		log.Print("[DEBUG] logWorker 退出")
		s.wg.Done()
	}()

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
	if err := s.store.BatchAddLogs(ctx, logs); err != nil {
		log.Printf("[ERROR] 日志批量写入失败 (batch_size=%d): %v", len(logs), err)
	}
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
		// 降低采样频率：每10次丢弃打印一次（原来是100次）
		if count%10 == 1 {
			log.Printf("[ERROR] 日志队列已满，日志被丢弃 (累计丢弃: %d) - 考虑增大LOG_BUFFER_SIZE或LOG_WORKERS", count)
		}
	}
}

// ============================================================================
// SSE 实时推送
// ============================================================================

// Subscribe 订阅日志 SSE 推送，返回一个接收日志的 channel
// 调用方需要在使用完毕后调用 Unsubscribe 取消订阅
func (s *LogService) Subscribe() chan *model.LogEntry {
	// 缓冲区设为256：平衡内存占用与日志丢失风险
	// 原64在高并发时容易溢出导致日志丢失
	ch := make(chan *model.LogEntry, 256)
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
			// 缓冲区满：挤掉最旧的一条，保留最新日志（实时性优先）
			select {
			case <-ch: // 丢弃最旧的
			default:
			}
			// 再次尝试塞入最新日志
			select {
			case ch <- entry:
			default:
			}
			count := s.sseDropCount.Add(1)
			if count%100 == 1 {
				log.Printf("[WARN]  SSE 缓冲区满，挤掉旧日志保持实时性 (累计: %d)", count)
			}
		}
	}
}

// ============================================================================
// 日志清理与统计聚合
// ============================================================================

// StartCleanupLoop 启动日志清理后台协程
// 每小时检查一次，删除过期日志
// 在清理前先聚合统计数据，确保历史数据不丢失
// 支持优雅关闭
func (s *LogService) StartCleanupLoop() {
	s.wg.Add(1)
	go s.cleanupOldLogsLoop()
}

// cleanupOldLogsLoop 日志清理后台协程（私有方法）
func (s *LogService) cleanupOldLogsLoop() {
	defer func() {
		log.Print("[DEBUG] cleanupOldLogsLoop 退出")
		s.wg.Done()
	}()

	ticker := time.NewTicker(config.LogCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdownCh:
			// 收到关闭信号，直接退出（不执行最后一次清理）
			return

		case <-ticker.C:
			// 清理前先检查是否正在关闭
			select {
			case <-s.shutdownCh:
				return
			default:
			}

			// 使用可被 shutdown 取消的 context，避免清理阻塞关闭流程
			ctx, cancel := context.WithCancel(context.Background())

			// 监听 shutdownCh，提前取消清理操作
			go func() {
				select {
				case <-s.shutdownCh:
					cancel()
				case <-ctx.Done():
				}
			}()

			// 1. 先聚合统计数据（在清理日志前）
			s.aggregatePendingDays(ctx)

			// 2. 清理过期日志
			if s.retentionDays > 0 {
				cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
				if err := s.store.CleanupLogsBefore(ctx, cutoff); err != nil {
					log.Printf("[ERROR] 清理过期日志失败: %v", err)
				}
			}

			// 3. 清理过期统计数据
			if s.statsRetentionDays > 0 {
				statsCutoff := time.Now().AddDate(0, 0, -s.statsRetentionDays)
				if err := s.store.CleanupDailyStatsBefore(ctx, statsCutoff); err != nil {
					log.Printf("[ERROR] 清理过期统计数据失败: %v", err)
				}
			}

			cancel() // 清理完成，取消 context
		}
	}
}

// aggregatePendingDays 聚合待处理的日期（从最后聚合日期到昨天）
func (s *LogService) aggregatePendingDays(ctx context.Context) {
	// 获取最新聚合日期
	latestDate, err := s.store.GetLatestDailyStatsDate(ctx)
	if err != nil {
		log.Printf("[ERROR] 获取最新统计日期失败: %v", err)
		return
	}

	// 计算需要聚合的日期范围
	yesterday := time.Now().AddDate(0, 0, -1)
	yesterday = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, yesterday.Location())

	var startDate time.Time
	if latestDate.IsZero() {
		// 没有统计数据，从日志保留天数前开始（最多聚合保留范围内的数据）
		startDate = time.Now().AddDate(0, 0, -s.retentionDays)
		startDate = time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, startDate.Location())
	} else {
		// 从最后聚合日期的下一天开始
		startDate = latestDate.AddDate(0, 0, 1)
	}

	// 聚合每一天的数据
	aggregatedCount := 0
	for date := startDate; !date.After(yesterday); date = date.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return // 被取消
		default:
		}

		if err := s.store.AggregateDailyStats(ctx, date); err != nil {
			log.Printf("[ERROR] 聚合 %s 统计数据失败: %v", date.Format("2006-01-02"), err)
			continue
		}
		aggregatedCount++
	}

	if aggregatedCount > 0 {
		log.Printf("[INFO] 已聚合 %d 天的统计数据", aggregatedCount)
	}
}

// BackfillDailyStats 补全历史统计数据（启动时调用）
// 从日志中聚合所有缺失的历史数据
func (s *LogService) BackfillDailyStats(ctx context.Context) {
	log.Print("[INFO] 开始补全历史统计数据...")
	s.aggregatePendingDays(ctx)
	log.Print("[INFO] 历史统计数据补全完成")
}
