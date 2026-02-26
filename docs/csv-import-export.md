# CSV 导入导出格式文档

## 概述

CSV 导入导出用于批量管理渠道配置。导出的 CSV 可以直接再导入，格式完全对称。

---

## CSV 列定义

| 列名 | 必填 | 说明 |
|------|------|------|
| `name` | ✅ | 渠道名称（唯一，重复名称会更新已有渠道） |
| `api_key` | ✅ | API Key 或 OAuth 认证 JSON（见下方详细说明） |
| `url` | 普通渠道必填 | API 端点 URL。OAuth 预设可省略（自动填充） |
| `models` | 普通渠道必填 | 模型列表，逗号分隔。OAuth 预设可省略（自动填充） |
| `preset` | OAuth 预设必填 | 预设类型：`codex` / `gemini` / `kiro` / `official` / `antigravity` / 留空 |
| `channel_type` | 可选 | 渠道类型：`anthropic` / `codex` / `gemini`。OAuth 预设可省略（自动推导） |
| `priority` | 可选 | 优先级，整数，默认 `0` |
| `model_redirects` | 可选 | 模型重定向，JSON 格式，如 `{"gpt-4":"gpt-4o"}` |
| `key_strategy` | 可选 | Key 使用策略：`sequential`（默认）/ `round_robin` |
| `enabled` | 可选 | 启用状态：`true`（默认）/ `false` |
| `quota_config` | 可选 | 用量监控配置，JSON 格式（见下方说明）。Codex 预设自动生成 |
| `id` | 可选（仅导出） | 渠道 ID，导入时忽略 |

---

## 预设类型与自动填充

### `preset=codex`（Codex 官方 OAuth）

| 字段 | 自动填充值 |
|------|-----------|
| `channel_type` | `codex` |
| `url` | `https://chatgpt.com/backend-api/codex` |
| `models` | `gpt-5.1,gpt-5,gpt-5.1-codex,gpt-5.1-codex-max,gpt-5.2` |
| `quota_config` | Codex 官方用量监控模板（自动从 access_token 提取认证信息） |

数据库映射：`channel_type="codex"`, `preset="official"`

### `preset=gemini`（Gemini 官方 OAuth）

| 字段 | 自动填充值 |
|------|-----------|
| `channel_type` | `gemini` |
| `url` | `https://generativelanguage.googleapis.com` |
| `models` | `gemini-2.5-pro,gemini-2.5-flash` |

数据库映射：`channel_type="gemini"`, `preset="official"`

### `preset=kiro`（Kiro / CodeWhisperer OAuth）

| 字段 | 自动填充值 |
|------|-----------|
| `channel_type` | `anthropic` |
| `url` | `https://codewhisperer.us-east-1.amazonaws.com` |
| `models` | `claude-opus-4-6,claude-sonnet-4-6,claude-sonnet-4-20250514,claude-3-5-sonnet-20241022,claude-3-5-haiku-20241022` |

数据库映射：`channel_type="anthropic"`, `preset="kiro"`

### 无预设（普通渠道）

`preset` 留空或不提供该列。`url` 和 `models` 为必填。`api_key` 列填写普通 API Key（多个用逗号分隔）。

---

## api_key 列格式

### 普通渠道

直接填写 API Key 字符串。多个 Key 用逗号分隔：

```
sk-key1,sk-key2,sk-key3
```

### OAuth 预设渠道（codex / gemini / kiro）

填写 JSON 对象，包含认证信息。字段名同时支持 camelCase 和 snake_case。

#### Codex OAuth JSON 字段

| 字段 | 必填 | 说明 |
|------|------|------|
| `refresh_token` / `refreshToken` | ✅（二选一） | 刷新令牌（`rt_` 开头） |
| `access_token` / `accessToken` | 推荐 | JWT 访问令牌（用于 API 请求和用量查询） |
| `id_token` / `idToken` | 推荐 | OpenAI id_token JWT |
| `token_expires_at` / `tokenExpiresAt` | 可选 | 过期时间（Unix 秒时间戳） |
| `expired` / `expires` | 可选 | 过期时间（ISO 8601 格式，如 `2026-03-07T19:52:09+08:00`） |

> 过期时间优先级：`tokenExpiresAt`（Unix 时间戳）> `expired`/`expires`（ISO 格式）
>
> `refresh_token` 和 `access_token` 至少提供一个。有 refresh_token 时服务会自动刷新。
>
> JSON 中的多余字段（如 `type`、`email`）会被自动忽略，不影响导入。

#### Gemini OAuth JSON 字段

| 字段 | 必填 | 说明 |
|------|------|------|
| `refresh_token` / `refreshToken` | ✅（二选一） | Google OAuth 刷新令牌 |
| `access_token` / `accessToken` | 推荐 | Google OAuth 访问令牌 |
| `id_token` / `idToken` | 推荐 | Google id_token JWT |
| `token_expires_at` / `tokenExpiresAt` | 可选 | 过期时间（Unix 秒时间戳） |
| `expired` / `expires` | 可选 | 过期时间（ISO 8601 格式） |

