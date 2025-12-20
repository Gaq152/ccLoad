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
 * 渲染渠道列表（按类型分组）
 */
function renderChannels(channelsToRender = channels) {
  const el = document.getElementById('channels-container');
  if (!channelsToRender || channelsToRender.length === 0) {
    el.innerHTML = '<div class="glass-card">暂无符合条件的渠道</div>';
    return;
  }

  // 初始化事件委托（仅一次）
  initChannelEventDelegation();

  // 按 channel_type 分组
  const groups = {};
  channelsToRender.forEach(channel => {
    const type = (channel.channel_type || 'anthropic').toLowerCase();
    if (!groups[type]) {
      groups[type] = [];
    }
    groups[type].push(channel);
  });

  // 类型显示名称和排序优先级
  const typeConfig = {
    'anthropic': { name: 'Anthropic (Claude)', order: 1 },
    'codex': { name: 'Codex', order: 2 },
    'gemini': { name: 'Gemini', order: 3 },
    'openai': { name: 'OpenAI', order: 4 }
  };

  // 按优先级排序类型
  const sortedTypes = Object.keys(groups).sort((a, b) => {
    const orderA = typeConfig[a]?.order || 99;
    const orderB = typeConfig[b]?.order || 99;
    return orderA - orderB;
  });

  // 使用 DocumentFragment 优化
  const fragment = document.createDocumentFragment();

  sortedTypes.forEach(type => {
    const channelsInGroup = groups[type];
    const config = typeConfig[type] || { name: type.toUpperCase(), order: 99 };
    const enabledCount = channelsInGroup.filter(c => c.enabled).length;
    const isCollapsed = channelGroupCollapsed[type] || false;

    // 创建分组容器
    const groupDiv = document.createElement('div');
    groupDiv.className = `channel-type-group${isCollapsed ? ' is-collapsed' : ''}`;
    groupDiv.dataset.type = type;

    // 分组头部
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
        <span class="channel-group-count">${enabledCount}/${channelsInGroup.length} 启用</span>
      </div>
    `;

    // 分组内容
    const content = document.createElement('div');
    content.className = 'channel-group-content';

    channelsInGroup.forEach(channel => {
      const card = createChannelCard(channel);
      if (card) content.appendChild(card);
    });

    groupDiv.appendChild(header);
    groupDiv.appendChild(content);
    fragment.appendChild(groupDiv);
  });

  el.innerHTML = '';
  el.appendChild(fragment);
}
