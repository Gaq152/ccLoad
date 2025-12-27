function inlineCooldownBadge(c) {
  const ms = c.cooldown_remaining_ms || 0;
  if (!ms || ms <= 0) return '';
  const text = humanizeMS(ms);
  return ` <span style="color: var(--theme-badge-error-text); font-size: 11px; font-weight: 600; background: var(--theme-badge-error-bg-gradient); padding: 2px 6px; border-radius: 4px; border: 1px solid var(--theme-badge-error-border); font-family: monospace;">⏱${text}</span>`;
}

function buildLatencyBadge(latencyMs, statusCode) {
  if (latencyMs === null || latencyMs === undefined) return '';

  let color, bgColor, borderColor, text;
  if (latencyMs < 0) {
    // 超时/失败
    color = 'var(--theme-badge-error-text)';
    bgColor = 'var(--theme-badge-error-bg)';
    borderColor = 'var(--theme-badge-error-border)';
    text = '超时';
  } else if (latencyMs < 500) {
    color = 'var(--theme-badge-success-text)';
    bgColor = 'var(--theme-badge-success-bg)';
    borderColor = 'var(--theme-badge-success-border)';
    text = `${latencyMs}ms`;
  } else if (latencyMs < 1000) {
    color = 'var(--theme-badge-warning-text)';
    bgColor = 'var(--theme-badge-warning-bg)';
    borderColor = 'var(--theme-badge-warning-border)';
    text = `${latencyMs}ms`;
  } else {
    color = 'var(--theme-badge-error-text)';
    bgColor = 'var(--theme-badge-error-bg)';
    borderColor = 'var(--theme-badge-error-border)';
    text = `${latencyMs}ms`;
  }

  return ` <span style="color: ${color}; font-size: 0.75rem; font-weight: 500; background: ${bgColor}; padding: 1px 6px; border-radius: 4px; border: 1px solid ${borderColor}; margin-left: 4px;">${text}</span>`;
}

/**
 * 更新渠道卡片上的延迟勋章（测速完成后调用）
 * @param {number} channelId - 渠道ID
 * @param {Array} endpoints - 端点列表（包含测速结果）
 */
function updateChannelLatencyBadge(channelId, endpoints) {
  if (!channelId || !endpoints || endpoints.length === 0) return;

  // 找到激活的端点
  const activeEndpoint = endpoints.find(ep => ep.is_active);
  if (!activeEndpoint) return;

  const latencyMs = activeEndpoint.latency_ms;
  const statusCode = activeEndpoint.status_code;

  // 更新 channels 数组中的数据
  const channel = channels.find(c => c.id === channelId);
  if (channel) {
    channel.active_endpoint_latency = latencyMs;
    channel.active_endpoint_status = statusCode;
  }

  // 更新 DOM 中的延迟勋章
  const badgeContainer = document.querySelector(`#channel-${channelId} .latency-badge-container`);
  if (badgeContainer) {
    badgeContainer.innerHTML = buildLatencyBadge(latencyMs, statusCode);
  }
}

