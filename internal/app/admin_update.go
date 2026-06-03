package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

	// 按 semver 选出最新正式版（不依赖 GitHub /tags 返回顺序——它实际按字典序，
	// 会让 beta.10 排在 beta.3 之前，靠 tags[0] 或"第一个正式版"取最新并不可靠）。
	// 正式版不含 "-"，预发布以 "-" 分隔（如 v1.6.5-beta.4）。
	var latest string
	for _, t := range tags {
		if strings.Contains(t.Name, "-") {
			continue // 跳过预发布
		}
		if latest == "" || compareSemver(t.Name, latest) > 0 {
			latest = t.Name
		}
	}
	// 兜底：若全是预发布版（未找到正式版），取 semver 最大的预发布，避免功能不可用
	if latest == "" {
		for _, t := range tags {
			if latest == "" || compareSemver(t.Name, latest) > 0 {
				latest = t.Name
			}
		}
	}

	RespondJSON(c, http.StatusOK, gin.H{
		"latest_version": latest,
		"release_url":    fmt.Sprintf("https://github.com/Gaq152/ccLoad/releases/tag/%s", latest),
	})
}

// compareSemver 比较两个语义化版本：1 表示 a>b，-1 表示 a<b，0 相等（与前端 compareVersion 规则一致）。
// 先比 major.minor.patch；主体相同时，正式版(无后缀) > 预发布版；两个预发布按后缀数字比(beta.4 < beta.10)。
func compareSemver(a, b string) int {
	na, prea := parseSemver(a)
	nb, preb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if na[i] > nb[i] {
			return 1
		}
		if na[i] < nb[i] {
			return -1
		}
	}
	if prea == preb {
		return 0
	}
	if prea == "" { // a 正式版 > b 预发布
		return 1
	}
	if preb == "" { // b 正式版 > a 预发布
		return -1
	}
	pa, pb := preNum(prea), preNum(preb)
	if pa > pb {
		return 1
	}
	if pa < pb {
		return -1
	}
	return 0
}

// parseSemver 解析 "v1.6.5-beta.12" → ([1,6,5], "beta.12")
func parseSemver(v string) ([3]int, string) {
	s := strings.TrimPrefix(v, "v")
	s = strings.TrimPrefix(s, "V")
	var pre string
	if i := strings.Index(s, "-"); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	var nums [3]int
	for i, p := range strings.Split(s, ".") {
		if i >= 3 {
			break
		}
		n, _ := strconv.Atoi(p)
		nums[i] = n
	}
	return nums, pre
}

// preNum 提取预发布后缀里的第一段数字，如 "beta.12" → 12，无数字返回 0
func preNum(pre string) int {
	digits := ""
	for _, r := range pre {
		if r >= '0' && r <= '9' {
			digits += string(r)
		} else if digits != "" {
			break
		}
	}
	if digits == "" {
		return 0
	}
	n, _ := strconv.Atoi(digits)
	return n
}
