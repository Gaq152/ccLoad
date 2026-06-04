package app

import "testing"

// TestCompareSemver 锁定版本比较核心规则：major.minor.patch 优先，
// 主体相同时正式版(无后缀) > 预发布版，两个预发布按后缀首段数字比较。
// 这些 case 对应历史 bug：GitHub /tags 按字典序返回导致 beta.10 排在 beta.3 前、
// 以及 beta.12 检查更新误报"已是最新"。
func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// 主版本号比较
		{"v1.7.0", "v1.6.9", 1},
		{"v1.6.9", "v1.7.0", -1},
		{"v2.0.0", "v1.9.9", 1},
		// minor / patch
		{"v1.6.5", "v1.6.4", 1},
		{"v1.6.4", "v1.6.5", -1},
		{"v1.7.0", "v1.6.0", 1},
		// 相等
		{"v1.6.5", "v1.6.5", 0},
		{"v1.6.5", "1.6.5", 0}, // 有无 v 前缀等价
		// 正式版 > 同号预发布（修复"beta.12 误报最新"）
		{"v1.6.5", "v1.6.5-beta.12", 1},
		{"v1.6.5-beta.12", "v1.6.5", -1},
		// 预发布之间按数字比较（修复"beta.10 排在 beta.3 前"的字典序问题）
		{"v1.6.5-beta.10", "v1.6.5-beta.3", 1},
		{"v1.6.5-beta.3", "v1.6.5-beta.10", -1},
		{"v1.6.5-beta.4", "v1.6.5-beta.12", -1},
		{"v1.6.5-beta.12", "v1.6.5-beta.4", 1},
		{"v1.6.5-beta.4", "v1.6.5-beta.4", 0},
		// 跨主体的预发布：先比主体，主体大的赢，不看后缀
		{"v1.6.5-beta.1", "v1.6.4", 1},      // 1.6.5-beta > 1.6.4 正式版
		{"v1.6.4", "v1.6.5-beta.1", -1},
		{"v1.6.6-beta.1", "v1.6.5", 1},      // 1.6.6-beta > 1.6.5 正式版
		// 综合链：1.6.5 > 1.6.5-beta.12 > 1.6.5-beta.4 > 1.6.4
		{"v1.6.5", "v1.6.5-beta.4", 1},
		{"v1.6.5-beta.12", "v1.6.4", 1},
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCompareSemverChain 验证一组版本排序后的相对次序符合预期（端到端语义）。
func TestCompareSemverChain(t *testing.T) {
	// 从小到大
	ordered := []string{
		"v1.6.4",
		"v1.6.5-beta.4",
		"v1.6.5-beta.10",
		"v1.6.5-beta.12",
		"v1.6.5",
		"v1.6.6-beta.1",
		"v1.6.6",
		"v1.7.0",
	}
	for i := range ordered {
		for j := range ordered {
			got := compareSemver(ordered[i], ordered[j])
			var want int
			switch {
			case i > j:
				want = 1
			case i < j:
				want = -1
			default:
				want = 0
			}
			if got != want {
				t.Errorf("compareSemver(%q, %q) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in       string
		wantNums [3]int
		wantPre  string
	}{
		{"v1.6.5", [3]int{1, 6, 5}, ""},
		{"1.6.5", [3]int{1, 6, 5}, ""},
		{"V1.6.5", [3]int{1, 6, 5}, ""},
		{"v1.6.5-beta.12", [3]int{1, 6, 5}, "beta.12"},
		{"v2.0.0-rc.1", [3]int{2, 0, 0}, "rc.1"},
		{"v1.6", [3]int{1, 6, 0}, ""}, // 缺省补 0
	}
	for _, c := range cases {
		nums, pre := parseSemver(c.in)
		if nums != c.wantNums || pre != c.wantPre {
			t.Errorf("parseSemver(%q) = %v,%q; want %v,%q", c.in, nums, pre, c.wantNums, c.wantPre)
		}
	}
}

func TestPreNum(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"beta.12", 12},
		{"beta.3", 3},
		{"rc.1", 1},
		{"beta", 0},  // 无数字
		{"", 0},      // 空
		{"alpha.0", 0},
	}
	for _, c := range cases {
		if got := preNum(c.in); got != c.want {
			t.Errorf("preNum(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