function renderChannelStatsInline(stats, cache, channelType) {
  if (!stats) {
    return `<span class="channel-stat-badge" style="margin-left: 6px; color: var(--neutral-500);">统计: --</span>`;
  }

  // 如果没有配置任何字段，返回空
  if (!channelStatsFields || channelStatsFields.length === 0) {
    return '';
  }

  const successRateText = cache?.successRateText || formatSuccessRate(stats.success, stats.total);
  const avgFirstByteText = cache?.avgFirstByteText || formatAvgFirstByte(stats.avgFirstByteTimeSeconds);
  const inputTokensText = cache?.inputTokensText || formatMetricNumber(stats.totalInputTokens);
  const outputTokensText = cache?.outputTokensText || formatMetricNumber(stats.totalOutputTokens);
  const cacheReadText = cache?.cacheReadText || formatMetricNumber(stats.totalCacheReadInputTokens);
  const cacheCreationText = cache?.cacheCreationText || formatMetricNumber(stats.totalCacheCreationInputTokens);
  const costDisplay = cache?.costDisplay || formatCostValue(stats.totalCost);

  const successRateColor = (() => {
    const rateNum = Number(successRateText.replace('%', ''));
    if (!Number.isFinite(rateNum)) return 'var(--neutral-600)';
    if (rateNum >= 95) return 'var(--success-600)';
    if (rateNum < 80) return 'var(--error-500)';
    return 'var(--warning-600)';
  })();

  const callText = `${formatMetricNumber(stats.success)}/${formatMetricNumber(stats.error)}`;
  const rangeLabel = getStatsRangeLabel(channelStatsRange);
  const supportsCaching = channelType === 'anthropic' || channelType === 'codex';

  // 根据配置构建显示字段
  const parts = [];

  if (channelStatsFields.includes('calls')) {
    parts.push(`<span class="channel-stat-badge" style="color: var(--neutral-800);"><strong>${rangeLabel}调用</strong> ${callText}</span>`);
  }
  if (channelStatsFields.includes('rate')) {
    parts.push(`<span class="channel-stat-badge" style="color: ${successRateColor};"><strong>率</strong> ${successRateText}</span>`);
  }
  if (channelStatsFields.includes('first_byte')) {
    parts.push(`<span class="channel-stat-badge" style="color: var(--primary-700);"><strong>首字</strong> ${avgFirstByteText}</span>`);
  }
  if (channelStatsFields.includes('input')) {
    parts.push(`<span class="channel-stat-badge" style="color: var(--neutral-800);"><strong>In</strong> ${inputTokensText}</span>`);
  }
  if (channelStatsFields.includes('output')) {
    parts.push(`<span class="channel-stat-badge" style="color: var(--neutral-800);"><strong>Out</strong> ${outputTokensText}</span>`);
  }
  if (channelStatsFields.includes('cache_read') && supportsCaching) {
    parts.push(`<span class="channel-stat-badge" style="color: var(--success-600); background: var(--success-50); border-color: var(--success-100);"><strong>缓存读</strong> ${cacheReadText}</span>`);
  }
  if (channelStatsFields.includes('cache_creation') && supportsCaching) {
    parts.push(`<span class="channel-stat-badge" style="color: var(--primary-700); background: var(--primary-50); border-color: var(--primary-100);"><strong>缓存建</strong> ${cacheCreationText}</span>`);
  }
  if (channelStatsFields.includes('cost')) {
    parts.push(`<span class="channel-stat-badge" style="color: var(--warning-700); background: var(--warning-50); border-color: var(--warning-100);"><strong>成本</strong> ${costDisplay}</span>`);
  }

  return parts.join(' ');
}

/**
 * 获取渠道类型配置信息
 * @param {string} channelType - 渠道类型
 * @returns {Object} 类型配置
 */
function getChannelTypeConfig(channelType) {
  const configs = {
    'anthropic': {
      text: 'Claude',
      color: 'var(--theme-badge-purple-text)',
      bgColor: 'var(--theme-badge-purple-bg)',
      borderColor: 'var(--theme-badge-purple-border)'
    },
    'codex': {
      text: 'Codex',
      color: 'var(--theme-badge-success-text)',
      bgColor: 'var(--theme-badge-success-bg)',
      borderColor: 'var(--theme-badge-success-border)'
    },
    'gemini': {
      text: 'Gemini',
      color: 'var(--theme-badge-info-text)',
      bgColor: 'var(--theme-badge-info-bg)',
      borderColor: 'var(--theme-badge-info-border)'
    }
  };
  const type = (channelType || '').toLowerCase();
  return configs[type] || configs['anthropic'];
}

/**
 * 生成渠道类型徽章HTML
 * @param {string} channelType - 渠道类型
 * @returns {string} 徽章HTML
 */
function buildChannelTypeBadge(channelType) {
  const config = getChannelTypeConfig(channelType);
  return `<span style="background: ${config.bgColor}; color: ${config.color}; padding: 3px 10px; border-radius: 6px; font-size: 0.75rem; font-weight: 700; margin-left: 8px; border: 1.5px solid ${config.borderColor}; letter-spacing: 0.025em; text-transform: uppercase;">${config.text}</span>`;
}

