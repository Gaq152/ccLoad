package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type githubTag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// HandleCheckUpdate 检查 GitHub 最新版本
// GET /admin/check-update
// 使用 /tags 接口而非 /releases/latest，因为仓库未创建 Release 时 /releases/latest 返回 404
// [FIX] 过滤预发布版本(含 -beta/-rc/-alpha 等后缀的 tag)，只把正式版当作可更新目标，
// 避免 1.6.3 等正式版用户检查更新时看到 beta 版本提示。
func (s *Server) HandleCheckUpdate(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// 拉取多个 tag（GitHub /tags 实践中按引用新→旧返回），用于过滤预发布版后取最新正式版
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/Gaq152/ccLoad/tags?per_page=30", nil)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "构建请求失败")
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadGateway, fmt.Sprintf("请求 GitHub 失败: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		RespondErrorMsg(c, http.StatusBadGateway,
			fmt.Sprintf("GitHub API 返回 %d", resp.StatusCode))
		return
	}

	var tags []githubTag
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "解析响应失败")
		return
	}

	if len(tags) == 0 {
		RespondErrorMsg(c, http.StatusNotFound, "仓库暂无版本标签")
		return
	}

	// 过滤预发布版本：语义化版本中预发布以 "-" 分隔（如 v1.6.4-beta.4），正式版不含 "-"
	var latest string
	for _, t := range tags {
		if !strings.Contains(t.Name, "-") {
			latest = t.Name
			break
		}
	}
	// 兜底：若拉取的 tag 全是预发布版（未找到正式版），返回最新的一个，避免功能不可用
	if latest == "" {
		latest = tags[0].Name
	}

	RespondJSON(c, http.StatusOK, gin.H{
		"latest_version": latest,
		"release_url":    fmt.Sprintf("https://github.com/Gaq152/ccLoad/releases/tag/%s", latest),
	})
}
