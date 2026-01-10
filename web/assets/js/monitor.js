/**
 * 请求监控页面逻辑
 */

// 监控状态
let monitorEnabled = false;
let sseEventSource = null;
let traces = []; // 缓存的追踪记录
let filteredTraces = []; // 过滤后的追踪记录
let currentFilter = 'all'; // 当前筛选：all/success/error
const MAX_TRACES = 500; // 最大缓存数量

// 统计数据
let stats = { total: 0, success: 0, error: 0 };

// 初始化
document.addEventListener('DOMContentLoaded', async function() {
  // 初始化顶部导航栏
  if (window.initTopbar) {
    initTopbar('monitor');
  }

  // 加载初始状态和数据
  await loadMonitorStatus();
  await loadTraces();

  // ESC 关闭弹窗
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      closeDetailModal();
      closeConfirmModal();
    }
  });

  // 点击弹窗外部关闭
  const detailModal = document.getElementById('traceDetailModal');
  if (detailModal) {
    detailModal.addEventListener('click', (e) => {
      if (e.target === detailModal) closeDetailModal();
    });
  }

  const confirmModal = document.getElementById('confirmModal');
  if (confirmModal) {
    confirmModal.addEventListener('click', (e) => {
      if (e.target === confirmModal) closeConfirmModal();
    });
  }
});

// 加载监控状态
async function loadMonitorStatus() {
  try {
    const data = await fetchDataWithAuth('/admin/monitor/status');
    monitorEnabled = data.enabled;
    updateStatusUI();
    if (monitorEnabled) {
      connectSSE();
    }
  } catch (e) {
    console.error('加载监控状态失败:', e);
  }
}

// 切换监控状态
async function toggleMonitor() {
  const newState = !monitorEnabled;
  try {
    await fetchAPIWithAuth('/admin/monitor/toggle', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: newState })
    });
    monitorEnabled = newState;
    updateStatusUI();

    if (newState) {
      connectSSE();
    } else {
      disconnectSSE();
    }
  } catch (e) {
    console.error('切换监控状态失败:', e);
    if (window.showError) showError('切换监控状态失败');
  }
}

// 更新状态 UI
function updateStatusUI() {
  const btn = document.getElementById('monitorBtn');
  if (btn) {
    if (monitorEnabled) {
      btn.textContent = '停止监控';
      btn.className = 'btn btn-danger btn-sm';
    } else {
      btn.textContent = '开始监控';
      btn.className = 'btn btn-primary btn-sm';
    }
  }
}

// 连接 SSE
function connectSSE() {
  if (sseEventSource) return;

  const token = localStorage.getItem('ccload_token');
  if (!token) {
    console.error('[Monitor SSE] 未登录');
    return;
  }

  sseEventSource = new EventSource(`/admin/monitor/stream?token=${encodeURIComponent(token)}`);

  sseEventSource.addEventListener('connected', () => {
    console.log('[Monitor SSE] 连接成功');
  });

  sseEventSource.addEventListener('trace', (e) => {
    try {
      const trace = JSON.parse(e.data);
      prependTrace(trace);
    } catch (err) {
      console.error('[Monitor SSE] 解析追踪数据失败:', err);
    }
  });

  sseEventSource.addEventListener('close', () => {
    console.log('[Monitor SSE] 服务器关闭连接');
    disconnectSSE();
  });

  sseEventSource.onerror = (e) => {
    console.error('[Monitor SSE] 连接错误:', e);
    disconnectSSE();
    // 5秒后重连（如果监控仍开启）
    if (monitorEnabled) {
      setTimeout(() => {
        if (monitorEnabled && !sseEventSource) {
          console.log('[Monitor SSE] 尝试重连...');
          connectSSE();
        }
      }, 5000);
    }
  };
}

// 断开 SSE
function disconnectSSE() {
  if (sseEventSource) {
    sseEventSource.close();
    sseEventSource = null;
  }
}

// 加载追踪记录列表
async function loadTraces() {
  try {
    const data = await fetchDataWithAuth('/admin/monitor/traces?limit=100');
    traces = data.data || [];
    // 更新统计
    if (data.stats) {
      stats = data.stats;
    } else {
      // 本地计算统计
      stats = calculateStats(traces);
    }
    updateStatsUI();
    applyFilter();
  } catch (e) {
    console.error('加载追踪记录失败:', e);
  }
}

