package app

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
)

// HandleCooldownSSE 冷却事件实时推送 SSE 端点
// GET /admin/cooldown/stream
func (s *Server) HandleCooldownSSE(c *gin.Context) {
	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	w := c.Writer

	// 订阅冷却事件
	eventCh := s.cooldownService.Subscribe()
	defer s.cooldownService.Unsubscribe(eventCh)

	// 发送连接成功事件
	if _, err := w.WriteString("event: connected\ndata: {\"status\":\"connected\"}\n\n"); err != nil {
		return
	}
	w.Flush()

	// 心跳定时器（每30秒发送一次，防止连接被中间代理超时断开）
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// 监听事件和客户端断开
	clientGone := c.Request.Context().Done()

	for {
		select {
		case <-clientGone:
			return
		case <-s.shutdownCh:
			return
		case <-heartbeat.C:
			// 发送心跳保活（SSE 注释格式，不触发前端事件）
			if _, err := w.WriteString(": heartbeat\n\n"); err != nil {
				return
			}
			w.Flush()
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			if err := writeSSECooldownEvent(w, event); err != nil {
				return
			}
			w.Flush()
		}
	}
}

// writeSSECooldownEvent 写入冷却事件到 SSE 流
func writeSSECooldownEvent(w gin.ResponseWriter, event *CooldownEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	if _, err := w.WriteString("event: cooldown\ndata: "); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if _, err := w.WriteString("\n\n"); err != nil {
		return err
	}
	return nil
}
