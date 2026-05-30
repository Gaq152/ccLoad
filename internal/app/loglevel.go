package app

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
)

// 日志级别系统
//
// 设计说明：
//   现有代码大量使用 log.Printf("[DEBUG] ...") / log.Printf("[INFO] ...") 这类
//   带级别前缀的调用。本系统不改动这些调用点，而是在标准库 log 的输出层拦截：
//   按行首的 [DEBUG]/[INFO]/[WARN]/[ERROR] 前缀判断级别，低于阈值的整条丢弃。
//
//   级别由环境变量 LOG_LEVEL 控制（debug/info/warn/error，默认 info）。
//   用环境变量而非数据库配置，是因为日志需在配置系统/存储初始化之前就生效
//   （迁移、连接等早期日志也要受控），此时还读不到 DB 配置。
//
// 级别从低到高：DEBUG < INFO < WARN < ERROR。
// 阈值为 info 时，DEBUG 不输出；为 debug 时全部输出。
// 无可识别前缀的日志（如 gin、第三方库、未加前缀的 log.Printf）一律放行，
// 避免误吞重要信息。

type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarn
	levelError
	// levelNone 表示行内无可识别的级别前缀（gin、第三方库、裸 log.Printf）。
	// 这类日志一律放行，不参与阈值过滤，避免误吞重要信息。
	levelNone
)

// parseLogLevel 解析级别字符串，无法识别时返回 info。
func parseLogLevel(s string) logLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return levelDebug
	case "info":
		return levelInfo
	case "warn", "warning":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

// lineLevel 根据日志行内容推断其级别。
// 标准库 log 的行格式为 "<时间> [LEVEL] ..."，级别前缀在时间戳之后，
// 因此用 Contains 而非 HasPrefix 匹配。无可识别前缀时返回 levelNone（始终放行）。
func lineLevel(line []byte) logLevel {
	switch {
	case bytes.Contains(line, []byte("[DEBUG]")):
		return levelDebug
	case bytes.Contains(line, []byte("[INFO]")):
		return levelInfo
	case bytes.Contains(line, []byte("[WARN]")):
		return levelWarn
	case bytes.Contains(line, []byte("[ERROR]")), bytes.Contains(line, []byte("[FATAL]")):
		return levelError
	default:
		// 无级别前缀（gin/第三方/裸 log.Printf）：放行，不参与过滤
		return levelNone
	}
}

// levelFilterWriter 包装底层 Writer，按行级别过滤。
type levelFilterWriter struct {
	threshold logLevel
	out       io.Writer
}

func (w *levelFilterWriter) Write(p []byte) (int, error) {
	// log.Printf 每次调用对应一次 Write（已含换行），直接整块判级别。
	if lineLevel(p) < w.threshold {
		// 丢弃：返回写入成功，避免标准库 log 认为出错。
		return len(p), nil
	}
	return w.out.Write(p)
}

// SetupLogLevel 根据 LOG_LEVEL 环境变量装配日志级别过滤器。
// 在 main 最早期调用（读 .env 之后、其他初始化之前）。
func SetupLogLevel() {
	threshold := parseLogLevel(os.Getenv("LOG_LEVEL"))
	log.SetOutput(&levelFilterWriter{
		threshold: threshold,
		out:       os.Stderr, // 标准库 log 默认输出到 stderr
	})
}