#### Kiro OAuth JSON 字段

| 字段 | 必填 | 说明 |
|------|------|------|
| `refresh_token` / `refreshToken` | ✅ | Kiro 刷新令牌 |
| `access_token` / `accessToken` | 可选 | Kiro 访问令牌 |
| `device_fingerprint` / `deviceFingerprint` | 可选 | 设备指纹 JSON（不提供则自动生成） |
| `token_expires_at` / `tokenExpiresAt` | 可选 | 过期时间 |
| `clientId` / `client_id` | 可选 | IdC 方式的 Client ID |
| `clientSecret` / `client_secret` | 可选 | IdC 方式的 Client Secret |

---

## CSV 示例

### Codex 批量导入（最简）

只需 3 列：

```csv
name,api_key,preset
codex-001,"{""refresh_token"":""rt_xxx"",""access_token"":""eyJ..."",""id_token"":""eyJ..."",""expired"":""2026-03-07T19:52:09+08:00""}",codex
codex-002,"{""refresh_token"":""rt_yyy"",""access_token"":""eyJ..."",""id_token"":""eyJ..."",""expired"":""2026-03-10T12:00:00+08:00""}",codex
```

### Codex 批量导入（自定义模型和优先级）

```csv
name,api_key,preset,models,priority
codex-001,"{""refresh_token"":""rt_xxx"",""access_token"":""eyJ..."",""id_token"":""eyJ...""}",codex,"gpt-5.1,gpt-5",10
codex-002,"{""refresh_token"":""rt_yyy"",""access_token"":""eyJ..."",""id_token"":""eyJ...""}",codex,"gpt-5.1,gpt-5",5
```

### Gemini 批量导入

```csv
name,api_key,preset
gemini-001,"{""refresh_token"":""1//xxx"",""access_token"":""ya29..."",""id_token"":""eyJ...""}",gemini
```

### Kiro 批量导入

```csv
name,api_key,preset
kiro-001,"{""refreshToken"":""xxx"",""accessToken"":""eyJ...""}",kiro
```

### 普通渠道导入

```csv
name,api_key,url,models,channel_type
my-claude,sk-ant-xxx,https://api.anthropic.com,"claude-sonnet-4-20250514,claude-opus-4-20250514",anthropic
my-proxy,sk-xxx,https://my-proxy.com/v1,"gpt-4o,claude-sonnet-4-20250514",anthropic
```

### 混合导入（OAuth + 普通）

```csv
name,api_key,url,models,preset,channel_type
codex-001,"{""refresh_token"":""rt_xxx"",""access_token"":""eyJ..."",""id_token"":""eyJ...""}",,,codex,
my-claude,sk-ant-xxx,https://api.anthropic.com,"claude-sonnet-4-20250514",,anthropic
```

---

## 你的 JSON 转 CSV 转换指南

你的原始 JSON 格式：

```json
{
  "type": "codex",
  "email": "xxx@duckmail.sbs",
  "expired": "2026-03-07T19:52:09+08:00",
  "id_token": "eyJ...",
  "access_token": "eyJ...|eyJ...|rt_xxx|1772979437|"
}
```

### access_token 字段拆分

你的 `access_token` 是 `|` 分隔的复合字段，结构为：

```
{access_token}|{id_token}|{refresh_token}|{expires_at_unix}|
```

转换逻辑：

```python
parts = raw_access_token.split("|")
access_token = parts[0]   # JWT access token
id_token     = parts[1]   # JWT id token（如果 JSON 顶层已有 id_token 可用顶层的）
refresh_token = parts[2]  # rt_ 开头的刷新令牌
expires_at   = parts[3]   # Unix 时间戳（可选，也可用顶层 expired 字段）
```

### 转换为 CSV 行

```python
import csv, json

# 读取你的 JSON 文件列表
accounts = [...]  # 你的 JSON 对象列表

with open("import.csv", "w", newline="", encoding="utf-8") as f:
    writer = csv.writer(f)
    writer.writerow(["name", "api_key", "preset"])

    for i, acc in enumerate(accounts):
        parts = acc["access_token"].split("|")

        api_key_json = json.dumps({
            "refresh_token": parts[2],
            "access_token": parts[0],
            "id_token": acc.get("id_token") or parts[1],
            "expired": acc["expired"]
        })

        writer.writerow([
            f"codex-{i+1:03d}",  # 渠道名称
            api_key_json,
            "codex"
        ])
```

生成的 CSV 直接通过管理后台的「导入 CSV」上传即可。

---

## 注意事项

1. CSV 编码必须为 UTF-8（导出自带 BOM，兼容 Excel）
2. JSON 字段在 CSV 中需要双引号转义：`""` 代替 `"`
3. 渠道名称唯一，重复名称会更新已有渠道而非新建
4. OAuth 预设的 `quota_config`：Codex 自动生成，Kiro/Gemini 需手动配置或通过 `quota_config` 列提供
5. Token 刷新：只要有 `refresh_token`，服务会在 token 过期前自动刷新