/**
 * 使用模板引擎创建渠道卡片元素
 * @param {Object} channel - 渠道数据
 * @returns {HTMLElement|null} 卡片元素
 */
function createChannelCard(channel) {
  const isCooldown = channel.cooldown_remaining_ms > 0;
  const cardClasses = ['glass-card'];
  if (isCooldown) cardClasses.push('channel-card-cooldown');
  if (!channel.enabled) cardClasses.push('channel-disabled');

  const channelTypeRaw = (channel.channel_type || '').toLowerCase();
  const stats = channelStatsById[channel.id] || null;

  // 预计算统计数据
  const statsCache = stats ? {
    successRateText: formatSuccessRate(stats.success, stats.total),
    avgFirstByteText: formatAvgFirstByte(stats.avgFirstByteTimeSeconds),
    inputTokensText: formatMetricNumber(stats.totalInputTokens),
    outputTokensText: formatMetricNumber(stats.totalOutputTokens),
    cacheReadText: formatMetricNumber(stats.totalCacheReadInputTokens),
    cacheCreationText: formatMetricNumber(stats.totalCacheCreationInputTokens),
    costDisplay: formatCostValue(stats.totalCost)
  } : null;

  const statsHtml = stats && statsCache
    ? `<span class="channel-stats-inline">${renderChannelStatsInline(stats, statsCache, channelTypeRaw)}</span>`
    : '';

  const modelsText = Array.isArray(channel.models) ? channel.models.join(', ') : '';

  // 准备模板数据
  const cardData = {
    cardClasses: cardClasses.join(' '),
    id: channel.id,
    name: channel.name,
    typeBadge: buildChannelTypeBadge(channelTypeRaw),
    modelsText: modelsText,
    url: channel.url,
    latencyBadge: buildLatencyBadge(channel.active_endpoint_latency, channel.active_endpoint_status),
    priority: channel.priority,
    statusText: channel.enabled ? '已启用' : '已禁用',
    statusClass: channel.enabled ? 'status-enabled' : 'status-disabled',
    cooldownBadge: inlineCooldownBadge(channel),
    statsHtml: statsHtml,
    enabled: channel.enabled,
    toggleText: channel.enabled ? '禁用' : '启用',
    toggleTitle: channel.enabled ? '禁用渠道' : '启用渠道',
    // 禁用/启用图标：启用状态显示暂停图标，禁用状态显示播放图标
    toggleIcon: channel.enabled
      ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><rect x="6" y="4" width="4" height="16"></rect><rect x="14" y="4" width="4" height="16"></rect></svg>'
      : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><polygon points="5 3 19 12 5 21 5 3"></polygon></svg>'
  };

  // 使用模板引擎渲染
  const card = TemplateEngine.render('tpl-channel-card', cardData);
  return card;
}

/**
 * 初始化渠道卡片事件委托 (替代inline onclick)
 */
function initChannelEventDelegation() {
  const container = document.getElementById('channels-container');
  if (!container || container.dataset.delegated) return;

  container.dataset.delegated = 'true';

  // 事件委托：处理所有渠道操作按钮
  container.addEventListener('click', (e) => {
    // 排序模式下阻止所有操作按钮
    if (sortModeEnabled) {
      const isActionBtn = e.target.closest('.channel-action-btn') || e.target.closest('.endpoint-manage-btn');
      if (isActionBtn) {
        e.preventDefault();
        e.stopPropagation();
        showToast('排序模式下请先保存或取消排序', 'warning');
        return;
      }
    }

    // 端点管理按钮
    const endpointBtn = e.target.closest('.endpoint-manage-btn');
    if (endpointBtn) {
      const channelId = parseInt(endpointBtn.dataset.channelId);
      if (typeof openEndpointModal === 'function') {
        openEndpointModal(channelId);
      }
      return;
    }

    const btn = e.target.closest('.channel-action-btn');
    if (!btn) return;

    const action = btn.dataset.action;
    const channelId = parseInt(btn.dataset.channelId);
    const channelName = btn.dataset.channelName;
    const enabled = btn.dataset.enabled === 'true';

    switch (action) {
      case 'edit':
        editChannel(channelId);
        break;
      case 'test':
        testChannel(channelId, channelName);
        break;
      case 'toggle':
        toggleChannel(channelId, !enabled);
        break;
      case 'copy':
        copyChannel(channelId, channelName);
        break;
      case 'delete':
        deleteChannel(channelId, channelName);
        break;
    }
  });
}

