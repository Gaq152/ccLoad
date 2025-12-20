    const API_BASE = '/admin';
    let allTokens = [];

    // 当前选中的时间范围(默认为本日)
    let currentTimeRange = 'today';

    // 抽屉状态
    let drawerMode = 'create'; // 'create' 或 'edit'
    let editingTokenId = null;

    // 缓存渠道列表
    let allChannels = [];

    document.addEventListener('DOMContentLoaded', () => {
      // 初始化时间范围选择器
      initTimeRangeSelector();

      // 加载令牌列表(默认显示本日统计)
      loadTokens();

      // 初始化事件委托
      initEventDelegation();

      // 抽屉过期时间选择器
      document.getElementById('drawerExpiryType').addEventListener('change', (e) => {
        document.getElementById('drawerCustomExpiryContainer').style.display =
          e.target.value === 'custom' ? 'block' : 'none';
      });
    });

    // 时间范围选择器事件处理
    function initTimeRangeSelector() {
      const buttons = document.querySelectorAll('.time-range-btn');
      buttons.forEach(btn => {
        btn.addEventListener('click', function() {
          // 更新按钮激活状态
          buttons.forEach(b => b.classList.remove('active'));
          this.classList.add('active');

          // 更新当前时间范围并重新加载数据
          currentTimeRange = this.dataset.range;
          loadTokens();
        });
      });
    }

    /**
     * 初始化事件委托(统一处理表格内按钮点击)
     */
    function initEventDelegation() {
      const container = document.getElementById('tokens-container');
      if (!container) return;

      container.addEventListener('click', (e) => {
        const target = e.target;

        // 处理编辑按钮（支持点击SVG图标）
        const editBtn = target.closest('.btn-edit');
        if (editBtn) {
          const row = editBtn.closest('tr');
          const tokenId = row ? parseInt(row.dataset.tokenId) : null;
          if (tokenId) openDrawer('edit', tokenId);
          return;
        }

        // 处理删除按钮（支持点击SVG图标）
        const deleteBtn = target.closest('.btn-delete');
        if (deleteBtn) {
          const row = deleteBtn.closest('tr');
          const tokenId = row ? parseInt(row.dataset.tokenId) : null;
          if (tokenId) deleteToken(tokenId);
          return;
        }

        // 处理禁用/启用按钮
        const toggleBtn = target.closest('.btn-toggle-status');
        if (toggleBtn) {
          const row = toggleBtn.closest('tr');
          const tokenId = row ? parseInt(row.dataset.tokenId) : null;
          const action = toggleBtn.dataset.action;  // 'disable' 或 'enable'
          if (tokenId && action) {
            if (action === 'disable') {
              showDisableConfirmModal(tokenId);
            } else {
              enableToken(tokenId);  // 启用无需确认
            }
          }
          return;
        }
      });
    }

    async function loadTokens() {
      try {
        // 根据currentTimeRange决定是否添加range参数
        let url = `${API_BASE}/auth-tokens`;
        if (currentTimeRange !== 'all') {
          url += `?range=${currentTimeRange}`;
        }

        const response = await fetchWithAuth(url);
        if (!response.ok) throw new Error('加载令牌失败');
        const data = await response.json();
        allTokens = data.data || [];
        renderTokens();
      } catch (error) {
        console.error('加载令牌失败:', error);
        showToast('加载令牌失败: ' + error.message, 'error');
      }
    }

    function renderTokens() {
      const container = document.getElementById('tokens-container');
      const emptyState = document.getElementById('empty-state');

      if (allTokens.length === 0) {
        container.innerHTML = '';
        emptyState.style.display = 'block';
        return;
      }

      emptyState.style.display = 'none';

      // 构建表格结构
      const table = document.createElement('table');
      table.innerHTML = `
        <thead>
          <tr>
            <th>描述</th>
            <th>令牌</th>
            <th style="text-align: center;">调用次数</th>
            <th style="text-align: center;">成功率</th>
            <th style="text-align: center;">Token用量</th>
            <th style="text-align: center;">总费用</th>
            <th style="text-align: center;">流首字平均</th>
            <th style="text-align: center;">非流平均</th>
            <th>最后使用</th>
            <th style="width: 200px;">操作</th>
          </tr>
        </thead>
      `;

      const tbody = document.createElement('tbody');

      // 使用模板引擎渲染行，降级处理
      if (typeof TemplateEngine !== 'undefined') {
        allTokens.forEach(token => {
          const row = createTokenRowWithTemplate(token);
          if (row) tbody.appendChild(row);
        });
      } else {
        // 降级：模板引擎不可用时使用原有方式
        console.warn('[Tokens] TemplateEngine not available, using fallback rendering');
        tbody.innerHTML = allTokens.map(token => createTokenRowFallback(token)).join('');
      }

      table.appendChild(tbody);
      container.innerHTML = '';
      container.appendChild(table);
    }

    // 格式化 Token 数量为 M 单位
    function formatTokenCount(count) {
      if (!count || count === 0) return '0M';
      const millions = count / 1000000;
      return millions.toFixed(2) + 'M';
    }

    /**
     * 使用模板引擎渲染令牌行
     */
    function createTokenRowWithTemplate(token) {
      const status = getTokenStatus(token);
      const createdAt = new Date(token.created_at).toLocaleString('zh-CN');
      const lastUsed = token.last_used_at ? new Date(token.last_used_at).toLocaleString('zh-CN') : '从未使用';
      const expiresAt = token.expires_at ? new Date(token.expires_at).toLocaleString('zh-CN') : '永不过期';

      // 计算统计信息
      const successCount = token.success_count || 0;
      const failureCount = token.failure_count || 0;
      const totalCount = successCount + failureCount;
      const successRate = totalCount > 0 ? ((successCount / totalCount) * 100).toFixed(1) : 0;

      // 预构建各个HTML片段(保留条件逻辑在JS中)
      const callsHtml = buildCallsHtml(successCount, failureCount, totalCount);
      const successRateHtml = buildSuccessRateHtml(successRate, totalCount);
      const tokensHtml = buildTokensHtml(token);
      const costHtml = buildCostHtml(token.total_cost_usd);
      const streamAvgHtml = buildResponseTimeHtml(token.stream_avg_ttfb, token.stream_count);
      const nonStreamAvgHtml = buildResponseTimeHtml(token.non_stream_avg_rt, token.non_stream_count);
      const toggleBtnHtml = buildToggleBtnHtml(token);

      // 使用模板引擎渲染
      return TemplateEngine.render('tpl-token-row', {
        id: token.id,
        description: token.description,
        token: token.token,
        statusClass: status.class,
        createdAt: createdAt,
        expiresAt: expiresAt,
        callsHtml: callsHtml,
        successRateHtml: successRateHtml,
        tokensHtml: tokensHtml,
        costHtml: costHtml,
        streamAvgHtml: streamAvgHtml,
        nonStreamAvgHtml: nonStreamAvgHtml,
        lastUsed: lastUsed,
        toggleBtnHtml: toggleBtnHtml
      });
    }

    /**
     * 构建禁用/启用按钮HTML
     * @param {Object} token - 令牌对象
     * @returns {string} 按钮HTML
     */
    function buildToggleBtnHtml(token) {
      // 暂停图标 (用于禁用按钮)
      const pauseIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="10" y1="15" x2="10" y2="9"></line><line x1="14" y1="15" x2="14" y2="9"></line></svg>';
      // 播放图标 (用于启用按钮)
      const playIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><polygon points="10 8 16 12 10 16 10 8"></polygon></svg>';

      // 过期的令牌：按钮禁用
      if (token.is_expired) {
        return `<button class="btn-action btn-disable" disabled title="令牌已过期，无法操作">${pauseIcon} 禁用</button>`;
      }

      // 根据 is_active 状态显示不同按钮
      if (token.is_active) {
        return `<button class="btn-action btn-disable btn-toggle-status" data-action="disable">${pauseIcon} 禁用</button>`;
      } else {
        return `<button class="btn-action btn-enable btn-toggle-status" data-action="enable">${playIcon} 启用</button>`;
      }
    }

    /**
     * 构建调用次数HTML
     */
    function buildCallsHtml(successCount, failureCount, totalCount) {
      if (totalCount === 0) {
        return '<span style="color: var(--neutral-500); font-size: 13px;">-</span>';
      }

      let html = '<div style="display: flex; flex-direction: column; gap: 4px; align-items: center;">';
      html += `<span class="stats-badge" style="background: var(--success-50); color: var(--success-700); font-weight: 600; border: 1px solid var(--success-200);" title="成功调用">`;
      html += `<span style="color: var(--success-600); font-size: 14px; font-weight: 700;">✓</span> ${successCount.toLocaleString()}`;
      html += `</span>`;

      if (failureCount > 0) {
        html += `<span class="stats-badge" style="background: var(--error-50); color: var(--error-700); font-weight: 600; border: 1px solid var(--error-200);" title="失败调用">`;
        html += `<span style="color: var(--error-600); font-size: 14px; font-weight: 700;">✗</span> ${failureCount.toLocaleString()}`;
        html += `</span>`;
      }

      html += '</div>';
      return html;
    }

    /**
     * 构建成功率HTML
     */
    function buildSuccessRateHtml(successRate, totalCount) {
      if (totalCount === 0) {
        return '<span style="color: var(--neutral-500); font-size: 13px;">-</span>';
      }

      let className = 'stats-badge';
      if (successRate >= 95) className += ' success-rate-high';
      else if (successRate >= 80) className += ' success-rate-medium';
      else className += ' success-rate-low';

      return `<span class="${className}">${successRate}%</span>`;
    }

    /**
     * 构建Token用量HTML
     */
    function buildTokensHtml(token) {
      const hasTokens = token.prompt_tokens_total > 0 ||
                        token.completion_tokens_total > 0 ||
                        token.cache_read_tokens_total > 0 ||
                        token.cache_creation_tokens_total > 0;

      if (!hasTokens) {
        return '<span style="color: var(--neutral-500); font-size: 13px;">-</span>';
      }

      let html = '<div style="display: flex; flex-direction: column; align-items: center; gap: 4px;">';

      // 输入/输出
      html += '<div style="display: inline-flex; gap: 4px; font-size: 12px;">';
      html += `<span class="stats-badge" style="background: var(--primary-50); color: var(--primary-700);" title="输入Tokens">`;
      html += `输入 ${formatTokenCount(token.prompt_tokens_total || 0)}`;
      html += `</span>`;
      html += `<span class="stats-badge" style="background: var(--secondary-50); color: var(--secondary-700);" title="输出Tokens">`;
      html += `输出 ${formatTokenCount(token.completion_tokens_total || 0)}`;
      html += `</span>`;
      html += '</div>';

      // 缓存
      if (token.cache_read_tokens_total > 0 || token.cache_creation_tokens_total > 0) {
        html += '<div style="display: inline-flex; gap: 4px; font-size: 12px;">';

        if (token.cache_read_tokens_total > 0) {
          html += `<span class="stats-badge" style="background: var(--success-50); color: var(--success-700);" title="缓存读Tokens">`;
          html += `缓存读 ${formatTokenCount(token.cache_read_tokens_total || 0)}`;
          html += `</span>`;
        }

        if (token.cache_creation_tokens_total > 0) {
          html += `<span class="stats-badge" style="background: var(--warning-50); color: var(--warning-700);" title="缓存建Tokens">`;
          html += `缓存建 ${formatTokenCount(token.cache_creation_tokens_total || 0)}`;
          html += `</span>`;
        }

        html += '</div>';
      }

      html += '</div>';
      return html;
    }

    /**
     * 构建总费用HTML
     */
    function buildCostHtml(totalCostUsd) {
      if (!totalCostUsd || totalCostUsd <= 0) {
        return '<span style="color: var(--neutral-500); font-size: 13px;">-</span>';
      }

      return `
        <div style="display: flex; flex-direction: column; align-items: center; gap: 2px;">
          <span class="metric-value" style="color: var(--success-700); font-size: 15px; font-weight: 700;">
            $${totalCostUsd.toFixed(4)}
          </span>
        </div>
      `;
    }

    /**
     * 构建响应时间HTML
     */
    function buildResponseTimeHtml(time, count) {
      if (!count || count === 0) {
        return '<span style="color: var(--neutral-500); font-size: 13px;">-</span>';
      }

      const responseClass = getResponseClass(time);
      return `<span class="metric-value ${responseClass}">${time.toFixed(2)}s</span>`;
    }

    /**
     * 获取响应时间颜色等级
     */
    function getResponseClass(time) {
      const num = Number(time);
      if (!Number.isFinite(num) || num <= 0) return '';
      if (num < 3) return 'response-fast';
      if (num < 6) return 'response-medium';
      return 'response-slow';
    }

    /**
     * 降级：模板引擎不可用时的渲染方式
     */
    function createTokenRowFallback(token) {
      const status = getTokenStatus(token);
      const createdAt = new Date(token.created_at).toLocaleString('zh-CN');
      const lastUsed = token.last_used_at ? new Date(token.last_used_at).toLocaleString('zh-CN') : '从未使用';
      const expiresAt = token.expires_at ? new Date(token.expires_at).toLocaleString('zh-CN') : '永不过期';

      // 计算统计信息
      const successCount = token.success_count || 0;
      const failureCount = token.failure_count || 0;
      const totalCount = successCount + failureCount;

      // 预构建HTML片段
      const callsHtml = buildCallsHtml(successCount, failureCount, totalCount);
      const successRate = totalCount > 0 ? ((successCount / totalCount) * 100).toFixed(1) : 0;
      const successRateHtml = buildSuccessRateHtml(successRate, totalCount);
      const tokensHtml = buildTokensHtml(token);
      const costHtml = buildCostHtml(token.total_cost_usd);
      const streamAvgHtml = buildResponseTimeHtml(token.stream_avg_ttfb, token.stream_count);
      const nonStreamAvgHtml = buildResponseTimeHtml(token.non_stream_avg_rt, token.non_stream_count);

      return `
        <tr data-token-id="${token.id}">
          <td style="font-weight: 500;">${escapeHtml(token.description)}</td>
          <td>
            <div><span class="token-display token-display-${status.class}">${escapeHtml(token.token)}</span></div>
            <div style="font-size: 12px; color: var(--neutral-500); margin-top: 4px;">${createdAt}创建 · ${expiresAt}</div>
          </td>
          <td style="text-align: center;">${callsHtml}</td>
          <td style="text-align: center;">${successRateHtml}</td>
          <td style="text-align: center;">${tokensHtml}</td>
          <td style="text-align: center;">${costHtml}</td>
          <td style="text-align: center;">${streamAvgHtml}</td>
          <td style="text-align: center;">${nonStreamAvgHtml}</td>
          <td style="color: var(--neutral-600);">${lastUsed}</td>
          <td>
            <div class="action-btn-group">
              <button class="btn-action btn-edit">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
                编辑
              </button>
              ${buildToggleBtnHtml(token)}
              <button class="btn-action delete btn-delete">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                删除
              </button>
            </div>
          </td>
        </tr>
      `;
    }

    function getTokenStatus(token) {
      if (token.is_expired) return { class: 'expired', text: '已过期' };
      if (!token.is_active) return { class: 'inactive', text: '已禁用' };
      return { class: 'active', text: '正常' };
    }

    // ============================================================================
    // 抽屉面板功能（2025-12 重构）
    // ============================================================================

    /**
     * 打开抽屉面板
     * @param {string} mode - 'create' 或 'edit'
     * @param {number} tokenId - 编辑模式时的令牌ID
     */
    async function openDrawer(mode, tokenId = null) {
      drawerMode = mode;
      editingTokenId = tokenId;

      // 重置表单
      document.getElementById('drawerForm').reset();
      document.getElementById('drawerTokenId').value = '';
      document.getElementById('drawerCustomExpiryContainer').style.display = 'none';

      if (mode === 'create') {
        // 创建模式
        document.getElementById('drawerTitle').textContent = '创建令牌';
        document.getElementById('drawerSaveBtn').textContent = '创建令牌';
        document.getElementById('drawerActive').checked = true;
        document.getElementById('drawerExpiryType').value = 'never';

        // 显示渠道配置部分（创建时也可以配置）
        document.getElementById('drawerChannelSection').style.display = 'block';

        // 默认不允许所有渠道（需要用户手动选择）
        document.getElementById('drawerAllChannels').checked = false;
        document.getElementById('drawerChannelsListWrapper').style.display = 'block';

        await loadDrawerChannelsForCreate();
      } else {
        // 编辑模式
        document.getElementById('drawerTitle').textContent = '编辑令牌';
        document.getElementById('drawerSaveBtn').textContent = '保存配置';

        // 加载令牌数据
        const token = allTokens.find(t => t.id === tokenId);
        if (token) {
          document.getElementById('drawerTokenId').value = tokenId;
          document.getElementById('drawerDescription').value = token.description;
          document.getElementById('drawerActive').checked = token.is_active;

          // 设置过期时间
          if (!token.expires_at) {
            document.getElementById('drawerExpiryType').value = 'never';
          } else {
            document.getElementById('drawerExpiryType').value = 'custom';
            document.getElementById('drawerCustomExpiryContainer').style.display = 'block';
            const date = new Date(token.expires_at);
            document.getElementById('drawerCustomExpiry').value = date.toISOString().slice(0, 16);
          }
        }

        // 显示渠道配置部分
        document.getElementById('drawerChannelSection').style.display = 'block';
        await loadDrawerChannels(tokenId);
      }

      // 显示遮罩层和抽屉
      document.getElementById('drawerOverlay').classList.add('show');
      document.getElementById('configDrawer').classList.add('open');
    }

    /**
     * 关闭抽屉面板
     */
    function closeDrawer() {
      document.getElementById('drawerOverlay').classList.remove('show');
      document.getElementById('configDrawer').classList.remove('open');
      drawerMode = 'create';
      editingTokenId = null;
    }

    /**
     * 保存抽屉数据
     */
    async function saveDrawerData() {
      const description = document.getElementById('drawerDescription').value.trim();
      if (!description) {
        showToast('请输入描述', 'error');
        return;
      }

      // 解析过期时间
      const expiryType = document.getElementById('drawerExpiryType').value;
      let expiresAt = null;
      if (expiryType !== 'never') {
        if (expiryType === 'custom') {
          const customDate = document.getElementById('drawerCustomExpiry').value;
          if (!customDate) {
            showToast('请选择过期时间', 'error');
            return;
          }
          expiresAt = new Date(customDate).getTime();
        } else {
          const days = parseInt(expiryType);
          expiresAt = Date.now() + days * 24 * 60 * 60 * 1000;
        }
      }

      const isActive = document.getElementById('drawerActive').checked;

      try {
        if (drawerMode === 'create') {
          // 创建令牌
          const response = await fetchWithAuth(`${API_BASE}/auth-tokens`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ description, expires_at: expiresAt, is_active: isActive })
          });
          if (!response.ok) throw new Error('创建失败');
          const data = await response.json();
          const newTokenId = data.data.id;

          // 保存渠道配置
          await saveDrawerChannels(newTokenId);

          closeDrawer();
          // 显示新令牌
          document.getElementById('newTokenValue').value = data.data.token;
          document.getElementById('tokenResultModal').style.display = 'block';
          loadTokens();
          showToast('令牌创建成功', 'success');
        } else {
          // 更新令牌
          const tokenId = editingTokenId;
          const response = await fetchWithAuth(`${API_BASE}/auth-tokens/${tokenId}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ description, is_active: isActive, expires_at: expiresAt })
          });
          if (!response.ok) throw new Error('更新失败');

          // 保存渠道配置
          await saveDrawerChannels(tokenId);

          closeDrawer();
          loadTokens();
          showToast('更新成功', 'success');
        }
      } catch (error) {
        console.error('保存失败:', error);
        showToast('保存失败: ' + error.message, 'error');
      }
    }

    /**
     * 加载抽屉中的渠道配置（编辑模式）
     */
    async function loadDrawerChannels(tokenId) {
      const listContainer = document.getElementById('drawerChannelsList');
      listContainer.innerHTML = '<div class="channels-loading">加载中...</div>';
      updateDrawerChannelCount(0);

      try {
        // 并行加载渠道列表和令牌配置
        const [channelsRes, configRes] = await Promise.all([
          fetchWithAuth(`${API_BASE}/channels`),
          fetchWithAuth(`${API_BASE}/auth-tokens/${tokenId}/channels`)
        ]);

        if (!channelsRes.ok) throw new Error('加载渠道列表失败');
        if (!configRes.ok) throw new Error('加载令牌渠道配置失败');

        const channelsData = await channelsRes.json();
        const configData = await configRes.json();

        allChannels = channelsData.data || [];
        const config = configData.data || {};

        // 设置全部渠道开关
        const allChannelsToggle = document.getElementById('drawerAllChannels');
        allChannelsToggle.checked = config.all_channels !== false;

        // 渲染渠道列表
        renderDrawerChannelsList(config.channel_ids || []);

        // 根据开关状态显示/隐藏渠道列表
        toggleDrawerChannelsList();
      } catch (error) {
        console.error('加载渠道配置失败:', error);
        listContainer.innerHTML = `<div class="channels-error">加载失败: ${error.message}</div>`;
      }
    }

    /**
     * 加载抽屉中的渠道配置（创建模式）
     * 默认：不允许所有渠道，需要用户手动选择
     */
    async function loadDrawerChannelsForCreate() {
      const listContainer = document.getElementById('drawerChannelsList');
      listContainer.innerHTML = '<div class="channels-loading">加载中...</div>';
      updateDrawerChannelCount(0);

      try {
        const channelsRes = await fetchWithAuth(`${API_BASE}/channels`);
        if (!channelsRes.ok) throw new Error('加载渠道列表失败');

        const channelsData = await channelsRes.json();
        allChannels = channelsData.data || [];

        // 渲染渠道列表（无选中项，需要用户手动选择）
        renderDrawerChannelsList([]);
      } catch (error) {
        console.error('加载渠道列表失败:', error);
        listContainer.innerHTML = `<div class="channels-error">加载失败: ${error.message}</div>`;
      }
    }

    /**
     * 渲染抽屉中的渠道列表
     */
    function renderDrawerChannelsList(selectedIds) {
      const container = document.getElementById('drawerChannelsList');

      if (allChannels.length === 0) {
        container.innerHTML = '<div class="channels-empty">暂无渠道</div>';
        updateDrawerChannelCount(0);
        return;
      }

      const selectedSet = new Set(selectedIds);
      let html = '';

      allChannels.forEach(channel => {
        const isChecked = selectedSet.has(channel.id);
        const statusClass = channel.enabled ? 'enabled' : 'disabled';
        const statusText = channel.enabled ? '启用' : '禁用';
        const selectedClass = isChecked ? ' selected' : '';

        html += `
          <label class="channel-item${selectedClass}" data-channel-id="${channel.id}">
            <span class="channel-checkbox">
              <input type="checkbox" value="${channel.id}" ${isChecked ? 'checked' : ''} onchange="updateDrawerChannelSelection(this)">
              <span class="checkmark"></span>
            </span>
            <span class="channel-info">
              <span class="channel-name">${escapeHtml(channel.name)}</span>
              <span class="channel-status ${statusClass}">${statusText}</span>
            </span>
            <span class="channel-type">${channel.channel_type || 'anthropic'}</span>
          </label>
        `;
      });

      container.innerHTML = html;
      updateDrawerChannelCount(selectedIds.length);
    }

    /**
     * 切换抽屉渠道列表显示
     */
    function toggleDrawerChannelsList() {
      const allChannelsChecked = document.getElementById('drawerAllChannels').checked;
      const wrapper = document.getElementById('drawerChannelsListWrapper');
      wrapper.style.display = allChannelsChecked ? 'none' : 'block';
    }

    /**
     * 更新抽屉渠道选择状态
     */
    function updateDrawerChannelSelection(checkbox) {
      const channelItem = checkbox.closest('.channel-item');
      if (checkbox.checked) {
        channelItem.classList.add('selected');
      } else {
        channelItem.classList.remove('selected');
      }
      // 更新计数
      const checkedCount = document.querySelectorAll('#drawerChannelsList input[type="checkbox"]:checked').length;
      updateDrawerChannelCount(checkedCount);
    }

    /**
     * 更新抽屉渠道数量徽章
     */
    function updateDrawerChannelCount(count) {
      const badge = document.getElementById('drawerChannelCount');
      if (badge) badge.textContent = count;
    }

    /**
     * 保存抽屉中的渠道配置
     */
    async function saveDrawerChannels(tokenId) {
      const allChannelsChecked = document.getElementById('drawerAllChannels').checked;
      const checkboxes = document.querySelectorAll('#drawerChannelsList input[type="checkbox"]:checked');
      const channelIds = Array.from(checkboxes).map(cb => parseInt(cb.value));

      const response = await fetchWithAuth(`${API_BASE}/auth-tokens/${tokenId}/channels`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          all_channels: allChannelsChecked,
          channel_ids: channelIds
        })
      });

      if (!response.ok) throw new Error('保存渠道配置失败');
    }

    /**
     * 显示创建令牌抽屉
     */
    function showCreateModal() {
      openDrawer('create');
    }

    function copyToken() {
      const textarea = document.getElementById('newTokenValue');
      textarea.select();
      document.execCommand('copy');
      showToast('已复制到剪贴板', 'success');
    }

    function closeTokenResultModal() {
      document.getElementById('tokenResultModal').style.display = 'none';
      document.getElementById('newTokenValue').value = '';
    }

    // 待删除的令牌ID
    let deletingTokenId = null;

    /**
     * 显示删除确认对话框
     */
    function deleteToken(id) {
      deletingTokenId = id;
      const modal = document.getElementById('deleteConfirmModal');
      requestAnimationFrame(() => {
        modal.classList.add('active');
      });
    }

    /**
     * 关闭删除确认对话框
     */
    function closeDeleteConfirmModal() {
      const modal = document.getElementById('deleteConfirmModal');
      modal.classList.remove('active');
      deletingTokenId = null;
    }

    /**
     * 确认删除令牌
     */
    async function confirmDeleteToken() {
      if (!deletingTokenId) return;

      try {
        const response = await fetchWithAuth(`${API_BASE}/auth-tokens/${deletingTokenId}`, {
          method: 'DELETE'
        });
        if (!response.ok) throw new Error('删除失败');
        closeDeleteConfirmModal();
        loadTokens();
        showToast('删除成功', 'success');
      } catch (error) {
        console.error('删除失败:', error);
        showToast('删除失败: ' + error.message, 'error');
      }
    }

    // ============================================================================
    // 禁用/启用令牌功能（2025-12新增）
    // ============================================================================

    // 待禁用的令牌ID
    let disablingTokenId = null;

    /**
     * 显示禁用确认对话框
     */
    function showDisableConfirmModal(id) {
      disablingTokenId = id;

      // 修改删除确认对话框的文案为禁用文案
      const titleEl = document.querySelector('#deleteConfirmModal .delete-modal-title');
      const descEl = document.querySelector('#deleteConfirmModal .delete-modal-desc');
      const confirmBtn = document.querySelector('#deleteConfirmModal .btn-danger');

      if (titleEl) titleEl.textContent = '禁用 API 令牌';
      if (descEl) descEl.textContent = '确定要禁用此令牌吗？禁用后所有使用此令牌的请求将被拒绝（返回 403 错误）。您可以随时重新启用。';
      if (confirmBtn) {
        confirmBtn.textContent = '确认禁用';
        confirmBtn.onclick = confirmDisableToken;
      }

      const modal = document.getElementById('deleteConfirmModal');
      requestAnimationFrame(() => {
        modal.classList.add('active');
      });
    }

    /**
     * 关闭禁用确认对话框并恢复删除对话框的原始状态
     */
    function closeDisableConfirmModal() {
      const modal = document.getElementById('deleteConfirmModal');
      modal.classList.remove('active');
      disablingTokenId = null;

      // 恢复删除对话框的原始文案
      const titleEl = document.querySelector('#deleteConfirmModal .delete-modal-title');
      const descEl = document.querySelector('#deleteConfirmModal .delete-modal-desc');
      const confirmBtn = document.querySelector('#deleteConfirmModal .btn-danger');

      if (titleEl) titleEl.textContent = '删除 API 令牌';
      if (descEl) descEl.textContent = '确定要删除此令牌吗？此操作无法撤销，删除后使用此令牌的所有请求将失败。';
      if (confirmBtn) {
        confirmBtn.textContent = '确认删除';
        confirmBtn.onclick = confirmDeleteToken;
      }
    }

    /**
     * 确认禁用令牌
     */
    async function confirmDisableToken() {
      if (!disablingTokenId) return;

      try {
        const response = await fetchWithAuth(`${API_BASE}/auth-tokens/${disablingTokenId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ is_active: false })
        });
        if (!response.ok) throw new Error('禁用失败');
        closeDisableConfirmModal();
        loadTokens();
        showToast('令牌已禁用', 'success');
      } catch (error) {
        console.error('禁用失败:', error);
        showToast('禁用失败: ' + error.message, 'error');
      }
    }

    /**
     * 启用令牌（无需确认）
     */
    async function enableToken(id) {
      try {
        const response = await fetchWithAuth(`${API_BASE}/auth-tokens/${id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ is_active: true })
        });
        if (!response.ok) throw new Error('启用失败');
        loadTokens();
        showToast('令牌已启用', 'success');
      } catch (error) {
        console.error('启用失败:', error);
        showToast('启用失败: ' + error.message, 'error');
      }
    }

    function showToast(message, type = 'info') {
      const toast = document.createElement('div');
      toast.className = `toast toast-${type}`;
      toast.textContent = message;
      toast.style.cssText = `
        position: fixed; top: 20px; right: 20px; padding: 12px 20px;
        background: ${type === 'success' ? 'var(--success-500)' : type === 'error' ? 'var(--error-500)' : 'var(--primary-500)'};
        color: white; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.15);
        z-index: 10000; animation: slideIn 0.3s ease-out;
      `;
      document.body.appendChild(toast);
      setTimeout(() => {
        toast.style.animation = 'slideOut 0.3s ease-out';
        setTimeout(() => toast.remove(), 300);
      }, 3000);
    }

    // 初始化顶部导航栏
    document.addEventListener('DOMContentLoaded', () => {
      initTopbar('tokens');
    });
