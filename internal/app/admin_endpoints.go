package app

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"ccLoad/internal/model"

	"github.com/gin-gonic/gin"
)

// ==================== 端点管理 API ====================

// EndpointRequest 端点请求结构
type EndpointRequest struct {
	URL      string `json:"url"`
	IsActive bool   `json:"is_active"`
}

// EndpointsUpdateRequest 批量更新端点请求
type EndpointsUpdateRequest struct {
	Endpoints          []EndpointRequest `json:"endpoints"`
	AutoSelectEndpoint bool              `json:"auto_select_endpoint"`
}

// SetActiveEndpointRequest 设置激活端点请求
type SetActiveEndpointRequest struct {
	EndpointID int64 `json:"endpoint_id"`
}

// EndpointTestResult 端点测速结果
type EndpointTestResult struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	LatencyMs int    `json:"latency_ms"` // -1 表示超时或失败
	Error     string `json:"error,omitempty"`
}

// HandleChannelEndpoints 处理端点的 GET/PUT 请求
func (s *Server) HandleChannelEndpoints(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		s.handleGetEndpoints(c)
	case http.MethodPut:
		s.handleUpdateEndpoints(c)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

// handleGetEndpoints 获取渠道端点列表
func (s *Server) handleGetEndpoints(c *gin.Context) {
	channelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}

	endpoints, err := s.store.ListEndpoints(c.Request.Context(), channelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 获取自动选择设置
	autoSelect, _ := s.store.GetChannelAutoSelectEndpoint(c.Request.Context(), channelID)

	c.JSON(http.StatusOK, gin.H{
		"data":                  endpoints,
		"auto_select_endpoint": autoSelect,
	})
}

// handleUpdateEndpoints 批量更新端点
func (s *Server) handleUpdateEndpoints(c *gin.Context) {
	channelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}

	var req EndpointsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 转换为模型
	endpoints := make([]model.ChannelEndpoint, len(req.Endpoints))
	hasActive := false
	for i, ep := range req.Endpoints {
		endpoints[i] = model.ChannelEndpoint{
			ChannelID: channelID,
			URL:       ep.URL,
			IsActive:  ep.IsActive,
			SortOrder: i,
		}
		if ep.IsActive {
			hasActive = true
		}
	}

	// 如果没有激活的端点，激活第一个
	if len(endpoints) > 0 && !hasActive {
		endpoints[0].IsActive = true
	}

	// 保存端点
	if err := s.store.SaveEndpoints(c.Request.Context(), channelID, endpoints); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 更新自动选择设置
	if err := s.store.SetChannelAutoSelectEndpoint(c.Request.Context(), channelID, req.AutoSelectEndpoint); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 如果有激活的端点，同步更新 channels.url
	if len(endpoints) > 0 {
		for _, ep := range endpoints {
			if ep.IsActive {
				// 重新获取保存后的端点ID
				savedEndpoints, _ := s.store.ListEndpoints(c.Request.Context(), channelID)
				for _, saved := range savedEndpoints {
					if saved.IsActive {
						s.store.SetActiveEndpoint(c.Request.Context(), channelID, saved.ID)
						break
					}
				}
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// HandleTestEndpoints 测速所有端点
func (s *Server) HandleTestEndpoints(c *gin.Context) {
	channelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}

	endpoints, err := s.store.ListEndpoints(c.Request.Context(), channelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(endpoints) == 0 {
		c.JSON(http.StatusOK, gin.H{"data": []EndpointTestResult{}})
		return
	}

	// 并发测速
	results := make([]EndpointTestResult, len(endpoints))
	latencyResults := make(map[int64]int)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, ep := range endpoints {
		wg.Add(1)
		go func(idx int, endpoint model.ChannelEndpoint) {
			defer wg.Done()

			result := EndpointTestResult{
				ID:  endpoint.ID,
				URL: endpoint.URL,
			}

			latency, err := s.testEndpointLatency(endpoint.URL)
			if err != nil {
				result.LatencyMs = -1
				result.Error = err.Error()
			} else {
				result.LatencyMs = latency
				mu.Lock()
				latencyResults[endpoint.ID] = latency
				mu.Unlock()
			}

			results[idx] = result
		}(i, ep)
	}

	wg.Wait()

	// 保存测速结果
	if len(latencyResults) > 0 {
		_ = s.store.UpdateEndpointsLatency(c.Request.Context(), latencyResults)
	}

	// 如果开启了自动选择，选择最快的端点
	autoSelect, _ := s.store.GetChannelAutoSelectEndpoint(c.Request.Context(), channelID)
	if autoSelect && len(latencyResults) > 0 {
		_ = s.store.SelectFastestEndpoint(c.Request.Context(), channelID)
	}

	// 重新获取更新后的端点列表
	updatedEndpoints, _ := s.store.ListEndpoints(c.Request.Context(), channelID)

	c.JSON(http.StatusOK, gin.H{
		"data":      results,
		"endpoints": updatedEndpoints,
	})
}

// HandleSetActiveEndpoint 设置激活的端点
func (s *Server) HandleSetActiveEndpoint(c *gin.Context) {
	channelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}

	var req SetActiveEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.store.SetActiveEndpoint(c.Request.Context(), channelID, req.EndpointID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// testEndpointLatency 测试端点延迟（只关注延迟，不关心响应状态码）
func (s *Server) testEndpointLatency(url string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		// HEAD 可能不支持，尝试 GET
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
	}

	start := time.Now()
	resp, err := s.client.Do(req)
	latency := int(time.Since(start).Milliseconds())

	if err != nil {
		return 0, err
	}
	resp.Body.Close()

	// 只关注延迟，不关心响应状态码
	return latency, nil
}