// 渠道分组折叠状态（记忆用户操作）
const channelGroupCollapsed = {};

// 优先级泳道折叠状态（key: "type-priority"）
const priorityLaneCollapsed = {};

// ==================== 排序模式状态 ====================
let sortModeEnabled = false;           // 是否处于排序模式
let pendingSortChanges = [];           // 待保存的排序更改
let originalChannelsSnapshot = null;   // 进入排序模式时的原始数据快照

/**
 * 进入排序模式
 */
function enterSortMode() {
  sortModeEnabled = true;
  pendingSortChanges = [];
  // 保存原始数据快照（深拷贝）
  originalChannelsSnapshot = JSON.parse(JSON.stringify(channels));

  // 更新UI
  document.body.classList.add('sort-mode-active');
  renderChannels();
  showToast('已进入排序模式，拖拽调整后点击保存', 'info');
}

/**
 * 退出排序模式（不保存）
 */
function exitSortMode() {
  sortModeEnabled = false;

  // 恢复原始数据
  if (originalChannelsSnapshot) {
    channels.length = 0;
    channels.push(...originalChannelsSnapshot);
  }

  pendingSortChanges = [];
  originalChannelsSnapshot = null;

  // 更新UI
  document.body.classList.remove('sort-mode-active');
  renderChannels();
  showToast('已取消排序', 'info');
}

/**
 * 保存排序更改
 */
async function saveSortChanges() {
  if (pendingSortChanges.length === 0) {
    showToast('没有需要保存的更改', 'info');
    exitSortMode();
    return;
  }

  try {
    await saveChannelOrder(pendingSortChanges);

    // 保存成功，退出排序模式
    sortModeEnabled = false;
    pendingSortChanges = [];
    originalChannelsSnapshot = null;

    document.body.classList.remove('sort-mode-active');
    renderChannels();
    showToast('排序已保存', 'success');
  } catch (err) {
    console.error('保存排序失败', err);
    showToast('保存失败: ' + err.message, 'error');
  }
}

/**
 * 记录排序更改（排序模式下使用）
 */
function recordSortChange(updates) {
  // 合并更新：相同ID的更新只保留最新的
  updates.forEach(upd => {
    const existingIdx = pendingSortChanges.findIndex(p => p.id === upd.id);
    if (existingIdx >= 0) {
      pendingSortChanges[existingIdx] = upd;
    } else {
      pendingSortChanges.push(upd);
    }
  });

  // 更新工具栏显示
  updateSortModeChangesCount();
}

/**
 * 更新排序模式工具栏的更改计数
 */
function updateSortModeChangesCount() {
  const countEl = document.getElementById('sortModeChanges');
  if (countEl) {
    if (pendingSortChanges.length > 0) {
      countEl.textContent = `(${pendingSortChanges.length} 项待保存)`;
    } else {
      countEl.textContent = '';
    }
  }
}

/**
 * 切换渠道分组折叠状态
 */
function toggleChannelGroup(type) {
  const group = document.querySelector(`.channel-type-group[data-type="${type}"]`);
  if (group) {
    group.classList.toggle('is-collapsed');
    channelGroupCollapsed[type] = group.classList.contains('is-collapsed');
  }
}

/**
 * 切换优先级泳道折叠状态
 */
function togglePriorityLane(type, priority) {
  const lane = document.querySelector(`.priority-group[data-type="${type}"][data-priority="${priority}"]`);
  if (lane) {
    lane.classList.toggle('is-collapsed');
    priorityLaneCollapsed[`${type}-${priority}`] = lane.classList.contains('is-collapsed');
  }
}

