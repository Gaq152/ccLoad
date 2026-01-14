package app

import (
	"encoding/json"
	"strconv"
	"time"

	"ccLoad/internal/storage"

	"github.com/gin-gonic/gin"
)

// HandleMonitorStatus 获取监控状态
// GET /admin/monitor/status
func (s *Server) HandleMonitorStatus(c *gin.Context) {
	if s.monitorService == nil {
		RespondErrorMsg(c, 503, "监控服务不可用")
		return
	}

	RespondJSON(c, 200, gin.H{
		"enabled": s.monitorService.IsEnabled(),
	})
}

// HandleMonitorToggle 切换监控开关
// POST /admin/monitor/toggle
// Body: {"enabled": true/false}
func (s *Server) HandleMonitorToggle(c *gin.Context) {
	if s.monitorService == nil {
		RespondErrorMsg(c, 503, "监控服务不可用")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, 400, "无效的请求")
		return
	}

	s.monitorService.SetEnabled(req.Enabled)
	RespondJSON(c, 200, gin.H{"enabled": req.Enabled})
}

// HandleMonitorSSE 监控事件实时推送
// GET /admin/monitor/stream
func (s *Server) HandleMonitorSSE(c *gin.Context) {
	if s.monitorService == nil {
		RespondErrorMsg(c, 503, "监控服务不可用")
		return
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁用 nginx 缓冲

	w := c.Writer

	// 订阅追踪事件
	traceCh := s.monitorService.Subscribe()
	defer s.monitorService.Unsubscribe(traceCh)

	// 发送连接成功事件
	if _, err := w.WriteString("event: connected\ndata: {\"status\":\"connected\"}\n\n"); err != nil {
		return
	}
	w.Flush()

	// 心跳定时器（每30秒）
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			// 客户端断开
			return

		case <-s.shutdownCh:
			// 服务器关闭
			_, _ = w.WriteString("event: close\ndata: {\"reason\":\"server_shutdown\"}\n\n")
			w.Flush()
			return

		case <-heartbeat.C:
			// 心跳保活
			if _, err := w.WriteString(": heartbeat\n\n"); err != nil {
				return
			}
			w.Flush()

		case trace, ok := <-traceCh:
			if !ok {
				return
			}
			// 发送追踪事件
			if err := writeSSETrace(w, trace); err != nil {
				return
			}
			w.Flush()
		}
	}
}

// writeSSETrace 写入单条追踪记录到 SSE 响应
func writeSSETrace(w gin.ResponseWriter, trace *storage.TraceListItem) error {
	data, err := json.Marshal(trace)
	if err != nil {
		return nil // 跳过序列化失败的记录
	}

	if _, err := w.WriteString("event: trace\ndata: "); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.WriteString("\n\n")
	return err
}

// HandleMonitorList 获取追踪记录列表
// GET /admin/monitor/traces?limit=100
func (s *Server) HandleMonitorList(c *gin.Context) {
	if s.monitorService == nil {
		RespondErrorMsg(c, 503, "监控服务不可用")
		return
	}

	limit := 100
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	traces, err := s.monitorService.GetStore().List(c.Request.Context(), limit)
	if err != nil {
		RespondErrorMsg(c, 500, "获取追踪记录失败")
		return
	}

	// 收集需要查询名称的 token_id
	tokenIDs := make(map[int64]bool)
	for _, t := range traces {
		if t.TokenID > 0 {
			tokenIDs[t.TokenID] = true
		}
	}

	// 批量查询令牌名称（description）
	tokenNames, _ := s.store.FetchTokenNamesBatch(c.Request.Context(), tokenIDs)

	// 填充 AuthTokenName
	for _, t := range traces {
		if t.TokenID > 0 {
			if name, ok := tokenNames[t.TokenID]; ok {
				t.AuthTokenName = name
			}
		}
	}

	// 获取统计信息
	stats, _ := s.monitorService.GetStore().Stats(c.Request.Context())

	RespondJSON(c, 200, gin.H{
		"data":  traces,
		"stats": stats,
	})
}

// HandleMonitorStats 获取追踪统计信息
// GET /admin/monitor/stats
func (s *Server) HandleMonitorStats(c *gin.Context) {
	if s.monitorService == nil {
		RespondErrorMsg(c, 503, "监控服务不可用")
		return
	}

	stats, err := s.monitorService.GetStore().Stats(c.Request.Context())
	if err != nil {
		RespondErrorMsg(c, 500, "获取统计信息失败")
		return
	}

	RespondJSON(c, 200, stats)
}

// HandleMonitorDetail 获取追踪记录详情
// GET /admin/monitor/traces/:id
func (s *Server) HandleMonitorDetail(c *gin.Context) {
	if s.monitorService == nil {
		RespondErrorMsg(c, 503, "监控服务不可用")
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		RespondErrorMsg(c, 400, "无效的追踪记录ID")
		return
	}

	trace, err := s.monitorService.GetStore().Get(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, 404, "追踪记录不存在")
		return
	}

	// 填充令牌名称
	if trace.TokenID > 0 {
		tokenIDs := map[int64]bool{trace.TokenID: true}
		if tokenNames, err := s.store.FetchTokenNamesBatch(c.Request.Context(), tokenIDs); err == nil {
			if name, ok := tokenNames[trace.TokenID]; ok {
				trace.AuthTokenName = name
			}
		}
	}

	RespondJSON(c, 200, trace)
}

// HandleMonitorClear 清空所有追踪记录
// DELETE /admin/monitor/traces
func (s *Server) HandleMonitorClear(c *gin.Context) {
	if s.monitorService == nil {
		RespondErrorMsg(c, 503, "监控服务不可用")
		return
	}

	if err := s.monitorService.ClearAll(c.Request.Context()); err != nil {
		RespondErrorMsg(c, 500, "清空追踪记录失败")
		return
	}

	RespondJSON(c, 200, gin.H{"message": "已清空"})
}