// 计算统计数据（200=成功，非200=错误）
function calculateStats(traceList) {
  const result = { total: traceList.length, success: 0, error: 0 };
  for (const t of traceList) {
    if (t.status_code === 200) {
      result.success++;
    } else {
      result.error++;
    }
  }
  return result;
}

// 更新统计 UI
function updateStatsUI() {
  const totalEl = document.getElementById('statTotal');
  const successEl = document.getElementById('statSuccess');
  const errorEl = document.getElementById('statError');
  if (totalEl) totalEl.textContent = stats.total;
  if (successEl) successEl.textContent = stats.success;
  if (errorEl) errorEl.textContent = stats.error;
}

// 设置筛选条件
function setFilter(filter) {
  currentFilter = filter;
  applyFilter();
  updateFilterUI();
}

// 应用过滤（200=成功，非200=错误）
function applyFilter() {
  switch (currentFilter) {
    case 'success':
      filteredTraces = traces.filter(t => t.status_code === 200);
      break;
    case 'error':
      filteredTraces = traces.filter(t => t.status_code !== 200);
      break;
    default:
      filteredTraces = [...traces];
  }
  renderTraces();
}

// 更新筛选 UI 状态
function updateFilterUI() {
  const allCard = document.getElementById('filterAll');
  const successCard = document.getElementById('filterSuccess');
  const errorCard = document.getElementById('filterError');

  [allCard, successCard, errorCard].forEach(card => {
    if (card) card.classList.remove('active');
  });

  if (currentFilter === 'all') {
    if (allCard) allCard.classList.add('active');
  } else if (currentFilter === 'success') {
    if (successCard) successCard.classList.add('active');
  } else if (currentFilter === 'error') {
    if (errorCard) errorCard.classList.add('active');
  }
}

// 新增追踪记录（SSE 推送）
function prependTrace(trace) {
  // 去重检查
  if (traces.some(t => t.id === trace.id)) return;

  traces.unshift(trace);
  if (traces.length > MAX_TRACES) {
    traces.pop();
  }

  // 更新统计（200=成功，非200=错误）
  stats.total++;
  if (trace.status_code === 200) {
    stats.success++;
  } else {
    stats.error++;
  }
  updateStatsUI();
  applyFilter();
}

// 更新记录数量显示
function updateTraceCount(count) {
  const el = document.getElementById('traceCount');
  if (el) {
    el.textContent = `${count} 条记录`;
  }
}

// 渲染追踪记录
function renderTraces() {
  const tbody = document.getElementById('tbody');
  if (!tbody) return;

  tbody.innerHTML = '';

  const displayTraces = filteredTraces;

  if (displayTraces.length === 0) {
    let emptyMsg;
    if (currentFilter === 'success') {
      emptyMsg = '没有成功记录';
    } else if (currentFilter === 'error') {
      emptyMsg = '没有错误记录';
    } else {
      emptyMsg = monitorEnabled ? '等待请求...' : '开启监控后将实时显示请求记录';
    }
    tbody.innerHTML = `
      <tr>
        <td colspan="9" class="empty-state">${emptyMsg}</td>
      </tr>
    `;
    return;
  }

  for (const trace of displayTraces) {
    const row = document.createElement('tr');
    row.setAttribute('data-id', trace.id);

    // 状态码样式（200=成功，非200=错误）
    let statusClass = 'status-error';
    if (trace.status_code === 200) {
      statusClass = 'status-success';
    }

    // 渠道显示
    const channelDisplay = trace.channel_name
      ? `${escapeHtml(trace.channel_name)} <small class="channel-id">#${trace.channel_id}</small>`
      : `#${trace.channel_id}`;

    // 测试标记
    const testBadge = trace.is_test ? '<span class="test-badge">测试</span>' : '';

    // 端点显示（简化路径）
    const endpoint = trace.request_path || '-';

    // Token 显示
    const inputTokens = trace.input_tokens > 0 ? trace.input_tokens : '-';
    const outputTokens = trace.output_tokens > 0 ? trace.output_tokens : '-';
    const tokensDisplay = (inputTokens !== '-' || outputTokens !== '-')
      ? `<span class="tokens-in">${inputTokens}</span> / <span class="tokens-out">${outputTokens}</span>`
      : '-';

    row.innerHTML = `
      <td class="time-cell">${formatTime(trace.time)}${testBadge}</td>
      <td class="ip-cell">${escapeHtml(trace.client_ip || '-')}</td>
      <td class="endpoint-cell">${escapeHtml(endpoint)}</td>
      <td><span class="model-tag">${escapeHtml(trace.model || '-')}</span></td>
      <td class="channel-cell">${channelDisplay}</td>
      <td><span class="status-badge ${statusClass}">${trace.status_code || '-'}</span></td>
      <td class="duration-cell">${trace.duration?.toFixed(3) || '-'}s</td>
      <td class="tokens-cell">${tokensDisplay}</td>
      <td>
        <button class="btn btn-secondary btn-xs" onclick="viewDetail(${trace.id})">查看</button>
      </td>
    `;

    tbody.appendChild(row);
  }
}

