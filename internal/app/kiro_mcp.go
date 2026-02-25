package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// ============================================================================
// Kiro MCP Web Search 路由
// 将包含 web_search 工具的请求路由到 Amazon Q MCP 端点
// 参考: https://codeberg.org/HenryXiaoYang/kirocli2api
// ============================================================================

const (
	// Amazon Q MCP 端点
	KiroMCPEndpoint = "https://q.us-east-1.amazonaws.com/mcp"
)

// MCP JSON-RPC 2.0 数据结构

type mcpRequest struct {
	ID      string    `json:"id"`
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  mcpParams `json:"params"`
}

type mcpParams struct {
	Name      string       `json:"name"`
	Arguments mcpArguments `json:"arguments"`
}

type mcpArguments struct {
	Query string `json:"query"`
}

type mcpResponse struct {
	ID      string     `json:"id"`
	JSONRPC string     `json:"jsonrpc"`
	Result  *mcpResult `json:"result,omitempty"`
	Error   *mcpError  `json:"error,omitempty"`
}

type mcpResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type webSearchResults struct {
	Results      []webSearchResult `json:"results"`
	TotalResults int               `json:"totalResults,omitempty"`
	Query        string            `json:"query,omitempty"`
}

type webSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet,omitempty"`
	Domain      string `json:"domain,omitempty"`
	PublishedAt int64  `json:"published_date,omitempty"`
}

// hasWebSearchTool 检测请求体中是否包含 web_search 工具定义
func hasWebSearchTool(body []byte) bool {
	// 快速路径：字符串检测
	if !bytes.Contains(body, []byte("web_search")) {
		return false
	}

	var req struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := sonic.Unmarshal(body, &req); err != nil {
		return false
	}
	for _, tool := range req.Tools {
		if tool.Name == "web_search" {
			return true
		}
	}
	return false
}

// extractWebSearchQuery 从请求体中提取搜索查询
// Claude Code 发送 web_search 请求时，查询内容在第一条消息中
func extractWebSearchQuery(body []byte) string {
	var req struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := sonic.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
		return ""
	}

	// 提取第一条消息的文本内容
	firstMsg := req.Messages[0]
	text := extractTextFromContent(firstMsg.Content)

	// 移除常见的搜索前缀
	prefix := "Perform a web search for the query: "
	if strings.HasPrefix(text, prefix) {
		return strings.TrimSpace(text[len(prefix):])
	}
	return text
}

// extractTextFromContent 从消息 content 中提取文本
func extractTextFromContent(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		for _, item := range c {
			if block, ok := item.(map[string]any); ok {
				if blockType, _ := block["type"].(string); blockType == "text" {
					if text, ok := block["text"].(string); ok {
						return text
					}
				}
			}
		}
	}
	return ""
}

// extractWebSearchMaxUses 从工具定义中提取 max_uses 参数
func extractWebSearchMaxUses(body []byte) int {
	var req struct {
		Tools []struct {
			Name    string `json:"name"`
			MaxUses int    `json:"max_uses"`
		} `json:"tools"`
	}
	if err := sonic.Unmarshal(body, &req); err != nil {
		return 5
	}
	for _, tool := range req.Tools {
		if tool.Name == "web_search" && tool.MaxUses > 0 {
			return tool.MaxUses
		}
	}
	return 5 // 默认值
}

// handleKiroWebSearch 处理 Kiro 预设的 web_search 请求
// 检测请求中的 web_search 工具 → 路由到 MCP 端点 → 构造 Anthropic SSE 响应
// 返回: (handled, error) - handled=true 表示已处理，不走正常转发流程
func (s *Server) handleKiroWebSearch(ctx context.Context, w http.ResponseWriter, reqCtx *proxyRequestContext) (bool, error) {
	query := extractWebSearchQuery(reqCtx.body)
	if query == "" {
		log.Printf("[WARN] [Kiro MCP] 无法提取搜索查询，跳过 MCP 路由")
		return false, nil
	}

	maxUses := extractWebSearchMaxUses(reqCtx.body)

	log.Printf("[INFO] [Kiro MCP] Web Search 请求: query=%q, maxUses=%d", query, maxUses)

	// 发送 MCP 请求
	searchResults, err := s.sendMCPRequest(ctx, query, reqCtx.kiroAccessToken)
	if err != nil {
		log.Printf("[ERROR] [Kiro MCP] MCP 请求失败: %v", err)
		return false, fmt.Errorf("mcp request failed: %w", err)
	}

	// 构造 SSE 响应
	buildWebSearchSSEResponse(w, searchResults, query, reqCtx.originalModel, maxUses)

	return true, nil
}