/**
 * 展开优先级泳道（拖拽时自动展开用）
 */
function expandPriorityLane(lane) {
  if (lane && lane.classList.contains('is-collapsed')) {
    const type = lane.dataset.type;
    const priority = lane.dataset.priority;
    lane.classList.remove('is-collapsed');
    priorityLaneCollapsed[`${type}-${priority}`] = false;
  }
}

/**
 * 渲染渠道列表（按类型→优先级嵌套分组，支持拖拽排序）
 */
function renderChannels(channelsToRender = channels) {
  const el = document.getElementById('channels-container');
  if (!channelsToRender || channelsToRender.length === 0) {
    el.innerHTML = '<div class="glass-card">暂无符合条件的渠道</div>';
    return;
  }

  // 初始化事件委托（仅一次）
  initChannelEventDelegation();

  // 1. 按 channel_type 分组
  const typeGroups = {};
  channelsToRender.forEach(channel => {
    const type = (channel.channel_type || 'anthropic').toLowerCase();
    if (!typeGroups[type]) {
      typeGroups[type] = [];
    }
    typeGroups[type].push(channel);
  });

  // 类型显示名称和排序优先级
  const typeConfig = {
    'anthropic': { name: 'Anthropic (Claude)', order: 1 },
    'codex': { name: 'Codex', order: 2 },
    'gemini': { name: 'Gemini', order: 3 }
  };

  // 按优先级排序类型
  const sortedTypes = Object.keys(typeGroups).sort((a, b) => {
    const orderA = typeConfig[a]?.order || 99;
    const orderB = typeConfig[b]?.order || 99;
    return orderA - orderB;
  });

  // 使用 DocumentFragment 优化
  const fragment = document.createDocumentFragment();

  sortedTypes.forEach(type => {
    const channelsInType = typeGroups[type];
    const config = typeConfig[type] || { name: type.toUpperCase(), order: 99 };
    const enabledCount = channelsInType.filter(c => c.enabled).length;
    const isCollapsed = channelGroupCollapsed[type] || false;

    // 创建类型分组容器
    const groupDiv = document.createElement('div');
    groupDiv.className = `channel-type-group${isCollapsed ? ' is-collapsed' : ''}`;
    groupDiv.dataset.type = type;

    // 类型头部
    const header = document.createElement('div');
    header.className = 'channel-group-header';
    header.onclick = () => toggleChannelGroup(type);
    header.innerHTML = `
      <div class="channel-group-left">
        <span class="channel-group-toggle">▼</span>
        <span class="channel-group-badge ${type}">${type.toUpperCase()}</span>
        <span class="channel-group-title">${config.name}</span>
      </div>
      <div class="channel-group-stats">
        <span class="channel-group-count">${enabledCount}/${channelsInType.length} 启用</span>
      </div>
    `;

    // 类型内容区
    const content = document.createElement('div');
    content.className = 'channel-group-content';

    // 排序模式：按优先级分组显示泳道
    // 普通模式：简单列表显示
    if (sortModeEnabled) {
      // ===== 排序模式：优先级泳道布局 =====
      // 2. 在类型内按 Priority 分组
      const priorityGroups = {};
      channelsInType.forEach(ch => {
        const p = ch.priority !== undefined ? ch.priority : 0;
        if (!priorityGroups[p]) priorityGroups[p] = [];
        priorityGroups[p].push(ch);
      });

      // 按优先级降序排列（高优先级在上）
      const sortedPriorities = Object.keys(priorityGroups)
        .map(Number)
        .sort((a, b) => b - a);

      sortedPriorities.forEach(priority => {
        const channelsInPriority = priorityGroups[priority];

        // 3. 组内按 sort_order 排序
        channelsInPriority.sort((a, b) => (a.sort_order || 0) - (b.sort_order || 0));

        // 检查折叠状态
        const laneKey = `${type}-${priority}`;
        const isLaneCollapsed = priorityLaneCollapsed[laneKey] || false;

        // 创建优先级泳道
        const priorityLane = document.createElement('div');
        priorityLane.className = `priority-group${isLaneCollapsed ? ' is-collapsed' : ''}`;
        priorityLane.dataset.priority = priority;
        priorityLane.dataset.type = type;

        // 添加拖拽事件监听
        priorityLane.addEventListener('dragover', handleDragOver);
        priorityLane.addEventListener('drop', (e) => handleDrop(e, type, priority));
        priorityLane.addEventListener('dragenter', handleDragEnter);
        priorityLane.addEventListener('dragleave', handleDragLeave);

        // 优先级标题（可点击折叠）
        const laneHeader = document.createElement('div');
        laneHeader.className = 'priority-header';
        laneHeader.innerHTML = `<span class="priority-icon">⬆</span> 优先级: ${priority} <span class="priority-count">(${channelsInPriority.length})</span>`;
        laneHeader.style.cursor = 'pointer';
        laneHeader.addEventListener('click', () => togglePriorityLane(type, priority));
        priorityLane.appendChild(laneHeader);

        // 渲染卡片
        channelsInPriority.forEach(channel => {
          const card = createChannelCard(channel);
          if (card) {
            // 添加 channel-card 类（用于拖拽选择器）
            card.classList.add('channel-card');
            // 为卡片添加拖拽属性
            card.setAttribute('draggable', 'true');
            card.dataset.id = channel.id;
            card.dataset.priority = priority;
            card.dataset.sortOrder = channel.sort_order || 0;

            // 绑定卡片拖拽事件
            card.addEventListener('dragstart', handleDragStart);
            card.addEventListener('dragend', handleDragEnd);

            priorityLane.appendChild(card);
          }
        });

        content.appendChild(priorityLane);
      });
    } else {
      // ===== 普通模式：简单列表布局 =====
      // 按优先级降序、sort_order升序排列
      channelsInType.sort((a, b) => {
        const pDiff = (b.priority || 0) - (a.priority || 0);
        if (pDiff !== 0) return pDiff;
        return (a.sort_order || 0) - (b.sort_order || 0);
      });

      // 直接渲染卡片（不显示优先级泳道）
      channelsInType.forEach(channel => {
        const card = createChannelCard(channel);
        if (card) {
          card.classList.add('channel-card');
          content.appendChild(card);
        }
      });
    }

    groupDiv.appendChild(header);
    groupDiv.appendChild(content);
    fragment.appendChild(groupDiv);
  });

  el.innerHTML = '';
  el.appendChild(fragment);
}

