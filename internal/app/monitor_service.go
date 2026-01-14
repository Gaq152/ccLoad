package app

import (
	"ccLoad/internal/storage"
	"context"
	"log"
	"sync"
	"sync/atomic"
)

// MonitorService 请求监控服务
// 提供请求/响应捕获、SSE 实时推送、存储管理功能
type MonitorService struct {
	enabled     atomic.Bool                                   // 监控开关
	store       *storage.TraceStore                           // 独立存储
	subscribers map[chan *storage.TraceListItem]struct{}      // SSE 订阅者
	mu          sync.RWMutex                                  // 保护 subscribers
	shutdownCh  chan struct{}                                 // 关闭信号
}

// NewMonitorService 创建监控服务
func NewMonitorService(traceStore *storage.TraceStore, shutdownCh chan struct{}) *MonitorService {
	return &MonitorService{
		store:       traceStore,
		subscribers: make(map[chan *storage.TraceListItem]struct{}),
		shutdownCh:  shutdownCh,
	}
}

// IsEnabled 检查监控是否开启
func (s *MonitorService) IsEnabled() bool {
	return s.enabled.Load()
}

// SetEnabled 设置监控开关
func (s *MonitorService) SetEnabled(enabled bool) {
	s.enabled.Store(enabled)
	if enabled {
		log.Print("[Monitor] 监控已开启")
	} else {
		log.Print("[Monitor] 监控已关闭")
	}
}

// Subscribe 订阅追踪事件（SSE）
func (s *MonitorService) Subscribe() chan *storage.TraceListItem {
	ch := make(chan *storage.TraceListItem, 100) // 带缓冲，防止慢消费者阻塞
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅
func (s *MonitorService) Unsubscribe(ch chan *storage.TraceListItem) {
	s.mu.Lock()
	delete(s.subscribers, ch)
	s.mu.Unlock()
	close(ch)
}

// Capture 捕获请求/响应（仅在监控开启时执行）
// 设计：异步执行，不阻塞请求处理
func (s *MonitorService) Capture(trace *storage.Trace) {
	if !s.enabled.Load() {
		return
	}
	// 异步保存和广播（不阻塞主请求）
	go s.saveAndBroadcast(trace)
}

// saveAndBroadcast 保存到数据库并广播给订阅者
func (s *MonitorService) saveAndBroadcast(trace *storage.Trace) {
	ctx := context.Background()

	// 保存到数据库
	id, err := s.store.Save(ctx, trace)
	if err != nil {
		log.Printf("[Monitor] 保存追踪记录失败: %v", err)
		return
	}
	trace.ID = id

	// 转换为列表项（不含请求体/响应体）
	item := &storage.TraceListItem{
		ID:            trace.ID,
		Time:          trace.Time,
		ChannelID:     trace.ChannelID,
		ChannelName:   trace.ChannelName,
		ChannelType:   trace.ChannelType,
		Model:         trace.Model,
		RequestPath:   trace.RequestPath,
		StatusCode:    trace.StatusCode,
		Duration:      trace.Duration,
		IsStreaming:   trace.IsStreaming,
		IsTest:        trace.IsTest,
		InputTokens:   trace.InputTokens,
		OutputTokens:  trace.OutputTokens,
		ClientIP:      trace.ClientIP,
		APIKeyUsed:    trace.APIKeyUsed,
		TokenID:       trace.TokenID,
		AuthTokenName: trace.AuthTokenName, // 从 Trace 传递（Capture 时填充）
	}

	// 广播给所有订阅者
	s.mu.RLock()
	for ch := range s.subscribers {
		select {
		case ch <- item:
			// 发送成功
		default:
			// 缓冲区满，跳过此订阅者（避免阻塞）
			log.Print("[Monitor] 订阅者缓冲区满，跳过")
		}
	}
	s.mu.RUnlock()
}

// GetStore 获取存储实例（供 Handler 使用）
func (s *MonitorService) GetStore() *storage.TraceStore {
	return s.store
}

// ClearAll 清空所有追踪记录
func (s *MonitorService) ClearAll(ctx context.Context) error {
	return s.store.Clear(ctx)
}