// sendMCPRequest 发送 MCP 请求到 Amazon Q
func (s *Server) sendMCPRequest(ctx context.Context, query string, bearerToken string) (*webSearchResults, error) {
	// 构造 MCP 请求
	mcpReq := mcpRequest{
		ID:      fmt.Sprintf("web_search_tooluse_%s_%d_%s", uuid.NewString()[:22], time.Now().UnixMilli(), uuid.NewString()[:8]),
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: mcpParams{
			Name:      "web_search",
			Arguments: mcpArguments{Query: query},
		},
	}

	jsonBytes, err := sonic.Marshal(mcpReq)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp request: %w", err)
	}

	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "POST", KiroMCPEndpoint, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("create mcp request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	// 使用 Kiro 专用客户端（utls 指纹伪装）
	resp, err := s.kiroClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send mcp request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read mcp response: %w", err)
	}

	// 解析响应
	var mcpResp mcpResponse
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp returned status %d: %s", resp.StatusCode, string(body))
	}
	if err := sonic.Unmarshal(body, &mcpResp); err != nil {
		return nil, fmt.Errorf("unmarshal mcp response: %w", err)
	}
	if mcpResp.Error != nil {
		return nil, fmt.Errorf("mcp error: code=%d, message=%s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	// 解析搜索结果
	if mcpResp.Result != nil && len(mcpResp.Result.Content) > 0 {
		for _, content := range mcpResp.Result.Content {
			if content.Type == "text" {
				var results webSearchResults
				if sonic.Unmarshal([]byte(content.Text), &results) == nil {
					return &results, nil
				}
			}
		}
	}

	return &webSearchResults{}, nil
}

// buildWebSearchSSEResponse 将 MCP 搜索结果构造为 Anthropic SSE 事件流
func buildWebSearchSSEResponse(w http.ResponseWriter, results *webSearchResults, query string, model string, maxUses int) {
	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[ERROR] [Kiro MCP] ResponseWriter 不支持 Flusher")
		return
	}

	toolUseID := "srvtoolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:32]
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]

	// 估算输入 token
	inputTokens := len(query) / 4

	// 1. message_start
	writeSSEEvent(w, flusher, "message_start", mustMarshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":  inputTokens,
				"output_tokens": 0,
			},
		},
	}))

	// 2. content_block_start (server_tool_use)
	writeSSEEvent(w, flusher, "content_block_start", mustMarshal(map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"id":    toolUseID,
			"type":  "server_tool_use",
			"name":  "web_search",
			"input": map[string]any{},
		},
	}))

	// 3. content_block_delta (input_json_delta)
	inputJSON := mustMarshal(map[string]string{"query": query})
	writeSSEEvent(w, flusher, "content_block_delta", mustMarshal(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	}))

	// 4. content_block_stop (tool_use)
	writeSSEEvent(w, flusher, "content_block_stop", mustMarshal(map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	}))

	// 5. content_block_start (web_search_tool_result)
	searchContent := buildSearchResultContent(results, maxUses)
	writeSSEEvent(w, flusher, "content_block_start", mustMarshal(map[string]any{
		"type":  "content_block_start",
		"index": 1,
		"content_block": map[string]any{
			"type":        "web_search_tool_result",
			"tool_use_id": toolUseID,
			"content":     searchContent,
		},
	}))

	// 6. content_block_stop (tool_result)
	writeSSEEvent(w, flusher, "content_block_stop", mustMarshal(map[string]any{
		"type":  "content_block_stop",
		"index": 1,
	}))

	// 7. content_block_start (text)
	writeSSEEvent(w, flusher, "content_block_start", mustMarshal(map[string]any{
		"type":  "content_block_start",
		"index": 2,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}))

	// 8. content_block_delta (text_delta) - 搜索结果摘要
	summary := buildSearchSummary(results, query, maxUses)
	writeSSEEvent(w, flusher, "content_block_delta", mustMarshal(map[string]any{
		"type":  "content_block_delta",
		"index": 2,
		"delta": map[string]any{
			"type": "text_delta",
			"text": summary,
		},
	}))

	// 9. content_block_stop (text)
	writeSSEEvent(w, flusher, "content_block_stop", mustMarshal(map[string]any{
		"type":  "content_block_stop",
		"index": 2,
	}))

	// 10. message_delta
	outputTokens := len(summary) / 4
	writeSSEEvent(w, flusher, "message_delta", mustMarshal(map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]int{"output_tokens": outputTokens},
	}))

	// 11. message_stop
	writeSSEEvent(w, flusher, "message_stop", mustMarshal(map[string]any{
		"type": "message_stop",
	}))
}

// buildSearchResultContent 构建搜索结果内容块
func buildSearchResultContent(results *webSearchResults, maxUses int) []map[string]any {
	content := make([]map[string]any, 0)
	if results == nil || len(results.Results) == 0 {
		return content
	}

	limit := len(results.Results)
	if maxUses > 0 && maxUses < limit {
		limit = maxUses
	}

	for i := 0; i < limit; i++ {
		r := results.Results[i]
		content = append(content, map[string]any{
			"type":              "web_search_result",
			"title":             r.Title,
			"url":               r.URL,
			"encrypted_content": r.Snippet,
			"page_age":          nil,
		})
	}
	return content
}

// buildSearchSummary 构建搜索结果摘要文本
func buildSearchSummary(results *webSearchResults, query string, maxUses int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Here are the search results for \"%s\":\n\n", query))

	if results == nil || len(results.Results) == 0 {
		sb.WriteString("No results found.\n")
		return sb.String()
	}

	limit := len(results.Results)
	if maxUses > 0 && maxUses < limit {
		limit = maxUses
	}

	for i := 0; i < limit; i++ {
		r := results.Results[i]
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, r.Title))
		if r.Snippet != "" {
			snippet := r.Snippet
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", snippet))
		}
		sb.WriteString(fmt.Sprintf("   Source: %s\n\n", r.URL))
	}

	return sb.String()
}

// mustMarshal JSON 序列化（忽略错误，内部使用）
func mustMarshal(v any) []byte {
	data, _ := sonic.Marshal(v)
	return data
}
