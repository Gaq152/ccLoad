package app

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// HandleLogSSE 日志实时推送 SSE 端点
// GET /admin/logs/stream
// 支持实时推送新产生的日志条目到前端
//
// 查询参数:
//   - since: Unix时间戳(秒)，连接时先推送此时间之后的历史日志（用于重连恢复）
func (s *Server) HandleLogSSE(c *gin.Context) {
	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁用 nginx 缓冲

	// 获取底层响应写入器
	w := c.Writer

	// 解析 since 参数（重连恢复）
	var sinceTime time.Time
	if sinceStr := c.Query("since"); sinceStr != "" {
		if ts, err := strconv.ParseInt(sinceStr, 10, 64); err == nil && ts > 0 {
			sinceTime = time.Unix(ts, 0)
		}
	}

	// 如果有 since 参数，先推送历史日志（重连恢复）
	if !sinceTime.IsZero() {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		// 获取 since 之后的日志，限制100条防止过载
		missedLogs, err := s.store.ListLogs(ctx, sinceTime, 100, 0, nil)
		cancel()

		if err == nil && len(missedLogs) > 0 {
			// 推送历史日志，标记为 recovery 事件
			for _, entry := range missedLogs {
				data, err := json.Marshal(entry)
				if err != nil {
					continue
				}
				if _, err := w.WriteString("event: log\ndata: "); err != nil {
					return
				}
				if _, err := w.Write(data); err != nil {
					return
				}
				if _, err := w.WriteString("\n\n"); err != nil {
					return
				}
			}
			w.Flush()
		}
	}

	// 订阅日志推送（在历史日志推送完成后订阅，避免重复）
	logCh := s.logService.Subscribe()
	defer s.logService.Unsubscribe(logCh)

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