// 查看详情
async function viewDetail(id) {
  try {
    const trace = await fetchDataWithAuth(`/admin/monitor/traces/${id}`);

    // 填充基本信息
    document.getElementById('detailChannel').textContent = trace.channel_name || '未知';
    document.getElementById('detailChannelID').textContent = trace.channel_id || '-';
    document.getElementById('detailModel').textContent = trace.model || '-';
    document.getElementById('detailRequestPath').textContent = trace.request_path || '-';
    document.getElementById('detailStatus').textContent = trace.status_code || '-';
    document.getElementById('detailDuration').textContent = trace.duration ? `${trace.duration.toFixed(3)}s` : '-';
    document.getElementById('detailStreaming').textContent = trace.is_streaming ? '是' : '否';
    document.getElementById('detailInputTokens').textContent = trace.input_tokens > 0 ? trace.input_tokens : '-';
    document.getElementById('detailOutputTokens').textContent = trace.output_tokens > 0 ? trace.output_tokens : '-';
    document.getElementById('detailClientIP').textContent = trace.client_ip || '-';
    document.getElementById('detailAPIKey').textContent = trace.api_key_used || '-';
    document.getElementById('detailIsTest').textContent = trace.is_test ? '是' : '否';
    document.getElementById('detailIsTest').className = trace.is_test ? 'test-indicator' : '';

    // 格式化 JSON（带语法高亮）
    document.getElementById('detailRequestBody').innerHTML = formatJSONWithHighlight(trace.request_body);
    document.getElementById('detailResponseBody').innerHTML = formatJSONWithHighlight(trace.response_body);

    // 解析并展示模型响应
    parseAndDisplayResponse(trace.response_body);

    // 重置折叠状态
    const reqContainer = document.getElementById('requestBodyContainer');
    const reqIcon = document.getElementById('requestBodyIcon');
    if (reqContainer) reqContainer.classList.add('collapsed');
    if (reqIcon) reqIcon.textContent = '▼';

    const rawContainer = document.getElementById('rawResponseContainer');
    const rawIcon = document.getElementById('rawResponseIcon');
    if (rawContainer) rawContainer.classList.add('collapsed');
    if (rawIcon) rawIcon.textContent = '▼';

    // 显示弹窗
    const modal = document.getElementById('traceDetailModal');
    if (modal) modal.classList.add('show');
  } catch (e) {
    console.error('加载详情失败:', e);
    if (window.showError) showError('加载详情失败');
  }
}

// 关闭详情弹窗
function closeDetailModal() {
  const modal = document.getElementById('traceDetailModal');
  if (modal) modal.classList.remove('show');
}

// 显示清空确认弹窗
function clearTraces() {
  const modal = document.getElementById('confirmModal');
  if (modal) modal.classList.add('show');
}

// 关闭确认弹窗
function closeConfirmModal() {
  const modal = document.getElementById('confirmModal');
  if (modal) modal.classList.remove('show');
}

// 确认清空记录
async function confirmClearTraces() {
  closeConfirmModal();

  try {
    await fetchAPIWithAuth('/admin/monitor/traces', { method: 'DELETE' });
    traces = [];
    filteredTraces = [];
    stats = { total: 0, success: 0, error: 0 };
    updateStatsUI();
    renderTraces();
    if (window.showSuccess) showSuccess('已清空');
  } catch (e) {
    console.error('清空失败:', e);
    if (window.showError) showError('清空失败');
  }
}