// ============================================================
// 拖拽处理函数
// ============================================================

// 拖拽状态：记录原始位置用于回滚
let draggedItem = null;
let draggedItemOriginalParent = null;
let draggedItemOriginalNextSibling = null;
let dropSucceeded = false;

function handleDragStart(e) {
  draggedItem = this;
  // 记录原始位置，用于拖拽取消时回滚
  draggedItemOriginalParent = this.parentElement;
  draggedItemOriginalNextSibling = this.nextElementSibling;
  dropSucceeded = false;

  e.dataTransfer.effectAllowed = 'move';
  // 延迟添加样式，避免拖拽时的重影也带有透明度
  setTimeout(() => this.classList.add('dragging'), 0);
}

function handleDragEnd(e) {
  this.classList.remove('dragging');

  // 清除所有泳道的高亮状态
  document.querySelectorAll('.priority-group').forEach(group => {
    group.classList.remove('drag-over');
  });

  // 如果 drop 未成功执行，回滚到原始位置
  if (!dropSucceeded && draggedItemOriginalParent) {
    if (draggedItemOriginalNextSibling) {
      draggedItemOriginalParent.insertBefore(this, draggedItemOriginalNextSibling);
    } else {
      draggedItemOriginalParent.appendChild(this);
    }
  }

  // 清理状态
  draggedItem = null;
  draggedItemOriginalParent = null;
  draggedItemOriginalNextSibling = null;
  dropSucceeded = false;
}

