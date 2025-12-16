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
	URL        string `json:"url"`
	IsActive   bool   `json:"is_active"`
	LatencyMs  *int   `json:"latency_ms,omitempty"`  // 保留延迟数据
	StatusCode *int   `json:"status_code,omitempty"` // 保留状态码
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
	ID         int64  `json:"id"`
	URL        string `json:"url"`
	LatencyMs  int    `json:"latency_ms"`  // -1 表示超时或失败
	StatusCode int    `json:"status_code"` // HTTP 状态码
	TestCount  int    `json:"test_count"`  // 实际测试次数
	Error      string `json:"error,omitempty"`
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
			ChannelID:  channelID,
			URL:        ep.URL,
			IsActive:   ep.IsActive,
			LatencyMs:  ep.LatencyMs,  // 保留延迟数据
			StatusCode: ep.StatusCode, // 保留状态码
			SortOrder:  i,
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

	// 从配置读取测试次数（默认3次，最少1次，最多10次）
	testCount := s.configService.GetIntMin("endpoint_test_count", 3, 1)
	if testCount > 10 {
		testCount = 10
	}

	// 并发测速（每个端点测试 N 次取平均值）
	results := make([]EndpointTestResult, len(endpoints))
	testResults := make(map[int64]model.EndpointTestResult)
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

			info, _ := s.testEndpointLatencyMulti(endpoint.URL, testCount)
			result.LatencyMs = info.LatencyMs
			result.StatusCode = info.StatusCode
			result.TestCount = info.TestCount

			if info.LatencyMs < 0 {
				result.Error = "连接失败"
			}

			// 无论成功失败都保存结果（失败时 latency=-1, status_code=0）
			mu.Lock()
			testResults[endpoint.ID] = model.EndpointTestResult{
				LatencyMs:  info.LatencyMs,
				StatusCode: info.StatusCode,
			}
			mu.Unlock()

			results[idx] = result
		}(i, ep)
	}

	wg.Wait()

	// 保存测速结果
	if len(testResults) > 0 {
		_ = s.store.UpdateEndpointsLatency(c.Request.Context(), testResults)
	}

	// 如果开启了自动选择，选择最快的端点
	autoSelect, _ := s.store.GetChannelAutoSelectEndpoint(c.Request.Context(), channelID)
	if autoSelect && len(testResults) > 0 {
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

// endpointTestInfo 端点测试详细结果
type endpointTestInfo struct {
	LatencyMs  int // 平均延迟（毫秒）
	StatusCode int // 最后一次响应状态码
	TestCount  int // 实际测试次数
}

// testEndpointLatencyMulti 测试端点延迟（多次测试取平均值）
func (s *Server) testEndpointLatencyMulti(url string, testCount int) (endpointTestInfo, error) {
	if testCount < 1 {
		testCount = 3 // 默认测试3次
	}

	var totalLatency int64
	var successCount int
	var lastStatusCode int

	for i := 0; i < testCount; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			// HEAD 可能不支持，尝试 GET
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				cancel()
				continue
			}
		}

		start := time.Now()
		resp, err := s.client.Do(req)
		latency := time.Since(start).Milliseconds()
		cancel()

		if err != nil {
			continue
		}
		resp.Body.Close()

		lastStatusCode = resp.StatusCode
		totalLatency += latency
		successCount++

		// 测试间隔 100ms，避免过快请求
		if i < testCount-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	if successCount == 0 {
		return endpointTestInfo{LatencyMs: -1, StatusCode: 0, TestCount: 0}, nil
	}

	avgLatency := int(totalLatency / int64(successCount))
	return endpointTestInfo{
		LatencyMs:  avgLatency,
		StatusCode: lastStatusCode,
		TestCount:  successCount,
	}, nil
}

// HandleEndpointsStatus 获取端点测速状态（用于前端倒计时）
// GET /admin/endpoints/status
func (s *Server) HandleEndpointsStatus(c *gin.Context) {
	nextRunTime, intervalSeconds, enabled := s.endpointTester.GetStatus()

	if !enabled {
		c.JSON(http.StatusOK, gin.H{
			"enabled": false,
			"message": "自动端点测速已禁用",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled":          true,
		"next_run_time":    nextRunTime.Format(time.RFC3339),
		"interval_seconds": intervalSeconds,
	})
}
