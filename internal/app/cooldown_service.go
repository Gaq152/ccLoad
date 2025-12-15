package app

import (
	"sync"
	"sync/atomic"
	"time"
)

// CooldownEvent 冷却事件（用于 SSE 推送）
type CooldownEvent struct {
	Type        string    `json:"type"`         // "channel" 或 "key"
	ChannelID   int64     `json:"channel_id"`
	ChannelName string    `json:"channel_name,omitempty"`
	KeyIndex    int       `json:"key_index,omitempty"`    // 仅 key 类型有效
	CooldownMs  int64     `json:"cooldown_ms"`            // 冷却时长（毫秒）
	Until       time.Time `json:"until"`                  // 冷却结束时间
	StatusCode  int       `json:"status_code,omitempty"`  // 触发冷却的状态码
	Timestamp   int64     `json:"timestamp"`              // 事件时间戳（毫秒）
}

// CooldownService 冷却事件广播服务
type CooldownService struct {
	// SSE 订阅者管理
	subscribers   map[chan *CooldownEvent]struct{}
	subscribersMu sync.RWMutex
	dropCount     atomic.Uint64

	// 优雅关闭
	shutdownCh     chan struct{}
	isShuttingDown *atomic.Bool
}

// NewCooldownService 创建冷却事件服务
func NewCooldownService(shutdownCh chan struct{}, isShuttingDown *atomic.Bool) *CooldownService {
	return &CooldownService{
		subscribers:    make(map[chan *CooldownEvent]struct{}),
		shutdownCh:     shutdownCh,
		isShuttingDown: isShuttingDown,
	}
}

// Subscribe 订阅冷却事件
func (s *CooldownService) Subscribe() chan *CooldownEvent {
	ch := make(chan *CooldownEvent, 64)
	s.subscribersMu.Lock()
	s.subscribers[ch] = struct{}{}
	s.subscribersMu.Unlock()
	return ch
}

// Unsubscribe 取消订阅
func (s *CooldownService) Unsubscribe(ch chan *CooldownEvent) {
	s.subscribersMu.Lock()
	delete(s.subscribers, ch)
	s.subscribersMu.Unlock()
	close(ch)
}

// Broadcast 广播冷却事件
func (s *CooldownService) Broadcast(event *CooldownEvent) {
	if s.isShuttingDown.Load() {
		return
	}

	s.subscribersMu.RLock()
	defer s.subscribersMu.RUnlock()

	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// 缓冲区满，丢弃旧事件
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- event:
			default:
			}
			s.dropCount.Add(1)
		}
	}
}

// BroadcastChannelCooldown 广播渠道冷却事件
// 签名匹配 cooldown.CooldownCallback（keyIndex 对渠道冷却无意义，忽略）
func (s *CooldownService) BroadcastChannelCooldown(channelID int64, channelName string, keyIndex int, until time.Time, statusCode int) {
	event := &CooldownEvent{
		Type:        "channel",
		ChannelID:   channelID,
		ChannelName: channelName,
		CooldownMs:  time.Until(until).Milliseconds(),
		Until:       until,
		StatusCode:  statusCode,
		Timestamp:   time.Now().UnixMilli(),
	}
	s.Broadcast(event)
}

// BroadcastKeyCooldown 广播 Key 冷却事件
func (s *CooldownService) BroadcastKeyCooldown(channelID int64, channelName string, keyIndex int, until time.Time, statusCode int) {
	event := &CooldownEvent{
		Type:        "key",
		ChannelID:   channelID,
		ChannelName: channelName,
		KeyIndex:    keyIndex,
		CooldownMs:  time.Until(until).Milliseconds(),
		Until:       until,
		StatusCode:  statusCode,
		Timestamp:   time.Now().UnixMilli(),
	}
	s.Broadcast(event)
}

// Shutdown 关闭服务
func (s *CooldownService) Shutdown() {
	s.subscribersMu.Lock()
	for ch := range s.subscribers {
		delete(s.subscribers, ch)
		close(ch)
	}
	s.subscribersMu.Unlock()
}