// 格式化 JSON
function formatJSON(str) {
  if (!str) return '-';
  try {
    const obj = JSON.parse(str);
    return JSON.stringify(obj, null, 2);
  } catch {
    return str;
  }
}

// 格式化 JSON 并添加语法高亮
function formatJSONWithHighlight(str) {
  if (!str) return '<span class="json-null">-</span>';

  // 检测是否为 AWS Event Stream 二进制格式（Kiro 响应）
  // 特征：包含 :event-type 和 :message-type 等二进制头标记
  const isAWSEventStream = str.includes(':event-type') && str.includes(':message-type');
  if (isAWSEventStream) {
    // AWS Event Stream 是二进制格式，不是 JSON，直接显示原始内容
    // 过滤掉不可打印字符，只保留可见的 JSON 片段
    const cleanStr = str.replace(/[\x00-\x1F\x7F-\x9F]/g, ' ').replace(/\s+/g, ' ');
    return '<span class="json-string">[AWS Event Stream 二进制格式]</span><br>' + escapeHtml(cleanStr);
  }

  try {
    const obj = JSON.parse(str);
    const formatted = JSON.stringify(obj, null, 2);
    return syntaxHighlight(formatted);
  } catch (e) {
    // 非 JSON，直接显示原始内容
    return escapeHtml(str);
  }
}

