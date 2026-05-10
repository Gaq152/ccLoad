package app

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"ccLoad/internal/model"

	"github.com/gin-gonic/gin"
)

// HandleLogSSE 日志实时推送 SSE 端点
// GET /admin/logs/stream
// 支持实时推送新产生的日志条目到前端
//
// 查询参数:
//   - since_id: 日志 ID，连接时先推送此 ID 之后的历史日志（推荐，精度可靠）
//   - since_ms: Unix毫秒时间戳（兼容保留）
//   - since: Unix秒时间戳（向后兼容）
func (s *Server) HandleLogSSE(c *gin.Context) {
	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁用 nginx 缓冲

	// 获取底层响应写入器
	w := c.Writer

	// 优先使用 since_id（精确），其次 since_ms
	sinceID := parseSinceID(c)
	sinceMs := parseSinceMs(c)
	var sinceTime time.Time
	if sinceID > 0 {
		// 基于 ID 恢复时，时间窗口取最近 24 小时即可覆盖常见重连场景
		sinceTime = time.Now().Add(-24 * time.Hour)
	} else if sinceMs > 0 {
		sinceTime = time.UnixMilli(sinceMs + 1)
	}

	// 如果有恢复参数，先推送历史日志
	if !sinceTime.IsZero() {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		missedLogs, err := s.store.ListLogs(ctx, sinceTime, 500, 0, nil)
		cancel()

		if err == nil && len(missedLogs) > 0 {
			// 基于 ID 过滤（精度可靠，避免时间戳精度问题）
			if sinceID > 0 {
				filtered := missedLogs[:0]
				for _, entry := range missedLogs {
					if entry.ID > sinceID {
						filtered = append(filtered, entry)
					}
				}
				missedLogs = filtered
			}

			// 反转顺序：数据库返回 DESC（新→旧），改为 ASC（旧→新）
			// 前端 prepend 时，旧的先插入，新的后插入到顶部，最终新的在最上面
			for i, j := 0, len(missedLogs)-1; i < j; i, j = i+1, j-1 {
				missedLogs[i], missedLogs[j] = missedLogs[j], missedLogs[i]
			}

			// 推送历史日志（从旧到新）
			for _, entry := range missedLogs {
				if err := writeSSELog(w, entry); err != nil {
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

	// 心跳定时器（每30秒发送一次，防止连接被中间代理超时断开）
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

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

		case <-heartbeat.C:
			// 发送心跳保活（SSE 注释格式，不触发前端事件）
			if _, err := w.WriteString(": heartbeat\n\n"); err != nil {
				return
			}
			w.Flush()

		case entry, ok := <-logCh:
			if !ok {
				// channel 已关闭
				return
			}

			if err := writeSSELog(w, entry); err != nil {
				return
			}
			w.Flush()
		}
	}
}

// parseSinceID 解析 since_id 参数
func parseSinceID(c *gin.Context) int64 {
	raw := c.Query("since_id")
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// parseSinceMs 解析 since 参数，支持秒/毫秒（13位视为毫秒）
func parseSinceMs(c *gin.Context) int64 {
	// 优先使用 since_ms
	raw := c.Query("since_ms")
	if raw == "" {
		raw = c.Query("since")
	}
	if raw == "" {
		return 0
	}
	ts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ts <= 0 {
		return 0
	}
	// 13位及以上视为毫秒，10位视为秒
	if len(raw) >= 13 {
		return ts
	}
	return ts * 1000
}

// sseLogEntry 包装 LogEntry 并附带毫秒时间戳，用于前端精确去重
type sseLogEntry struct {
	*model.LogEntry
	TimeMs int64 `json:"time_ms"` // 毫秒时间戳，防止同秒内日志被去重
}

// writeSSELog 写入单条日志到 SSE 响应
func writeSSELog(w gin.ResponseWriter, entry *model.LogEntry) error {
	ts := entry.Time.Time
	if ts.IsZero() {
		ts = time.Now()
	}

	// 包装日志，添加毫秒时间戳
	wrapped := sseLogEntry{
		LogEntry: entry,
		TimeMs:   ts.UnixMilli(),
	}

	data, err := json.Marshal(wrapped)
	if err != nil {
		return nil // 跳过序列化失败的日志
	}

	if _, err := w.WriteString("event: log\ndata: "); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.WriteString("\n\n")
	return err
}

// HandleActiveRequests 获取当前活跃请求列表
// GET /admin/logs/active
func (s *Server) HandleActiveRequests(c *gin.Context) {
	requests := s.activeReqManager.List()
	RespondJSON(c, 200, requests)
}