function handleDragOver(e) {
  e.preventDefault(); // 允许放置

  if (!draggedItem) return;

  const container = e.currentTarget;

  // 限制：只能在同类型内拖拽
  const draggedType = draggedItem.closest('.channel-type-group')?.dataset.type;
  const targetType = container.dataset.type;
  if (draggedType !== targetType) {
    e.dataTransfer.dropEffect = 'none';
    return;
  }

  container.classList.add('drag-over');

  // 获取鼠标位置下方的元素
  const afterElement = getDragAfterElement(container, e.clientY);

  // 只有当位置真正改变时才操作 DOM
  if (afterElement == null) {
    container.appendChild(draggedItem);
  } else if (afterElement !== draggedItem) {
    container.insertBefore(draggedItem, afterElement);
  }
}

function handleDragEnter(e) {
  e.preventDefault();
  if (draggedItem) {
    const container = e.currentTarget;
    container.classList.add('drag-over');

    // 如果目标泳道是折叠的，自动展开
    if (container.classList.contains('priority-group') && container.classList.contains('is-collapsed')) {
      expandPriorityLane(container);
    }
  }
}

function handleDragLeave(e) {
  // 只有当离开当前元素而不是进入子元素时才移除
  if (e.relatedTarget && !e.currentTarget.contains(e.relatedTarget)) {
    e.currentTarget.classList.remove('drag-over');
  }
}

/**
 * 核心逻辑：处理放下 (Drop)
 */
async function handleDrop(e, groupType, newPriority) {
  e.preventDefault();
  const container = e.currentTarget;
  container.classList.remove('drag-over');

  if (!draggedItem) return;

  // 限制：只能在同类型内拖拽
  const draggedType = draggedItem.closest('.channel-type-group')?.dataset.type;
  if (draggedType !== groupType) {
    return; // 类型不匹配，拒绝放置
  }

  // 标记 drop 成功（防止 dragEnd 回滚）
  dropSucceeded = true;

  const channelId = parseInt(draggedItem.dataset.id);
  // 获取当前泳道内所有卡片（不包含 header）
  const cards = [...container.querySelectorAll('.channel-card')];

  // 重新计算该组内所有卡片的 sort_order
  const updates = cards.map((card, index) => ({
    id: parseInt(card.dataset.id),
    priority: newPriority,
    sort_order: index
  }));

  // 乐观更新 UI (Data Attributes)
  draggedItem.dataset.priority = newPriority;
  cards.forEach((card, index) => {
    card.dataset.sortOrder = index;
  });

  // 更新本地 channels 数组和 UI 显示
  updates.forEach(upd => {
    const ch = channels.find(c => c.id === upd.id);
    if (ch) {
      ch.priority = upd.priority;
      ch.sort_order = upd.sort_order;
    }
    // 更新卡片上显示的优先级文本
    const card = document.querySelector(`.channel-card[data-channel-id="${upd.id}"]`);
    if (card) {
      const priorityValue = card.querySelector('.col-priority .col-value');
      if (priorityValue) {
        priorityValue.textContent = upd.priority;
      }
    }
  });

  // 排序模式：记录更改，等待用户点击保存
  // 已在 recordSortChange 中实现，由 saveSortChanges 统一提交
  recordSortChange(updates);
}

/**
 * 辅助函数：根据鼠标 Y 坐标获取插入位置后方的元素
 */
function getDragAfterElement(container, y) {
  const draggableElements = [...container.querySelectorAll('.channel-card:not(.dragging)')];

  return draggableElements.reduce((closest, child) => {
    const box = child.getBoundingClientRect();
    const offset = y - box.top - box.height / 2;

    if (offset < 0 && offset > closest.offset) {
      return { offset: offset, element: child };
    } else {
      return closest;
    }
  }, { offset: Number.NEGATIVE_INFINITY }).element;
}

/**
 * API 调用：保存排序
 */
async function saveChannelOrder(changes) {
  const response = await fetchWithAuth('/admin/channels/reorder', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({ changes: changes })
  });

  if (!response.ok) {
    throw new Error('Failed to save order');
  }

  return response.json();
}
