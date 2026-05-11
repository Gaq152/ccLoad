package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
func (s *Server) HandleCheckUpdate(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/Gaq152/ccLoad/tags?per_page=1", nil)
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

	latest := tags[0]
	RespondJSON(c, http.StatusOK, gin.H{
		"latest_version": latest.Name,
		"release_url":    fmt.Sprintf("https://github.com/Gaq152/ccLoad/releases/tag/%s", latest.Name),
	})
}
