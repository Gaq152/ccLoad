package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// HandleCheckUpdate 检查 GitHub 最新版本
// GET /admin/check-update
func (s *Server) HandleCheckUpdate(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/Gaq152/ccLoad/releases/latest", nil)
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

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "解析响应失败")
		return
	}

	RespondJSON(c, http.StatusOK, gin.H{
		"latest_version": release.TagName,
		"release_url":    release.HTMLURL,
	})
}
