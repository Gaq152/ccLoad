    let currentLogsPage = 1;
    let logsPageSize = 20;
    let totalLogsPages = 1;
    let totalLogs = 0;
    let currentChannelType = 'all'; // 当前选中的渠道类型
    let authTokens = []; // 令牌列表
    let defaultTestContent = 'sonnet 4.0的发布日期是什么'; // 默认测试内容（从设置加载）

    // 活跃请求轮询
    let activeRequestsTimer = null;
    const ACTIVE_REQUESTS_INTERVAL = 1000; // 1秒刷新一次

    // SSE 实时日志缓冲区（混合分页模式）
    let realtimeBuffer = []; // SSE 推送的新日志缓冲区
    const BUFFER_MAX_SIZE = 500; // 缓冲区最大容量
    let hasNewLogs = false; // 是否有新日志（用于非第一页提示）

    // 从 URL 提取域名部分（用于日志显示）
    function extractUrlHost(url) {
      if (!url) return '';
      try {
        const u = new URL(url);
        return u.host;
      } catch {
        return url.slice(0, 30) + (url.length > 30 ? '...' : '');
      }
    }

    const keyHashCache = new Map();

    function isDisplayOnlyKey(value) {
      return typeof value === 'string' && /^\[[^\]]+\]$/.test(value.trim());
    }

    async function hashKeyForCompare(key) {
      if (!key || !window.crypto?.subtle || typeof TextEncoder === 'undefined') {
        return '';
      }
      if (keyHashCache.has(key)) {
        return keyHashCache.get(key);
      }

      const promise = (async () => {
        const data = new TextEncoder().encode(key);
        const digest = await window.crypto.subtle.digest('SHA-256', data);
        return Array.from(new Uint8Array(digest))
          .map((b) => b.toString(16).padStart(2, '0'))
          .join('');
      })();

      keyHashCache.set(key, promise);
      return promise;
    }

    // 加载默认测试内容（从系统设置）
    async function loadDefaultTestContent() {
      try {
        const data = await fetchDataWithAuth('/admin/settings/channel_test_content');
        if (data?.value) {
          defaultTestContent = data.value;
        }
      } catch (e) {
        console.warn('加载默认测试内容失败，使用内置默认值', e);
      }
    }

    async function load() {
      try {
        showLoading();

        // 从表单元素获取筛选条件（支持下拉框切换后立即生效）
        const range = document.getElementById('f_hours')?.value || 'today';
        const channelId = document.getElementById('f_id')?.value?.trim() || '';
        const channelName = document.getElementById('f_name')?.value?.trim() || '';
        const model = document.getElementById('f_model')?.value?.trim() || '';
        const statusCode = document.getElementById('f_status')?.value?.trim() || '';
        const authTokenId = document.getElementById('f_auth_token')?.value?.trim() || '';

        let finalData = [];
        let serverTotal = 0;

        // 混合分页模式：第一页使用缓冲区 + 服务端，其他页纯服务端
        if (currentLogsPage === 1) {
          // 第一页：从 realtimeBuffer 取数据
          const fromBuffer = realtimeBuffer.slice(0, logsPageSize);
          const needFromServer = logsPageSize - fromBuffer.length;

          if (needFromServer > 0) {
            // 需要从服务端补齐
            const params = new URLSearchParams({
              range,
              limit: needFromServer.toString(),
              offset: '0'
            });

            if (channelId) params.set('channel_id_like', channelId);
            if (channelName) params.set('channel_name_like', channelName);
            if (model) params.set('model_like', model);
            if (statusCode) params.set('status_code_like', statusCode);
            if (authTokenId) params.set('auth_token_id', authTokenId);
            if (currentChannelType && currentChannelType !== 'all') {
              params.set('channel_type', currentChannelType);
            }

            const response = await fetchAPIWithAuth('/admin/logs?' + params.toString());
            const result = response.data || {};
            const serverData = result.data || result || [];
            serverTotal = response.count || result.total || 0;

            // 合并缓冲区和服务端数据
            finalData = [...fromBuffer, ...serverData];
          } else {
            // 缓冲区数据已足够
            finalData = fromBuffer;
          }

          // 总日志数 = 缓冲区 + 服务端
          totalLogs = realtimeBuffer.length + serverTotal;
          totalLogsPages = Math.ceil(totalLogs / logsPageSize) || 1;

          // 清除"有新日志"标记
          hasNewLogs = false;
          hideNewLogsBadge();

        } else {
          // 其他页：纯服务端分页，offset 需要减去缓冲区长度
          const effectiveOffset = (currentLogsPage - 1) * logsPageSize - realtimeBuffer.length;
          const serverOffset = Math.max(effectiveOffset, 0);

          const params = new URLSearchParams({
            range,
            limit: logsPageSize.toString(),
            offset: serverOffset.toString()
          });

          if (channelId) params.set('channel_id_like', channelId);
          if (channelName) params.set('channel_name_like', channelName);
          if (model) params.set('model_like', model);
          if (statusCode) params.set('status_code_like', statusCode);
          if (authTokenId) params.set('auth_token_id', authTokenId);
          if (currentChannelType && currentChannelType !== 'all') {
            params.set('channel_type', currentChannelType);
          }

          const response = await fetchAPIWithAuth('/admin/logs?' + params.toString());
          const result = response.data || {};
          finalData = result.data || result || [];
          serverTotal = response.count || result.total || 0;

          // 总日志数 = 缓冲区 + 服务端
          totalLogs = realtimeBuffer.length + serverTotal;
          totalLogsPages = Math.ceil(totalLogs / logsPageSize) || 1;
        }

        // 从日志列表初始化 lastReceivedLogTimeMs，确保 SSE 重连时能正确恢复
        syncLastReceivedFromList(finalData);

        updatePagination();
        renderLogs(finalData);
        updateStats(finalData);

      } catch (error) {
        console.error('加载日志失败:', error);
        try { if (window.showError) window.showError('无法加载请求日志'); } catch(_){}
        showError();
      }
    }

    // ✅ 动态计算列数（避免硬编码维护成本）
    function getTableColspan() {
      const headerCells = document.querySelectorAll('thead th');
      return headerCells.length || 13; // fallback到13列（向后兼容）
    }

    function showLoading() {
      const tbody = document.getElementById('tbody');
      const colspan = getTableColspan();
      const loadingRow = TemplateEngine.render('tpl-log-loading', { colspan });
      tbody.innerHTML = '';
      if (loadingRow) tbody.appendChild(loadingRow);
    }

    function showError() {
      const tbody = document.getElementById('tbody');
      const colspan = getTableColspan();
      const errorRow = TemplateEngine.render('tpl-log-error', { colspan });
      tbody.innerHTML = '';
      if (errorRow) tbody.appendChild(errorRow);
    }
    // [FIX] 避免本地 showError 污染全局 window.showError（toast）
    // logs.js 未用 IIFE 包裹，脚本顶层的 function 声明会覆盖 window.showError
    // 还原 ui.js 注册的 toast 版本
    if (typeof App !== 'undefined' && App.ui && App.ui.showToast) {
      window.showError = (msg) => App.ui.showToast(msg, 'error');
    }

    function renderLogs(data) {
      const tbody = document.getElementById('tbody');
      const colspan = getTableColspan();

      if (data.length === 0) {
        const emptyRow = TemplateEngine.render('tpl-log-empty', { colspan });
        tbody.innerHTML = '';
        if (emptyRow) tbody.appendChild(emptyRow);
        return;
      }

      tbody.innerHTML = '';

      for (const entry of data) {
        const rowEl = createLogRow(entry);
        if (rowEl) tbody.appendChild(rowEl);
        // 将已渲染的日志加入去重集合，避免 SSE 历史补推导致重复
        displayedLogIds.add(buildLogKey(entry));
      }
    }

    // ============================================================
    // 活跃请求显示（进行中的请求）
    // ============================================================

    // 格式化字节数为可读形式（K/M/G）
    function formatBytes(bytes) {
      if (bytes == null || bytes <= 0) return '';
      const UNITS = ['B', 'K', 'M', 'G'];
      const FACTOR = 1024;
      const i = Math.min(Math.floor(Math.log(bytes) / Math.log(FACTOR)), UNITS.length - 1);
      const value = bytes / Math.pow(FACTOR, i);
      return value.toFixed(i > 0 ? 1 : 0) + ' ' + UNITS[i];
    }

    function clearActiveRequestsRows() {
      document.querySelectorAll('tr.pending-row').forEach(el => el.remove());
    }

    // 渲染进行中的请求（插入到表格顶部）
    function renderActiveRequests(activeRequests) {
      clearActiveRequestsRows();
      if (!activeRequests || activeRequests.length === 0) return;

      const tbody = document.getElementById('tbody');
      const firstRow = tbody.firstChild;
      const totalCols = getTableColspan();
      const fragment = document.createDocumentFragment();

      for (const req of activeRequests) {
        const startMs = req.start_time ? req.start_time * 1000 : 0;
        const elapsed = startMs ? ((Date.now() - startMs) / 1000).toFixed(1) : '-';
        const streamFlag = req.is_streaming ?
          '<span class="stream-flag">流</span>' :
          '<span class="stream-flag placeholder">流</span>';

        // 渠道显示
        let channelDisplay = '<span style="color: var(--neutral-500);">选择中...</span>';
        if (req.channel_name) {
          channelDisplay = `${escapeHtml(req.channel_name)} <small>(#${req.channel_id})</small>`;
        }

        // Key显示
        let keyDisplay = '<span style="color: var(--neutral-500);">-</span>';
        if (req.api_key_used) {
          keyDisplay = `<span style="font-family: monospace; font-size: 0.85em;">${escapeHtml(req.api_key_used)}</span>`;
        }

        // 字节数显示
        const bytesInfo = formatBytes(req.bytes_received);
        const hasBytes = !!bytesInfo;
        const infoDisplay = hasBytes ? `已接收 ${bytesInfo}` : '请求处理中...';
        const infoColor = hasBytes ? 'var(--success-600)' : 'var(--neutral-500)';

        const row = document.createElement('tr');
        row.className = 'pending-row';
        row.innerHTML = `
          <td>${formatTime(req.start_time)}</td>
          <td>${escapeHtml(req.client_ip || '-')}</td>
          <td><div class="model-request-cell"><span class="model-tag">${escapeHtml(req.model)}</span>${renderRequestTypeBadge(req.request_type)}</div></td>
          <td class="config-info">${channelDisplay}</td>
          <td style="text-align: center;">${keyDisplay}</td>
          <td><span class="status-pending">进行中</span></td>
          <td style="text-align: right;">${elapsed}s... ${streamFlag}</td>
          <td></td>
          <td><span style="color: ${infoColor};">${escapeHtml(infoDisplay)}</span></td>
        `;
        fragment.appendChild(row);
      }

      tbody.insertBefore(fragment, firstRow);
    }

    // 加载活跃请求
    async function loadActiveRequests() {
      try {
        const data = await fetchDataWithAuth('/admin/logs/active');
        renderActiveRequests(data || []);
      } catch (e) {
        console.warn('加载活跃请求失败:', e);
      }
    }

    // 启动活跃请求轮询
    function startActiveRequestsPolling() {
      if (activeRequestsTimer) return;
      loadActiveRequests();
      activeRequestsTimer = setInterval(loadActiveRequests, ACTIVE_REQUESTS_INTERVAL);
    }

    // 停止活跃请求轮询
    function stopActiveRequestsPolling() {
      if (activeRequestsTimer) {
        clearInterval(activeRequestsTimer);
        activeRequestsTimer = null;
      }
      clearActiveRequestsRows();
    }

    // ============================================================
    // 性能优化：独立行渲染函数（用于复用）
    // ============================================================
    function createLogRow(entry) {
      // 0. 客户端IP和令牌名称显示
      const ipPart = entry.client_ip ? escapeHtml(entry.client_ip) : '-';
      const tokenPart = entry.auth_token_name ?
        `<div style="font-size: 0.8em; color: var(--primary-600); margin-top: 2px;" title="令牌: ${escapeHtml(entry.auth_token_name)}">${escapeHtml(entry.auth_token_name)}</div>` : '';
      const clientIPDisplay = `${ipPart}${tokenPart}`;

      // 1. 渠道信息显示（含 API URL）
      const configInfo = entry.channel_name ||
        (entry.channel_id ? `渠道 #${entry.channel_id}` :
         (entry.message === 'exhausted backends' ? '系统（所有渠道失败）' :
          entry.message === 'no available upstream (all cooled or none)' ? '系统（无可用渠道）' : '系统'));
      const apiUrlDisplay = entry.api_base_url ?
        `<div style="font-size: 0.8em; color: var(--neutral-500); margin-top: 2px;" title="${escapeHtml(entry.api_base_url)}">${escapeHtml(extractUrlHost(entry.api_base_url))}</div>` : '';
      const configDisplay = entry.channel_id ?
        `<a class="channel-link" href="/web/channels.html?id=${entry.channel_id}#channel-${entry.channel_id}">${escapeHtml(entry.channel_name||'')} <small>(#${entry.channel_id})</small></a>${apiUrlDisplay}` :
        `<span style="color: var(--neutral-500);">${escapeHtml(configInfo)}</span>`;

      // 2. 状态码样式 & 行背景样式
      const statusClass = (entry.status_code >= 200 && entry.status_code < 300) ?
        'status-success' : 'status-error';
      const statusCode = entry.status_code;

      // 根据状态码决定行背景色
      let rowClass = '';
      if (entry.status_code >= 500) {
        rowClass = 'log-row-error';
      } else if (entry.status_code >= 400 || (entry.status_code < 200 && entry.status_code > 0)) {
        rowClass = 'log-row-warning';
      }

      // 3. 模型显示
      const modelValue = entry.model ?
        `<span class="model-with-badges"><span class="model-tag">${escapeHtml(entry.model)}</span>${renderFastBadge(entry)}</span>` :
        '<span style="color: var(--neutral-500);">-</span>';
      const modelDisplay = `<div class="model-request-cell">${modelValue}${renderRequestTypeBadge(entry.request_type)}</div>`;

      // 4. 响应时间显示(流式/非流式)
      const hasDuration = entry.duration !== undefined && entry.duration !== null;
      const durationDisplay = hasDuration ?
        `<span style="color: var(--neutral-700);">${entry.duration.toFixed(3)}</span>` :
        '<span style="color: var(--neutral-500);">-</span>';

      const streamFlag = entry.is_streaming ?
        '<span class="stream-flag">流</span>' :
        '<span class="stream-flag placeholder">流</span>';

      let responseTimingDisplay;
      if (entry.is_streaming) {
        const hasFirstByte = entry.first_byte_time !== undefined && entry.first_byte_time !== null;
        const firstByteDisplay = hasFirstByte ?
          `<span style="color: var(--success-600);">${entry.first_byte_time.toFixed(3)}</span>` :
          '<span style="color: var(--neutral-500);">-</span>';
        responseTimingDisplay = `
          <span style="display: inline-flex; align-items: center; justify-content: flex-end; gap: 4px; white-space: nowrap;">
            ${firstByteDisplay}
            <span style="color: var(--neutral-400);">/</span>
            ${durationDisplay}
          </span>
          ${streamFlag}
        `;
      } else {
        responseTimingDisplay = `
          <span style="display: inline-flex; align-items: center; justify-content: flex-end; gap: 4px; white-space: nowrap;">
            ${durationDisplay}
          </span>
          ${streamFlag}
        `;
      }

      // 5. API Key显示(含按钮组)
      let apiKeyDisplay = '';
      const isActionableKey = entry.api_key_used && entry.channel_id && !isDisplayOnlyKey(entry.api_key_used);
      if (isActionableKey) {
        const sc = entry.status_code || 0;
        const showTestBtn = sc !== 200;
        const showDeleteBtn = sc === 403;

        let buttons = '';
        if (showTestBtn) {
          buttons += `
            <button
              class="test-key-btn"
              data-action="test"
              data-channel-id="${entry.channel_id}"
              data-channel-name="${escapeHtml(entry.channel_name || '').replace(/"/g, '&quot;')}"
              data-api-key="${escapeHtml(entry.api_key_used).replace(/"/g, '&quot;')}"
              data-api-key-hash="${escapeHtml(entry.api_key_hash || '').replace(/"/g, '&quot;')}"
              data-model="${escapeHtml(entry.model).replace(/"/g, '&quot;')}"
              title="测试此 API Key">
              ⚡
            </button>
          `;
        }
        if (showDeleteBtn) {
          buttons += `
            <button
              class="test-key-btn"
              style="color: var(--error-600);"
              data-action="delete"
              data-channel-id="${entry.channel_id}"
              data-channel-name="${escapeHtml(entry.channel_name || '').replace(/"/g, '&quot;')}"
              data-api-key="${escapeHtml(entry.api_key_used).replace(/"/g, '&quot;')}"
              title="删除此 API Key">
              🗑
            </button>
          `;
        }

        apiKeyDisplay = `
          <div
            style="display: flex; align-items: center; gap: 6px; justify-content: center;"
            data-api-key-hash="${escapeHtml(entry.api_key_hash || '').replace(/"/g, '&quot;')}">
            <code style="font-size: 0.9em; color: var(--neutral-600);">${escapeHtml(entry.api_key_used)}</code>
            ${buttons}
          </div>
        `;
      } else if (entry.api_key_used) {
        apiKeyDisplay = `<code style="font-size: 0.9em; color: var(--neutral-600);">${escapeHtml(entry.api_key_used)}</code>`;
      } else {
        apiKeyDisplay = '<span style="color: var(--neutral-500);">-</span>';
      }

      // 6. Token统计显示(0值为空)
      const tokenValue = (value, color) => {
        if (value === undefined || value === null || value === 0) {
          return '';
        }
        return `<span class="token-metric-value" style="color: ${color};">${value.toLocaleString()}</span>`;
      };
      const inputTokensDisplay = tokenValue(entry.input_tokens, 'var(--neutral-700)');
      const outputTokensDisplay = tokenValue(entry.output_tokens, 'var(--neutral-700)');
      const cacheReadDisplay = tokenValue(entry.cache_read_input_tokens, 'var(--success-600)');
      const cacheCreationDisplay = tokenValue(entry.cache_creation_input_tokens, 'var(--primary-600)');
      const cacheRateDisplay = formatLogCacheRate(entry);

      // 7. 成本显示(0值为空)
      const costDisplay = entry.cost ?
        `<span style="color: var(--warning-600); font-weight: 500;">${formatCost(entry.cost)}</span>` :
        '';

      // 返回 DOM 元素
      return TemplateEngine.render('tpl-log-row', {
        rowClass,
        time: formatTime(entry.time),
        clientIPDisplay,
        modelDisplay,
        configDisplay,
        apiKeyDisplay,
        statusClass,
        statusCode,
        responseTimingDisplay,
        inputTokensDisplay,
        outputTokensDisplay,
        cacheReadDisplay,
        cacheCreationDisplay,
        cacheRateDisplay,
        costDisplay,
        message: entry.message || ''
      });
    }

    function formatLogCacheRate(entry) {
      const input = Number(entry.input_tokens) || 0;
      const cacheRead = Number(entry.cache_read_input_tokens) || 0;
      const cacheCreation = Number(entry.cache_creation_input_tokens) || 0;
      const totalPromptTokens = input + cacheRead + cacheCreation;
      if (totalPromptTokens <= 0 || cacheRead <= 0) return '';
      const rate = (cacheRead / totalPromptTokens) * 100;
      return `<span class="token-metric-value" title="缓存读取 / (输入 + 缓存读取 + 缓存创建)" style="color: var(--success-700);">${rate.toFixed(1)}%</span>`;
    }

    function renderFastBadge(entry) {
      if (!entry || !entry.is_fast) return '';

      const multiplier = Number(entry.fast_multiplier);
      const multiplierText = Number.isFinite(multiplier) ? `${formatFastMultiplier(multiplier)}x` : '';
      const tier = entry.service_tier ? ` · ${entry.service_tier}` : '';
      const title = `Fast 请求${tier}${multiplierText ? ` · ${multiplierText}` : ''}`;

      return `<span class="fast-badge" title="${escapeHtml(title).replace(/"/g, '&quot;')}">FAST</span>`;
    }

    function renderRequestTypeBadge(requestType) {
      const meta = {
        compact_v1: { label: '压缩 V1', title: '独立 /responses/compact 压缩请求' },
        compact_v2: { label: '压缩 V2', title: '携带 compaction_trigger 的 Responses 请求' },
        search: { label: '搜索', title: 'Codex Search 请求' }
      }[requestType];
      if (!meta) return '';
      return `<span class="request-type-badge request-type-${requestType}" title="${escapeHtml(meta.title).replace(/"/g, '&quot;')}">${escapeHtml(meta.label)}</span>`;
    }

    function formatFastMultiplier(value) {
      return Number(value).toFixed(2).replace(/\.?0+$/, '');
    }

    // ============================================================
    // 筛选检查：判断日志是否符合当前筛选条件
    // ============================================================
    function matchesCurrentFilter(entry) {
      // 获取当前筛选条件
      const channelId = document.getElementById('f_id')?.value?.trim() || '';
      const channelName = document.getElementById('f_name')?.value?.trim() || '';
      const model = document.getElementById('f_model')?.value?.trim() || '';
      const statusCode = document.getElementById('f_status')?.value?.trim() || '';
      const authTokenId = document.getElementById('f_auth_token')?.value?.trim() || '';
      const range = document.getElementById('f_hours')?.value || 'today';

      // 渠道 ID 前缀匹配（输入 "1" 匹配 1, 10, 11, 12 等）
      if (channelId && !String(entry.channel_id || '').startsWith(channelId)) {
        return false;
      }

      // 渠道名称模糊匹配
      if (channelName && !(entry.channel_name || '').toLowerCase().includes(channelName.toLowerCase())) {
        return false;
      }

      // 模型模糊匹配
      if (model && !(entry.model || '').toLowerCase().includes(model.toLowerCase())) {
        return false;
      }

      // 状态码前缀匹配（输入 "4" 匹配 400, 401, 403 等 4xx 错误）
      if (statusCode && !String(entry.status_code || '').startsWith(statusCode)) {
        return false;
      }

      // 令牌 ID 精确匹配（下拉菜单选择）
      if (authTokenId && String(entry.auth_token_id) !== authTokenId) {
        return false;
      }

      // 渠道类型匹配（下拉菜单选择）
      if (currentChannelType && currentChannelType !== 'all') {
        if (String(entry.channel_type) !== currentChannelType) {
          return false;
        }
      }

      // 时间范围匹配（SSE 推送的新日志通常都在范围内，但需要检查）
      const logTimeMs = extractLogTimeMs(entry);
      if (logTimeMs > 0 && range !== 'all') {
        const now = Date.now();
        let rangeStartMs = 0;

        if (range === 'today') {
          // 今天0点
          const today = new Date();
          today.setHours(0, 0, 0, 0);
          rangeStartMs = today.getTime();
        } else if (range === 'yesterday') {
          // 昨天0点到今天0点
          const today = new Date();
          today.setHours(0, 0, 0, 0);
          const yesterday = new Date(today.getTime() - 24 * 60 * 60 * 1000);
          // 昨天的日志，SSE 推送的新日志肯定不是昨天的
          if (logTimeMs < yesterday.getTime() || logTimeMs >= today.getTime()) {
            return false;
          }
        } else {
          // 解析小时数（如 "1", "6", "24", "168"）
          const hours = parseInt(range);
          if (!isNaN(hours) && hours > 0) {
            rangeStartMs = now - hours * 60 * 60 * 1000;
          }
        }

        if (rangeStartMs > 0 && logTimeMs < rangeStartMs) {
          return false;
        }
      }

      return true;
    }

    // ============================================================
    // 性能优化：增量插入实时日志（避免全量重渲染）
    // ============================================================
    function prependRealtimeLog(entry) {
      const tbody = document.getElementById('tbody');
      // 如果当前显示的是空状态/加载行，先清空
      const emptyOrLoading = tbody.querySelector('[colspan]');
      if (emptyOrLoading) {
        tbody.innerHTML = '';
      }

      const rowEl = createLogRow(entry);
      if (rowEl) {
        // 添加简单的进入动画（使用 styles.css 中定义的 slideInUp）
        rowEl.style.animation = 'slideInUp 0.25s ease-out';
        tbody.prepend(rowEl);
        trimExcessRows();
      }
    }

    // ============================================================
    // 性能优化：限制 DOM 节点数量（防止内存泄漏）
    // ============================================================
    function trimExcessRows() {
      const tbody = document.getElementById('tbody');
      while (tbody.children.length > logsPageSize) {
        if (tbody.lastElementChild) {
          tbody.removeChild(tbody.lastElementChild);
        } else {
          break;
        }
      }
    }

    function updatePagination() {
      // 更新页码显示（只更新底部分页）
      const currentPage2El = document.getElementById('logs_current_page2');
      const totalPages2El = document.getElementById('logs_total_pages2');
      const prev2El = document.getElementById('logs_prev2');
      const next2El = document.getElementById('logs_next2');
      const jumpPageInput = document.getElementById('logs_jump_page');

      if (currentPage2El) currentPage2El.textContent = currentLogsPage;
      if (totalPages2El) totalPages2El.textContent = totalLogsPages;

      // 更新跳转输入框的max属性
      if (jumpPageInput) {
        jumpPageInput.max = totalLogsPages;
        jumpPageInput.placeholder = `1-${totalLogsPages}`;
      }

      // 更新按钮状态（只更新底部分页）
      const prevDisabled = currentLogsPage <= 1;
      const nextDisabled = currentLogsPage >= totalLogsPages;

      if (prev2El) prev2El.disabled = prevDisabled;
      if (next2El) next2El.disabled = nextDisabled;
    }

    function updateStats(data) {
      // 更新筛选器统计信息
      const displayedCountEl = document.getElementById('displayedCount');
      const totalCountEl = document.getElementById('totalCount');

      if (displayedCountEl) displayedCountEl.textContent = data.length;
      if (totalCountEl) totalCountEl.textContent = totalLogs || data.length;
    }

    function scrollLogsResultsToTop() {
      if (!window.matchMedia('(max-width: 768px)').matches) return;
      document.querySelector('.logs-table')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }

    async function prevLogsPage() {
      if (currentLogsPage > 1) {
        currentLogsPage--;
        await load();
        scrollLogsResultsToTop();
      }
    }

    async function nextLogsPage() {
      if (currentLogsPage < totalLogsPages) {
        currentLogsPage++;
        await load();
        scrollLogsResultsToTop();
      }
    }

    async function jumpToPage() {
      const jumpPageInput = document.getElementById('logs_jump_page');
      if (!jumpPageInput) return;

      const targetPage = parseInt(jumpPageInput.value);

      // 输入验证
      if (isNaN(targetPage) || targetPage < 1 || targetPage > totalLogsPages) {
        jumpPageInput.value = ''; // 清空无效输入
        if (window.showError) {
          try {
            window.showError(`请输入有效的页码 (1-${totalLogsPages})`);
          } catch(_) {}
        }
        return;
      }

      // 跳转到目标页
      if (targetPage !== currentLogsPage) {
        currentLogsPage = targetPage;
        await load();
        scrollLogsResultsToTop();
      }

      // 清空输入框
      jumpPageInput.value = '';
    }

    async function changePageSize() {
      const newPageSize = parseInt(document.getElementById('page_size').value);
      if (newPageSize !== logsPageSize) {
        logsPageSize = newPageSize;
        currentLogsPage = 1;
        totalLogsPages = 1;
        await load();
        scrollLogsResultsToTop();
      }
    }

    function applyFilter() {
      currentLogsPage = 1;
      totalLogsPages = 1;

      const range = document.getElementById('f_hours').value.trim();
      const id = document.getElementById('f_id').value.trim();
      const name = document.getElementById('f_name').value.trim();
      const model = document.getElementById('f_model').value.trim();
      const status = document.getElementById('f_status') ? document.getElementById('f_status').value.trim() : '';
      const authToken = document.getElementById('f_auth_token').value.trim();
      const channelType = document.getElementById('f_channel_type').value.trim();

      // 保存筛选条件到 localStorage
      saveLogsFilters();

      // 构建 URL 参数（用于分享链接）
      const q = new URLSearchParams();

      if (range && range !== 'today') q.set('range', range);
      if (id) q.set('channel_id_like', id);
      if (name) q.set('channel_name_like', name);
      if (model) q.set('model_like', model);
      if (status) q.set('status_code_like', status);
      if (authToken) q.set('auth_token_id', authToken);
      if (channelType && channelType !== 'all') q.set('channel_type', channelType);

      // 使用 replaceState 更新 URL，不刷新页面
      const newUrl = q.toString() ? '?' + q.toString() : location.pathname;
      history.replaceState(null, '', newUrl);

      // 清空实时缓冲区（筛选条件变化后缓冲区数据可能不符合新条件）
      realtimeBuffer = [];
      displayedLogIds.clear();

      // 重新加载数据
      load();
    }

    function initFilters() {
      const u = new URLSearchParams(location.search);
      const saved = loadLogsFilters();
      // URL 参数优先，否则从 localStorage 恢复
      const hasUrlParams = u.toString().length > 0;

      // 兼容新旧参数名
      const id = u.get('channel_id_like') || u.get('channel_id') || (!hasUrlParams && saved?.channelId) || '';
      const name = u.get('channel_name_like') || u.get('channel_name') || (!hasUrlParams && saved?.channelName) || '';
      const range = u.get('range') || (!hasUrlParams && saved?.range) || 'today';
      const model = u.get('model_like') || u.get('model') || (!hasUrlParams && saved?.model) || '';
      const status = u.get('status_code_like') || u.get('status_code') || (!hasUrlParams && saved?.status) || '';
      const authToken = u.get('auth_token_id') || (!hasUrlParams && saved?.authToken) || '';
      const channelType = u.get('channel_type') || (!hasUrlParams && saved?.channelType) || 'all';

      // 初始化时间范围选择器 (默认"本日")，切换后立即筛选
      if (window.initDateRangeSelector) {
        initDateRangeSelector('f_hours', 'today', () => {
          saveLogsFilters();
          currentLogsPage = 1;
          // 清空实时缓冲区（筛选条件变化后缓冲区数据可能不符合新条件）
          realtimeBuffer = [];
          displayedLogIds.clear();
          load();
        });
        // 设置URL中的值
        document.getElementById('f_hours').value = range;
      }

      document.getElementById('f_id').value = id;
      document.getElementById('f_name').value = name;
      document.getElementById('f_model').value = model;
      const statusEl = document.getElementById('f_status');
      if (statusEl) statusEl.value = status;

      // 设置渠道类型
      currentChannelType = channelType;
      const channelTypeEl = document.getElementById('f_channel_type');
      if (channelTypeEl) channelTypeEl.value = channelType;

      // 加载令牌列表
      loadAuthTokens().then(() => {
        document.getElementById('f_auth_token').value = authToken;
      });

      // 令牌选择器切换后立即筛选
      document.getElementById('f_auth_token').addEventListener('change', () => {
        saveLogsFilters();
        currentLogsPage = 1;
        // 清空实时缓冲区（筛选条件变化后缓冲区数据可能不符合新条件）
        realtimeBuffer = [];
        displayedLogIds.clear();
        load();
      });

      // 输入框自动筛选（防抖）
      const debouncedFilter = debounce(applyFilter, 500);
      ['f_id', 'f_name', 'f_model', 'f_status'].forEach(id => {
        const el = document.getElementById(id);
        if (el) {
          el.addEventListener('input', debouncedFilter);
        }
      });

      // 回车键筛选
      ['f_hours', 'f_id', 'f_name', 'f_model', 'f_status', 'f_auth_token', 'f_channel_type'].forEach(id => {
        const el = document.getElementById(id);
        if (el) {
          el.addEventListener('keydown', e => {
            if (e.key === 'Enter') applyFilter();
          });
        }
      });
    }

    function formatTime(timeStr) {
      try {
        // 处理Unix timestamp（秒或毫秒）或ISO字符串
        let timestamp = timeStr;
        if (typeof timeStr === 'number' || /^\d+$/.test(timeStr)) {
          const raw = Number(timeStr);
          // 13位及以上视为毫秒，10位视为秒
          timestamp = raw > 1e12 ? raw : raw * 1000;
        }

        const date = new Date(timestamp);
        if (isNaN(date.getTime()) || date.getFullYear() < 2020) {
          return '-';
        }

        // 计算相对时间
        const now = Date.now();
        const diffMs = now - date.getTime();
        const diffMinutes = Math.floor(diffMs / 60000);
        const diffHours = Math.floor(diffMs / 3600000);

        // 相对时间显示
        // 注：服务器与客户端时间可能有微小差异，允许5秒内的"未来时间"也显示为"刚刚"
        let relativeTime = '';
        if (diffMs < -5000) {
          relativeTime = ''; // 超过5秒的未来时间不显示相对时间
        } else if (diffMinutes < 1) {
          relativeTime = '刚刚';
        } else if (diffMinutes < 60) {
          relativeTime = `${diffMinutes}分钟前`;
        } else if (diffHours < 24) {
          relativeTime = `${diffHours}小时前`;
        }

        // 绝对时间
        const absoluteTime = date.toLocaleString('zh-CN', {
          year: 'numeric',
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit'
        });

        // 返回格式：时间点在前，相对时间在后 "绝对时间 · 相对时间"
        if (relativeTime) {
          return `<span style="color: var(--primary-600); font-weight: 500;">${absoluteTime}</span> <span style="color: var(--neutral-400); font-size: 0.85em;">· ${relativeTime}</span>`;
        }
        return absoluteTime;
      } catch (e) {
        return '-';
      }
    }

    // 加载令牌列表
    async function loadAuthTokens() {
      try {
        const data = await fetchDataWithAuth('/admin/auth-tokens');
        // [FIX] 后端返回格式是 {tokens: [...]}，需要提取 tokens 数组
        authTokens = data?.tokens || data || [];

        // 填充令牌选择器
        const tokenSelect = document.getElementById('f_auth_token');
        if (tokenSelect && authTokens.length > 0) {
          // 保留"全部令牌"选项
          tokenSelect.innerHTML = '<option value="">全部令牌</option>';
          authTokens.forEach(token => {
            const option = document.createElement('option');
            option.value = token.id;
            option.textContent = token.description || `令牌 #${token.id}`;
            tokenSelect.appendChild(option);
          });
        }
      } catch (error) {
        console.error('加载令牌列表失败:', error);
      }
    }

    function parseApiKeysFromChannel(channel) {
      if (!channel) return [];
      // 优先支持新结构：api_keys 为对象数组
      if (Array.isArray(channel.api_keys)) {
        return channel.api_keys
          .map(k => (k && (k.api_key || k.key)) || '')
          .map(k => k.trim())
          .filter(k => k);
      }
      // 向后兼容：api_key 为逗号分隔的字符串
      if (typeof channel.api_key === 'string') {
        return channel.api_key
          .split(',')
          .map(k => k.trim())
          .filter(k => k);
      }
      return [];
    }

    function maskKeyForCompare(key) {
      if (!key) return '';
      if (key.length <= 8) return key;
      return `${key.slice(0, 4)}...${key.slice(-4)}`;
    }

    function findKeyIndexByMaskedKey(keys, maskedKey) {
      if (!maskedKey || !keys || !keys.length) return null;
      const target = maskedKey.trim();
      for (let i = 0; i < keys.length; i++) {
        if (maskKeyForCompare(keys[i]) === target) return i;
      }
      return null;
    }

    async function findKeyIndexForLog(keys, maskedKey, apiKeyHash) {
      if (!keys || !keys.length) {
        return { index: null, matchType: 'none' };
      }

      const normalizedHash = (apiKeyHash || '').trim().toLowerCase();
      if (normalizedHash) {
        for (let i = 0; i < keys.length; i++) {
          const keyHash = await hashKeyForCompare(keys[i]);
          if (keyHash && keyHash === normalizedHash) {
            return { index: i, matchType: 'hash' };
          }
        }
      }

      const maskedIndex = findKeyIndexByMaskedKey(keys, maskedKey);
      if (maskedIndex !== null) {
        return { index: maskedIndex, matchType: 'mask' };
      }

      return { index: null, matchType: 'none' };
    }

    function updateTestKeyIndexInfo(text) {
      const el = document.getElementById('testKeyIndexInfo');
      if (el) el.textContent = text || '';
    }

    // 注销功能（已由 ui.js 的 onLogout 统一处理）

    // localStorage key for logs page filters
    const LOGS_FILTER_KEY = 'logs.filters';

    function saveLogsFilters() {
      try {
        const filters = {
          channelType: document.getElementById('f_channel_type')?.value || 'all',
          range: document.getElementById('f_hours')?.value || 'today',
          channelId: document.getElementById('f_id')?.value || '',
          channelName: document.getElementById('f_name')?.value || '',
          model: document.getElementById('f_model')?.value || '',
          status: document.getElementById('f_status')?.value || '',
          authToken: document.getElementById('f_auth_token')?.value || ''
        };
        localStorage.setItem(LOGS_FILTER_KEY, JSON.stringify(filters));
      } catch (_) {}
    }

    function loadLogsFilters() {
      try {
        const saved = localStorage.getItem(LOGS_FILTER_KEY);
        if (saved) return JSON.parse(saved);
      } catch (_) {}
      return null;
    }

    // 页面初始化
    document.addEventListener('DOMContentLoaded', async function() {
      if (window.initTopbar) initTopbar('logs');

      // 优先从 URL 读取，其次从 localStorage 恢复，默认 all
      const u = new URLSearchParams(location.search);
      const hasUrlParams = u.toString().length > 0;
      const savedFilters = loadLogsFilters();
      currentChannelType = u.get('channel_type') || (!hasUrlParams && savedFilters?.channelType) || 'all';

      await initChannelTypeFilter(currentChannelType);

      initFilters();
      await loadDefaultTestContent();

      // ✅ 修复：如果没有 URL 参数但有保存的筛选条件，先同步 URL 再加载数据
      if (!hasUrlParams && savedFilters) {
        const q = new URLSearchParams();
        if (savedFilters.range) q.set('range', savedFilters.range);
        if (savedFilters.channelId) q.set('channel_id_like', savedFilters.channelId);
        if (savedFilters.channelName) q.set('channel_name_like', savedFilters.channelName);
        if (savedFilters.model) q.set('model_like', savedFilters.model);
        if (savedFilters.status) q.set('status_code_like', savedFilters.status);
        if (savedFilters.authToken) q.set('auth_token_id', savedFilters.authToken);
        if (savedFilters.channelType && savedFilters.channelType !== 'all') {
          q.set('channel_type', savedFilters.channelType);
        }
        // 使用 replaceState 更新 URL，不触发页面刷新
        if (q.toString()) {
          history.replaceState(null, '', '?' + q.toString());
        }
      }

      // ✅ 修复：先加载日志数据（会同步 lastReceivedLogTimeMs），再初始化实时模式
      // 这样 SSE 启动时能正确获取 since_ms 参数，避免重连时丢失日志
      await load();
      initRealtimeToggle();

      // ESC键关闭测试模态框
      document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
          closeTestKeyModal();
        }
      });

      // 事件委托：处理日志表格中的按钮点击
      const tbody = document.getElementById('tbody');
      if (tbody) {
        tbody.addEventListener('click', (e) => {
          const btn = e.target.closest('.test-key-btn[data-action]');
          if (!btn) return;

          const action = btn.dataset.action;
          const channelId = parseInt(btn.dataset.channelId);
          const channelName = btn.dataset.channelName || '';
          const apiKey = btn.dataset.apiKey || '';
          const apiKeyHash = btn.dataset.apiKeyHash || btn.parentElement?.dataset.apiKeyHash || '';
          const model = btn.dataset.model || '';

          if (action === 'test') {
            testKey(channelId, channelName, apiKey, apiKeyHash, model);
          } else if (action === 'delete') {
            deleteKeyFromLog(channelId, channelName, apiKey, apiKeyHash);
          }
        });
      }
    });

    // 初始化渠道类型筛选器
    async function initChannelTypeFilter(initialType) {
      const select = document.getElementById('f_channel_type');
      if (!select) return;

      const types = await window.ChannelTypeManager.getChannelTypes();

      // 添加"全部"选项
      const allOption = document.createElement('option');
      allOption.value = 'all';
      allOption.textContent = '全部';
      if (!initialType || initialType === 'all') {
        allOption.selected = true;
      }
      select.innerHTML = '';
      select.appendChild(allOption);

      types.forEach(type => {
        const option = document.createElement('option');
        option.value = type.value;
        option.textContent = type.display_name;
        if (type.value === initialType) {
          option.selected = true;
        }
        select.appendChild(option);
      });

      // 绑定change事件
      select.addEventListener('change', (e) => {
        currentChannelType = e.target.value;
        saveLogsFilters();
        // 切换渠道类型时重置到第一页并重新加载
        currentLogsPage = 1;
        // 清空实时缓冲区（筛选条件变化后缓冲区数据可能不符合新条件）
        realtimeBuffer = [];
        displayedLogIds.clear();
        load();
      });
    }

    // ========== API Key 测试功能 ==========
    let testingKeyData = null;

    async function testKey(channelId, channelName, apiKey, apiKeyHash, model) {
      testingKeyData = {
        channelId,
        channelName,
        maskedApiKey: apiKey,
        apiKeyHash,
        originalModel: model,
        channelType: null, // 将在异步加载渠道配置后填充
        keyIndex: null
      };

      // 填充模态框基本信息
      document.getElementById('testKeyChannelName').textContent = channelName;
      document.getElementById('testKeyDisplay').textContent = apiKey;
      document.getElementById('testKeyOriginalModel').textContent = model;

      // 重置状态
      resetTestKeyModal();
      updateTestKeyIndexInfo('');

      // 显示模态框
      document.getElementById('testKeyModal').classList.add('show');

      // 异步加载渠道配置以获取支持的模型列表
      try {
        const channel = await fetchDataWithAuth(`/admin/channels/${channelId}`);

        // ✅ 保存渠道类型,用于后续测试请求
        testingKeyData.channelType = channel.channel_type || 'anthropic';
        const apiKeys = parseApiKeysFromChannel(channel);
        const matchResult = await findKeyIndexForLog(apiKeys, apiKey, apiKeyHash);
        testingKeyData.keyIndex = matchResult.index;
        const matchedIndex = matchResult.index;
        if (apiKeys.length > 0) {
          updateTestKeyIndexInfo(
            matchResult.index !== null
              ? `匹配到 Key #${matchedIndex + 1}，按日志所用Key测试`
              : '未匹配到日志中的 Key，将按默认顺序测试'
          );
        } else {
          updateTestKeyIndexInfo('未获取到渠道 Key，将按默认顺序测试');
        }

        // 填充模型下拉列表
        const modelSelect = document.getElementById('testKeyModel');
        modelSelect.innerHTML = '';

        if (channel.models && channel.models.length > 0) {
          channel.models.forEach(m => {
            const option = document.createElement('option');
            option.value = m;
            option.textContent = m;
            modelSelect.appendChild(option);
          });

          // 如果日志中的模型在支持列表中，则预选；否则选择第一个
          if (channel.models.includes(model)) {
            modelSelect.value = model;
          } else {
            modelSelect.value = channel.models[0];
          }
        } else {
          // 没有配置模型，使用日志中的模型
          const option = document.createElement('option');
          option.value = model;
          option.textContent = model;
          modelSelect.appendChild(option);
          modelSelect.value = model;
        }
      } catch (e) {
        console.error('加载渠道配置失败', e);
        // 降级方案：使用日志中的模型
        const modelSelect = document.getElementById('testKeyModel');
        modelSelect.innerHTML = '';
        const option = document.createElement('option');
        option.value = model;
        option.textContent = model;
        modelSelect.appendChild(option);
        modelSelect.value = model;
        updateTestKeyIndexInfo('渠道配置加载失败，将按默认顺序测试');
      }
    }

    function closeTestKeyModal() {
      document.getElementById('testKeyModal').classList.remove('show');
      testingKeyData = null;
    }

    function resetTestKeyModal() {
      document.getElementById('testKeyProgress').classList.remove('show');
      document.getElementById('testKeyResult').classList.remove('show', 'success', 'error');
      document.getElementById('runKeyTestBtn').disabled = false;
      document.getElementById('testKeyContent').value = defaultTestContent;
      document.getElementById('testKeyStream').checked = true;
      updateTestKeyIndexInfo('');
      // 重置模型选择框
      const modelSelect = document.getElementById('testKeyModel');
      modelSelect.innerHTML = '<option value="">加载中...</option>';
    }

    async function runKeyTest() {
      if (!testingKeyData) return;

      const modelSelect = document.getElementById('testKeyModel');
      const contentInput = document.getElementById('testKeyContent');
      const streamCheckbox = document.getElementById('testKeyStream');
      const selectedModel = modelSelect.value;
      const testContent = contentInput.value.trim() || defaultTestContent;
      const streamEnabled = streamCheckbox.checked;

      if (!selectedModel) {
        if (window.showError) showError('请选择一个测试模型');
        return;
      }

      // 显示进度
      document.getElementById('testKeyProgress').classList.add('show');
      document.getElementById('testKeyResult').classList.remove('show');
      document.getElementById('runKeyTestBtn').disabled = true;

      try {
        // 构建测试请求（使用用户选择的模型）
        const testRequest = {
          model: selectedModel,
          max_tokens: 512,
          stream: streamEnabled,
          content: testContent,
          channel_type: testingKeyData.channelType || 'anthropic' // ✅ 添加渠道类型
        };
        if (testingKeyData && testingKeyData.keyIndex !== null && testingKeyData.keyIndex !== undefined) {
          testRequest.key_index = testingKeyData.keyIndex;
        }

        const resp = await fetchAPIWithAuth(`/admin/channels/${testingKeyData.channelId}/test`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(testRequest)
        });

        const testResult = resp.data || {};

        displayKeyTestResult(testResult);
      } catch (e) {
        console.error('测试失败', e);
        displayKeyTestResult({
          success: false,
          error: '测试请求失败: ' + e.message
        });
      } finally {
        document.getElementById('testKeyProgress').classList.remove('show');
        document.getElementById('runKeyTestBtn').disabled = false;
      }
    }

    function displayKeyTestResult(result) {
      const testResultDiv = document.getElementById('testKeyResult');
      const contentDiv = document.getElementById('testKeyResultContent');
      const detailsDiv = document.getElementById('testKeyResultDetails');

      testResultDiv.classList.remove('success', 'error');
      testResultDiv.classList.add('show');

      if (result.success) {
        testResultDiv.classList.add('success');
        contentDiv.innerHTML = `
          <div style="display: flex; align-items: center; gap: 8px;">
            <span style="font-size: 18px;">✅</span>
            <strong>${escapeHtml(result.message || 'API测试成功')}</strong>
          </div>
        `;

        let details = `响应时间: ${result.duration_ms}ms`;
        if (result.status_code) {
          details += ` | 状态码: ${result.status_code}`;
        }

        // 显示响应文本
        if (result.response_text) {
          details += `
            <div style="margin-top: 12px;">
              <h4 style="margin-bottom: 8px; color: var(--neutral-700);">API 响应内容</h4>
              <div style="padding: 12px; background: var(--neutral-50); border-radius: 4px; border: 1px solid var(--neutral-200); color: var(--neutral-700); white-space: pre-wrap; font-family: monospace; font-size: 0.9em; max-height: 300px; overflow-y: auto;">${escapeHtml(result.response_text)}</div>
            </div>
          `;
        }

        // 显示完整API响应
        if (result.api_response) {
          const responseId = 'api-response-' + Date.now();
          details += `
            <div style="margin-top: 12px;">
              <h4 style="margin-bottom: 8px; color: var(--neutral-700);">完整 API 响应</h4>
              <button class="btn btn-secondary btn-sm" onclick="toggleResponse('${responseId}')" style="margin-bottom: 8px;">显示/隐藏 JSON</button>
              <div id="${responseId}" style="display: none; padding: 12px; background: var(--neutral-50); border-radius: 4px; border: 1px solid var(--neutral-200); color: var(--neutral-700); white-space: pre-wrap; font-family: monospace; font-size: 0.85em; max-height: 400px; overflow-y: auto;">${escapeHtml(JSON.stringify(result.api_response, null, 2))}</div>
            </div>
          `;
        }

        detailsDiv.innerHTML = details;
      } else {
        testResultDiv.classList.add('error');
        contentDiv.innerHTML = `
          <div style="display: flex; align-items: center; gap: 8px;">
            <span style="font-size: 18px;">❌</span>
            <strong>测试失败</strong>
          </div>
        `;

        let details = `<p style="color: var(--error-600); margin-top: 8px;">${escapeHtml(result.error || '未知错误')}</p>`;

        if (result.status_code) {
          details += `<p style="margin-top: 8px;">状态码: ${result.status_code}</p>`;
        }

        if (result.raw_response) {
          const rawId = 'raw-response-' + Date.now();
          details += `
            <div style="margin-top: 12px;">
              <h4 style="margin-bottom: 8px; color: var(--neutral-700);">原始响应</h4>
              <button class="btn btn-secondary btn-sm" onclick="toggleResponse('${rawId}')" style="margin-bottom: 8px;">显示/隐藏</button>
              <div id="${rawId}" style="display: none; padding: 12px; background: var(--neutral-50); border-radius: 4px; border: 1px solid var(--neutral-200); color: var(--error-700); white-space: pre-wrap; font-family: monospace; font-size: 0.85em; max-height: 400px; overflow-y: auto;">${escapeHtml(result.raw_response)}</div>
            </div>
          `;
        }

        detailsDiv.innerHTML = details;
      }
    }

    function toggleResponse(id) {
      const el = document.getElementById(id);
      if (el) {
        el.style.display = el.style.display === 'none' ? 'block' : 'none';
      }
    }

    // ========== 删除 Key（从日志列表入口） ==========
    async function deleteKeyFromLog(channelId, channelName, maskedApiKey, apiKeyHash) {
      if (!channelId || !maskedApiKey) return;

      const confirmDel = await showConfirm({
        title: '删除确认',
        message: `确定删除渠道"${channelName || ('#' + channelId)}"中的此Key (${maskedApiKey}) 吗？`,
        type: 'danger'
      });
      if (!confirmDel) return;

      try {
        // 获取渠道详情，匹配掩码对应的 key_index
        const channel = await fetchDataWithAuth(`/admin/channels/${channelId}`);

        const apiKeys = parseApiKeysFromChannel(channel);
        const matchResult = await findKeyIndexForLog(apiKeys, maskedApiKey, apiKeyHash);
        const keyIndex = matchResult.index;
        if (keyIndex === null) {
          showAlert('未能匹配到该Key，请检查渠道配置。');
          return;
        }

        // 删除Key
        const delResult = await fetchAPIWithAuth(`/admin/channels/${channelId}/keys/${keyIndex}`, { method: 'DELETE' });
        if (!delResult.success) throw new Error(delResult.error || '删除失败');

        showAlert(`已删除 Key #${keyIndex + 1} (${maskedApiKey})`);

        // 如果没有剩余Key，询问是否删除渠道
        if (delResult.data?.remaining_keys === 0) {
          const delChannel = await showConfirm({
            title: '删除渠道',
            message: '该渠道已无可用Key，是否删除整个渠道？',
            type: 'danger'
          });
          if (delChannel) {
            const chRes = await fetchAPIWithAuth(`/admin/channels/${channelId}`, { method: 'DELETE' });
            if (!chRes.success) throw new Error(chRes.error || '删除渠道失败');
            showAlert('渠道已删除');
          }
        }

        // 刷新日志列表
        load();
      } catch (e) {
        console.error('删除Key失败', e);
        showAlert(e.message || '删除Key失败');
      }
    }

    // ========== SSE 实时日志推送 ==========
    const REALTIME_MODE_KEY = 'logs.realtime_enabled';
    let sseEventSource = null;
    let realtimeModeEnabled = false;
    let realtimeLogCount = 0; // 实时接收的日志计数
    let lastReceivedLogTimeMs = 0; // 最后接收的日志时间戳（毫秒，兼容保留）
    let lastReceivedLogId = 0; // 最后接收的日志 ID（精确游标，推荐使用）
    const displayedLogIds = new Set(); // 已显示的日志ID，用于去重

    // 从日志条目中提取毫秒时间戳
    function extractLogTimeMs(entry) {
      if (!entry) return 0;
      if (entry.time_ms !== undefined && entry.time_ms !== null) return Number(entry.time_ms);
      const t = entry.time;
      if (typeof t === 'number') {
        return t > 1e12 ? t : t * 1000;
      }
      if (typeof t === 'string') {
        if (/^\d+$/.test(t)) {
          const raw = Number(t);
          return raw > 1e12 ? raw : raw * 1000;
        }
        const parsed = Date.parse(t);
        if (!Number.isNaN(parsed)) return parsed;
      }
      return 0;
    }

    // 生成日志去重 key
    // 有 id 时优先用 id（数据库自增唯一），避免 HTTP 秒级 vs SSE 毫秒级时间戳不一致导致重复
    function buildLogKey(entry) {
      if (entry && entry.id) {
        return `id:${entry.id}`;
      }
      const ts = extractLogTimeMs(entry);
      return `ts:${ts}-${(entry && entry.channel_id) || 0}-${(entry && entry.status_code) || 0}`;
    }

    // 从日志列表中同步最后接收状态（用于 SSE 重连恢复）
    function syncLastReceivedFromList(logs) {
      if (!Array.isArray(logs) || logs.length === 0) return;
      let newestMs = lastReceivedLogTimeMs;
      let newestId = lastReceivedLogId;
      for (const entry of logs) {
        const ts = extractLogTimeMs(entry);
        if (ts > newestMs) newestMs = ts;
        const id = Number(entry && entry.id) || 0;
        if (id > newestId) newestId = id;
      }
      if (newestMs > lastReceivedLogTimeMs) {
        lastReceivedLogTimeMs = newestMs;
      }
      if (newestId > lastReceivedLogId) {
        lastReceivedLogId = newestId;
        console.log('[SSE DEBUG] 从日志列表同步 lastReceivedLogId:', newestId);
      }
    }

    function updateRealtimeStatus(text, isConnected) {
      const statusEl = document.getElementById('realtimeStatus');
      const labelEl = document.getElementById('realtimeLabel');
      if (statusEl) {
        statusEl.textContent = text;
        statusEl.style.display = text ? 'inline' : 'none';
        statusEl.style.color = isConnected ? 'var(--success-600)' : 'var(--neutral-500)';
      }
      if (labelEl) {
        labelEl.style.color = isConnected ? 'var(--success-600)' : 'var(--neutral-600)';
      }
    }

    // 显示"有新日志"提示
    function showNewLogsBadge(count) {
      let badge = document.getElementById('newLogsBadge');
      if (!badge) {
        // 创建提示元素
        badge = document.createElement('div');
        badge.id = 'newLogsBadge';
        badge.style.cssText = `
          position: fixed;
          top: 80px;
          right: 20px;
          background: var(--primary-600);
          color: white;
          padding: 12px 20px;
          border-radius: 8px;
          box-shadow: 0 4px 12px rgba(0,0,0,0.15);
          cursor: pointer;
          z-index: 1000;
          font-size: 14px;
          font-weight: 500;
          transition: all 0.3s ease;
        `;
        badge.addEventListener('click', () => {
          currentLogsPage = 1;
          load();
        });
        badge.addEventListener('mouseenter', () => {
          badge.style.transform = 'translateY(-2px)';
          badge.style.boxShadow = '0 6px 16px rgba(0,0,0,0.2)';
        });
        badge.addEventListener('mouseleave', () => {
          badge.style.transform = 'translateY(0)';
          badge.style.boxShadow = '0 4px 12px rgba(0,0,0,0.15)';
        });
        document.body.appendChild(badge);
      }
      badge.textContent = `有 ${count} 条新日志，点击查看`;
      badge.style.display = 'block';
    }

    // 隐藏"有新日志"提示
    function hideNewLogsBadge() {
      const badge = document.getElementById('newLogsBadge');
      if (badge) {
        badge.style.display = 'none';
      }
    }

    function connectSSE() {
      if (sseEventSource) {
        sseEventSource.close();
      }

      // 获取当前的认证 token
      const token = localStorage.getItem('ccload_token');
      if (!token) {
        updateRealtimeStatus('未登录', false);
        return;
      }

      // EventSource 不支持自定义头，使用 URL 参数传递 token
      // 优先使用 since_id（精确游标），否则回退到 since_ms
      let url = `/admin/logs/stream?token=${encodeURIComponent(token)}`;
      if (lastReceivedLogId > 0) {
        url += `&since_id=${lastReceivedLogId}`;
      } else if (lastReceivedLogTimeMs > 0) {
        url += `&since_ms=${lastReceivedLogTimeMs}`;
      }
      console.log('[SSE DEBUG] connectSSE URL:', url);
      sseEventSource = new EventSource(url);
      realtimeLogCount = 0;

      sseEventSource.addEventListener('connected', (e) => {
        console.log('[SSE] 连接成功');
        updateRealtimeStatus('已连接', true);
      });

      sseEventSource.addEventListener('log', (e) => {
        try {
          const entry = JSON.parse(e.data);
          console.log('[SSE DEBUG] 收到日志:', { id: entry.id, time_ms: entry.time_ms, channel_id: entry.channel_id });

          // 获取毫秒时间戳（优先 time_ms，兼容秒级 time）
          const logTimeMs = (() => {
            if (entry.time_ms !== undefined && entry.time_ms !== null) return Number(entry.time_ms);
            if (typeof entry.time === 'number') {
              // 13位视为毫秒，10位视为秒
              return entry.time > 1e12 ? entry.time : entry.time * 1000;
            }
            const parsed = parseInt(entry.time) || 0;
            return parsed > 1e12 ? parsed : parsed * 1000;
          })();

          // 生成日志去重 key（优先 id）
          const logKey = buildLogKey(entry);
          if (displayedLogIds.has(logKey)) {
            // 重复日志，跳过（重连恢复时可能重复）
            return;
          }
          displayedLogIds.add(logKey);

          // 更新最后接收时间（毫秒，用于重连恢复）
          if (logTimeMs > lastReceivedLogTimeMs) {
            lastReceivedLogTimeMs = logTimeMs;
          }
          // 更新最后接收的 ID（精确游标）
          const entryId = Number(entry.id) || 0;
          if (entryId > lastReceivedLogId) {
            lastReceivedLogId = entryId;
          }

          // 插入到实时缓冲区
          realtimeBuffer.unshift(entry);

          // 缓冲区溢出处理：超过最大容量时删除最旧的日志
          if (realtimeBuffer.length > BUFFER_MAX_SIZE) {
            const dropped = realtimeBuffer.pop();
            // 从去重集合中删除被丢弃的日志
            displayedLogIds.delete(buildLogKey(dropped));
          }

          // 更新计数器
          realtimeLogCount++;
          updateRealtimeStatus(`+${realtimeLogCount}`, true);

          // 检查是否符合当前筛选条件
          const matchesFilter = matchesCurrentFilter(entry);

          // 如果在第一页且符合筛选条件，增量插入新行；否则显示"有新日志"提示
          if (currentLogsPage === 1 && matchesFilter) {
            prependRealtimeLog(entry);
            // 增量更新统计计数
            totalLogs++;
            const totalCountEl = document.getElementById('totalCount');
            const displayedCountEl = document.getElementById('displayedCount');
            if (totalCountEl) totalCountEl.textContent = totalLogs;
            if (displayedCountEl) {
              const tbody = document.getElementById('tbody');
              displayedCountEl.textContent = tbody ? tbody.children.length : 0;
            }
          } else if (currentLogsPage !== 1) {
            // 不在第一页时，提示有新日志
            hasNewLogs = true;
            showNewLogsBadge(realtimeLogCount);
          }
          // 注：不符合筛选条件的日志仍保留在 realtimeBuffer 中，清除筛选后可见
        } catch (err) {
          console.error('[SSE] 解析日志失败:', err);
        }
      });

      sseEventSource.addEventListener('close', (e) => {
        console.log('[SSE] 服务器关闭连接');
        updateRealtimeStatus('已断开', false);
        sseEventSource = null;
      });

      sseEventSource.onerror = (e) => {
        console.error('[SSE] 连接错误:', e);
        // 主动清理实例，避免 readyState=CLOSED 却不重连
        if (sseEventSource) {
          try { sseEventSource.close(); } catch (_) {}
          sseEventSource = null;
        }
        updateRealtimeStatus('连接失败', false);
        // 5秒后尝试重连
        if (realtimeModeEnabled) {
          setTimeout(() => {
            if (realtimeModeEnabled && !sseEventSource) {
              connectSSE();
            }
          }, 5000);
        }
      };
    }

    function disconnectSSE() {
      if (sseEventSource) {
        sseEventSource.close();
        sseEventSource = null;
      }
      updateRealtimeStatus('', false);
      realtimeLogCount = 0;
      // 注意：不重置 lastReceivedLogTimeMs，以便重连时恢复错过的日志
    }

    function toggleRealtimeMode(enabled) {
      realtimeModeEnabled = enabled;
      localStorage.setItem(REALTIME_MODE_KEY, enabled ? 'true' : 'false');
      if (enabled) {
        connectSSE();
        startActiveRequestsPolling();
      } else {
        disconnectSSE();
        stopActiveRequestsPolling();
        // 用户主动关闭时重置状态
        lastReceivedLogTimeMs = 0;
        lastReceivedLogId = 0;
        displayedLogIds.clear();
      }
    }


    // 页面可见性监听（后台标签页断开 SSE，节省资源）
    document.addEventListener('visibilitychange', () => {
      console.log('[SSE DEBUG] visibilitychange:', {
        hidden: document.hidden,
        realtimeModeEnabled,
        sseEventSource: sseEventSource ? `readyState=${sseEventSource.readyState}` : 'null',
        lastReceivedLogTimeMs
      });

      if (document.hidden) {
        if (realtimeModeEnabled && sseEventSource) {
          console.log('[SSE DEBUG] 页面隐藏，断开 SSE');
          disconnectSSE();
          stopActiveRequestsPolling();
        }
      } else {
        // 页面重新可见时，检查是否需要重连
        // 除了 sseEventSource 为 null，还需检查 readyState === CLOSED 的情况
        if (realtimeModeEnabled) {
          const needReconnect = !sseEventSource ||
            (sseEventSource.readyState === EventSource.CLOSED);
          console.log('[SSE DEBUG] 页面可见，needReconnect:', needReconnect);
          if (needReconnect) {
            // 丢弃已关闭的实例
            if (sseEventSource && sseEventSource.readyState === EventSource.CLOSED) {
              try { sseEventSource.close(); } catch (_) {}
              sseEventSource = null;
            }
            console.log('[SSE DEBUG] 重新连接 SSE，since_ms:', lastReceivedLogTimeMs);
            connectSSE();
            startActiveRequestsPolling();
          }
        }
      }
    });

    // 初始化实时模式开关
    function initRealtimeToggle() {
      const toggle = document.getElementById('realtimeToggle');
      const saved = localStorage.getItem(REALTIME_MODE_KEY);
      // 默认关闭
      const enabled = saved === 'true';

      if (toggle) {
        toggle.checked = enabled;
        toggle.addEventListener('change', (e) => {
          toggleRealtimeMode(e.target.checked);
        });
      }

      // 根据保存的状态启动
      if (enabled) {
        toggleRealtimeMode(true);
      }
    }
