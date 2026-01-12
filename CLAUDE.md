# CLAUDE.md

## 构建与测试

```bash
# 构建(必须 -tags go_json)
go build -tags go_json -o ccload .

# 测试(必须 -tags go_json)
go test -tags go_json ./internal/... -v
go test -tags go_json -race ./internal/...  # 竞态检测

# 运行
export CCLOAD_PASS=test123  # 必填
go run -tags go_json .
```

## 核心架构

```
internal/
├── app/           # HTTP层+业务逻辑
│   ├── proxy_*.go      # 代理转发 (handler/forward/error/stream/util)
│   ├── proxy_kiro.go   # Kiro (CodeWhisperer) 请求转发与响应处理
│   ├── proxy_codex.go  # Codex OAuth 认证与请求转换
│   ├── proxy_gemini.go # Gemini OAuth 认证与请求转换
│   ├── kiro_auth.go    # Kiro OAuth Token 管理与刷新
│   ├── kiro_transform.go # Anthropic → CodeWhisperer 请求转换
│   ├── kiro_types.go   # Kiro 类型定义与模型映射
│   ├── token_counter.go    # Token 计数接口（三层降级）
│   ├── tiktoken_counter.go # tiktoken 本地计算
│   ├── admin_*.go      # 管理API (channels/tokens/settings/testing等)
│   ├── selector.go     # 渠道选择器
│   ├── key_selector.go # Key负载均衡
│   └── auth_service.go # API令牌认证服务
├── cooldown/      # 冷却决策引擎 (manager.go)
├── storage/
│   ├── schema/    # 表结构定义 (tables.go, builder.go)
│   ├── sql/       # 统一SQL实现 (SQLite/MySQL共享)
│   └── redis/     # Redis同步备份
├── validator/     # 渠道验证器
├── testutil/      # 渠道测试工具 (api_tester.go)
└── util/          # 工具库 (classifier.go, models_fetcher.go)

web/
├── assets/js/     # 前端JS (channels.js, tokens.js, logs.js等)
├── assets/css/    # 样式 (styles.css 含暗色/亮色主题)
└── *.html         # 页面模板
```

## 核心功能模块

### 官方预设 OAuth 认证
- **Kiro**: `kiro_auth.go` - Social/IdC 双认证、Token 刷新、设备指纹管理
- **Codex**: `proxy_codex.go` - OAuth Token 解析、刷新、请求头注入
- **Gemini**: `proxy_gemini.go` - OAuth Token 解析、刷新、CLI格式转换
- **Token刷新**: `RefreshKiroTokenIfNeeded()` / `RefreshCodexTokenIfNeeded()` / `RefreshGeminiTokenIfNeeded()`
- **数据库字段**: `api_keys.access_token`, `refresh_token`, `token_expires_at`, `device_fingerprint`

### Kiro (CodeWhisperer) 预设
- **请求转换**: `kiro_transform.go:TransformToKiroRequest()` - Anthropic → CodeWhisperer 格式
- **响应处理**: `proxy_kiro.go:ProcessKiroAWSEventStream()` - AWS EventStream → Anthropic SSE
- **错误映射**: `IsKiroContentLengthExceeds()` → `max_tokens` stop_reason
- **Thinking 智能调整**: 确保 `max_tokens > budget_tokens`，不足时自动 +4096
- **Quota 同步**: Token 刷新时自动更新 `quota_config.Authorization`

### Token 计数（三层降级）
- **接口**: `POST /v1/messages/count_tokens`
- **第一层**: 带 `beta` 参数时转发上游 API（100% 准确）
- **第二层**: tiktoken 本地计算（~5% 误差）
- **第三层**: 纯算法快速估算（~15% 误差）
- **监控**: 所有请求记录到监控，显示计算来源

### 多端点管理
- **表**: `channel_endpoints` (channel_id, url, latency, status_code, is_active)
- **自动测速**: `admin_endpoints.go` - 后台定时测速服务
- **端点选择**: 优先选择延迟最低且状态正常的端点

### SSE 实时推送
- **日志推送**: `admin_logs.go:HandleLogsSSE()` - 实时日志流
- **冷却事件**: `admin_cooldown.go:HandleCooldownSSE()` - 冷却状态变更
- **前端处理**: `logs.js` - SSE连接管理、去重、筛选过滤

### API令牌系统
- **表**: `auth_tokens` (token, description, expires_at, is_active, all_channels)
- **渠道控制**: `token_channels` 关联表，限制令牌可访问的渠道
- **认证服务**: `auth_service.go:ValidateToken()` - 令牌验证与权限检查

## 故障切换策略

- Key级错误(401/403/429) → 重试同渠道其他Key
- 渠道级错误(5xx/520/524) → 切换到其他渠道
- 网关错误(502/503/504) → 重试同渠道，不冷却
- 客户端错误(404/405) → 不重试，直接返回
- Kiro 上下文超限 → 转换为 `max_tokens` stop_reason（触发客户端压缩）
- Kiro 账户暂停 → 24小时冷却
- 指数退避: 2min → 4min → 8min → 30min(上限)

**关键入口**:
- `cooldown.Manager.HandleError()` - 冷却决策引擎
- `util.ClassifyHTTPStatus()` - 错误分类
- `app.KeySelector.SelectAvailableKey()` - Key负载均衡

## 开发指南

### 添加 Admin API
1. `admin_types.go` - 定义请求/响应类型
2. `admin_<feature>.go` - 实现Handler
3. `server.go:SetupRoutes()` - 注册路由

### 数据库操作
- Schema定义: `storage/schema/tables.go` - DefineXxxTable()
- 迁移: `storage/migrate.go` - 启动自动执行
- 事务: `(*SQLStore).WithTransaction(ctx, func(tx) error)`
- 缓存失效: `InvalidateChannelListCache()` / `InvalidateAPIKeysCache()`

### 前端开发
- 主题: CSS变量在 `styles.css`，JS切换在各页面 `applyTheme()`
- 模板引擎: `TemplateEngine.render('tpl-xxx', data)`
- SSE: `EventSource` 连接，注意重连和去重逻辑
- SSE解析: `parseSSEResponse()` 提取实际文本内容

## 代码规范

- **必须** `-tags go_json` 构建和测试
- **必须** `any` 替代 `interface{}`
- **禁止** 过度工程，YAGNI原则
- **Fail-Fast**: 配置错误直接 `log.Fatal()` 退出
- **Context**: `defer cancel()` 必须无条件调用
- **注释**: 中文注释，解释业务规则和边界情况