// JSON 语法高亮
function syntaxHighlight(json) {
  // 转义 HTML
  json = json.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  // 添加语法高亮
  return json.replace(
    /("(\\u[\da-fA-F]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g,
    function (match) {
      let cls = 'json-number';
      if (/^"/.test(match)) {
        if (/:$/.test(match)) {
          cls = 'json-key';
        } else {
          cls = 'json-string';
        }
      } else if (/true|false/.test(match)) {
        cls = 'json-boolean';
      } else if (/null/.test(match)) {
        cls = 'json-null';
      }
      return '<span class="' + cls + '">' + match + '</span>';
    }
  );
}

// 解析 JSON 非流式响应（OpenAI/Claude/Gemini）
function parseJSONResponse(obj) {
  let thinking = '';
  let reply = '';

  // OpenAI 格式: choices[0].message.content
  if (obj.choices && obj.choices[0]) {
    const choice = obj.choices[0];
    if (choice.message && choice.message.content) {
      reply = choice.message.content;
    }
  }

  // Claude 格式: content[].type=thinking/text
  if (obj.content && Array.isArray(obj.content)) {
    for (const block of obj.content) {
      if (block.type === 'thinking' && block.thinking) {
        thinking += block.thinking;
      } else if (block.type === 'text' && block.text) {
        reply += block.text;
      }
    }
  }

  // Gemini 格式: response.candidates[0].content.parts[] 或 candidates[0].content.parts[]
  const geminiCandidates = obj.response?.candidates || obj.candidates;
  if (geminiCandidates && geminiCandidates[0]) {
    const candidate = geminiCandidates[0];
    if (candidate.content && candidate.content.parts) {
      for (const part of candidate.content.parts) {
        if (part.thought) {
          thinking += part.thought;
        } else if (part.text) {
          reply += part.text;
        }
        // thoughtSignature 是加密的思考签名，跳过
      }
    }
  }

  return { thinking, reply };
}

// 解析 SSE 流式响应（OpenAI/Claude/Gemini/Codex）
function parseSSEResponse(responseBody) {
  let thinking = '';
  let reply = '';
  let currentBlockType = 'text'; // 跟踪当前内容块类型

  const lines = responseBody.split('\n');
  for (const line of lines) {
    // 跳过非 data 行
    if (!line.startsWith('data:')) continue;
    const data = line.slice(5).trim();
    if (data === '[DONE]' || data === '') continue;

    try {
      const obj = JSON.parse(data);

      // OpenAI SSE: choices[0].delta.content
      if (obj.choices && obj.choices[0] && obj.choices[0].delta) {
        const delta = obj.choices[0].delta;
        if (delta.content) {
          reply += delta.content;
        }
        // OpenAI reasoning_content (o1/o3 系列)
        if (delta.reasoning_content) {
          thinking += delta.reasoning_content;
        }
      }

      // Claude SSE: content_block_start 标记块类型
      if (obj.type === 'content_block_start' && obj.content_block) {
        currentBlockType = obj.content_block.type || 'text';
      }

      // Claude SSE: content_block_delta 内容增量
      if (obj.type === 'content_block_delta' && obj.delta) {
        if (obj.delta.type === 'thinking_delta' && obj.delta.thinking) {
          thinking += obj.delta.thinking;
        } else if (obj.delta.type === 'text_delta' && obj.delta.text) {
          reply += obj.delta.text;
        } else if (obj.delta.text) {
          // 兼容旧格式
          if (currentBlockType === 'thinking') {
            thinking += obj.delta.text;
          } else {
            reply += obj.delta.text;
          }
        }
      }

      // Gemini SSE: response.candidates[0].content.parts[] 或 candidates[0].content.parts[]
      const geminiCandidates = obj.response?.candidates || obj.candidates;
      if (geminiCandidates && geminiCandidates[0]) {
        const candidate = geminiCandidates[0];
        if (candidate.content && candidate.content.parts) {
          for (const part of candidate.content.parts) {
            if (part.thought) {
              thinking += part.thought;
            } else if (part.text) {
              reply += part.text;
            }
            // thoughtSignature 是加密的思考签名，跳过
          }
        }
      }

      // Codex SSE: event: response.output_text.delta, data: {"delta":"内容",...}
      if (obj.type === 'response.output_text.delta' && obj.delta) {
        reply += obj.delta;
      }
    } catch {
      // 忽略单行解析错误
    }
  }

  return { thinking, reply };
}

// 解析错误响应（Anthropic/OpenAI/Gemini 错误格式）
function parseErrorResponse(obj) {
  // Anthropic 错误格式: {"type":"error","error":{"type":"...","message":"..."}}
  if (obj.type === 'error' && obj.error) {
    const errType = obj.error.type || '';
    const errMsg = obj.error.message || '';
    return errType ? `[${errType}] ${errMsg}` : errMsg;
  }

  // OpenAI 错误格式: {"error":{"message":"...","type":"...","code":"..."}}
  if (obj.error && obj.error.message) {
    const errType = obj.error.type || obj.error.code || '';
    const errMsg = obj.error.message || '';
    return errType ? `[${errType}] ${errMsg}` : errMsg;
  }

  // Gemini 错误格式: {"error":{"message":"...","status":"..."}}
  if (obj.error && typeof obj.error === 'object') {
    const status = obj.error.status || '';
    const errMsg = obj.error.message || JSON.stringify(obj.error);
    return status ? `[${status}] ${errMsg}` : errMsg;
  }

  // 通用错误字段
  if (obj.message) {
    return obj.message;
  }

  return '';
}

// 解析 AWS Event Stream 二进制格式（Kiro 响应）
// 从二进制数据中提取 JSON payload 并拼接 content
function parseAWSEventStreamResponse(responseBody) {
  let reply = '';

  // AWS Event Stream 的 JSON payload 格式: {"content":"文本"}
  // 使用正则提取所有 {"content":"..."} 格式的 JSON
  const contentRegex = /\{"content":"([^"]*(?:\\.[^"]*)*)"\}/g;
  let match;

  while ((match = contentRegex.exec(responseBody)) !== null) {
    try {
      // 解析转义字符
      const content = match[1]
        .replace(/\\n/g, '\n')
        .replace(/\\r/g, '\r')
        .replace(/\\t/g, '\t')
        .replace(/\\"/g, '"')
        .replace(/\\\\/g, '\\');
      reply += content;
    } catch {
      // 忽略解析错误
    }
  }

  return reply;
}

// 解析并展示模型响应（思考内容和回复内容）
// 支持格式：OpenAI、Claude、Gemini、Codex（流式和非流式）+ 错误响应
function parseAndDisplayResponse(responseBody) {
  const thinkingEl = document.getElementById('parsedThinking');
  const thinkingTextEl = document.getElementById('parsedThinkingText');
  const replyEl = document.getElementById('parsedReply');
  const replyTextEl = document.getElementById('parsedReplyText');
  const emptyEl = document.getElementById('parsedEmpty');

  // 隐藏所有
  if (thinkingEl) thinkingEl.style.display = 'none';
  if (replyEl) replyEl.style.display = 'none';
  if (emptyEl) emptyEl.style.display = 'block';

  if (!responseBody) return;

  let thinking = '';
  let reply = '';

  // 检测是否为 AWS Event Stream 二进制格式（Kiro 响应）
  // 特征：包含 :event-type 和 :message-type 等二进制头标记
  const isAWSEventStream = responseBody.includes(':event-type') && responseBody.includes(':message-type');

  // 检测是否为 SSE 格式（包含 data: 前缀）
  const isSSE = responseBody.includes('data:') && !isAWSEventStream;

  if (isAWSEventStream) {
    // 解析 AWS Event Stream 二进制格式（Kiro）
    reply = parseAWSEventStreamResponse(responseBody);
  } else if (isSSE) {
    // 解析 SSE 流式响应
    const result = parseSSEResponse(responseBody);
    thinking = result.thinking;
    reply = result.reply;
  } else {
    // 解析 JSON 非流式响应
    try {
      const obj = JSON.parse(responseBody);

      // 首先尝试解析错误响应
      const errorMsg = parseErrorResponse(obj);
      if (errorMsg) {
        reply = errorMsg;
      } else {
        // 正常响应解析
        const result = parseJSONResponse(obj);
        thinking = result.thinking;
        reply = result.reply;
      }
    } catch {
      // 非有效 JSON，跳过
    }
  }

  // 展示解析结果
  let hasContent = false;

  if (thinking) {
    if (thinkingEl) thinkingEl.style.display = 'block';
    if (thinkingTextEl) thinkingTextEl.textContent = thinking;
    hasContent = true;
  }

  if (reply) {
    if (replyEl) replyEl.style.display = 'block';
    if (replyTextEl) replyTextEl.textContent = reply;
    hasContent = true;
  }

  if (hasContent && emptyEl) {
    emptyEl.style.display = 'none';
  }
}

// 切换请求体显示
function toggleRequestBody() {
  const container = document.getElementById('requestBodyContainer');
  const icon = document.getElementById('requestBodyIcon');
  if (container) {
    container.classList.toggle('collapsed');
    if (icon) {
      icon.textContent = container.classList.contains('collapsed') ? '▼' : '▲';
    }
  }
}

// 切换完整响应体显示
function toggleRawResponse() {
  const container = document.getElementById('rawResponseContainer');
  const icon = document.getElementById('rawResponseIcon');
  if (container) {
    container.classList.toggle('collapsed');
    if (icon) {
      icon.textContent = container.classList.contains('collapsed') ? '▼' : '▲';
    }
  }
}

// 复制内容到剪贴板
async function copyContent(elementId) {
  const el = document.getElementById(elementId);
  if (!el) return;

  // 获取纯文本内容（去除 HTML 标签）
  const text = el.textContent || el.innerText;
  if (!text || text === '-') {
    if (window.showError) showError('没有可复制的内容');
    return;
  }

  try {
    await navigator.clipboard.writeText(text);
    // 显示复制成功提示
    if (window.showSuccess) {
      showSuccess('已复制到剪贴板');
    }
  } catch (err) {
    console.error('复制失败:', err);
    // 降级方案：使用 execCommand
    try {
      const textarea = document.createElement('textarea');
      textarea.value = text;
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand('copy');
      document.body.removeChild(textarea);
      if (window.showSuccess) showSuccess('已复制到剪贴板');
    } catch {
      if (window.showError) showError('复制失败');
    }
  }
}

// 格式化时间
function formatTime(timestamp) {
  if (!timestamp) return '-';
  const date = new Date(timestamp);
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  });
}

// HTML 转义
function escapeHtml(str) {
  if (!str) return '';
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

// 导出到全局
window.toggleMonitor = toggleMonitor;
window.viewDetail = viewDetail;
window.closeDetailModal = closeDetailModal;
window.clearTraces = clearTraces;
window.closeConfirmModal = closeConfirmModal;
window.confirmClearTraces = confirmClearTraces;
window.applyFilter = applyFilter;
window.setFilter = setFilter;
window.toggleRequestBody = toggleRequestBody;
window.toggleRawResponse = toggleRawResponse;
window.copyContent = copyContent;
