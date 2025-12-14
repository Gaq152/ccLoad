package app

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
)

// HandleLogSSE 日志实时推送 SSE 端点
// GET /admin/logs/stream
// 支持实时推送新产生的日志条目到前端
func (s *Server) HandleLogSSE(c *gin.Context) {
	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁用 nginx 缓冲

	// 订阅日志推送
	logCh := s.logService.Subscribe()
	defer s.logService.Unsubscribe(logCh)

	// 获取底层响应写入器
	w := c.Writer

	// 发送初始连接成功事件
	if _, err := w.WriteString("event: connected\ndata: {\"status\":\"connected\"}\n\n"); err != nil {
		return
	}
	w.Flush()

	// 监听日志和客户端断开
	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			// 客户端断开连接
			return

		case <-s.shutdownCh:
			// 服务器关闭
			_, _ = w.WriteString("event: close\ndata: {\"reason\":\"server_shutdown\"}\n\n")
			w.Flush()
			return

		case entry, ok := <-logCh:
			if !ok {
				// channel 已关闭
				return
			}

			// 序列化日志条目
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}

			// 发送 SSE 事件，写入失败时退出（客户端可能已断开）
			if _, err := w.WriteString("event: log\ndata: "); err != nil {
				return
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			if _, err := w.WriteString("\n\n"); err != nil {
				return
			}
			w.Flush()
		}
	}
}
