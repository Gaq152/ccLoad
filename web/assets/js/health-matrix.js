// 渠道健康矩阵模块
// 集成第三方监控数据（relaypulse.top），展示LLM渠道实时健康状态

let currentPeriod = '24h';
let healthData = [];
let internalChannels = [];
let hiddenChannels = new Map(); // Map<id, name> 存储已隐藏渠道
let healthMatrixCountdown = 300; // 5分钟倒计时（秒）
let healthMatrixInterval = null; // 倒计时定时器

// 初始化渠道健康矩阵
async function initHealthMatrix() {
  try {
    // 加载隐藏列表（优先加载）
    loadHiddenChannels();

    // 加载内部渠道列表（用于映射）
    internalChannels = await loadAllInternalChannels();

    // 加载并渲染健康数据（使用默认周期）
    await loadAndRenderHealthData(currentPeriod);

    // 绑定周期切换按钮
    setupPeriodButtons();

    // 绑定 Tab 切换按钮
    setupTabButtons();

    // 绑定隐藏管理按钮
    setupHiddenManager();

    // 启动自动刷新倒计时
    startHealthMatrixCountdown();

    // 绑定手动刷新按钮
    setupHealthMatrixRefreshButton();
  } catch (error) {
    console.error('健康矩阵初始化失败:', error);
    showHealthMatrixError('数据加载失败，请稍后重试');
  }
}

// 加载所有内部渠道（用于映射）
async function loadAllInternalChannels() {
  try {
    const resp = await fetchWithAuth('/admin/channels?enabled=all');
    if (!resp.ok) {
      console.error('加载内部渠道失败:', resp.status);
      return [];
    }
    const data = await resp.json();
    return data.success ? data.data : data;
  } catch (error) {
    console.error('加载内部渠道异常:', error);
    return [];
  }
}

// 获取外部健康数据
async function fetchHealthData(period = '24h') {
  const resp = await fetchWithAuth(`/admin/channel-health-proxy?period=${period}`);
  if (!resp.ok) {
    const isStale = resp.headers.get('X-Cache-Stale') === 'true';
    if (isStale) {
      console.warn('健康数据来自过期缓存');
    } else {
      throw new Error(`获取健康数据失败: ${resp.status}`);
    }
  }
  return resp.json();
}

// 映射外部数据到内部渠道
function mapToInternalChannels(externalData) {
  const mapped = [];

  for (const ext of externalData) {
    // 跳过无效记录
    if (!ext.provider || !ext.service || !ext.channel || !ext.probe_url) {
      continue;
    }

    // 尝试通过probe_url的hostname匹配内部渠道endpoint
    const match = findMatchingChannel(ext);

    if (match) {
      // 计算整体可用性（基于timeline平均值）
      const avgAvailability = calculateAvailability(ext.timeline);
      const mainError = extractMainError(ext.timeline);

      mapped.push({
        channelId: match.id,
        channelName: `${ext.provider}-${ext.channel.toUpperCase()}`,
        provider: ext.provider,
        service: ext.service, // "cc" or "cx"
        probeUrl: ext.probe_url,
        currentStatus: ext.current_status?.status || 0,
        currentLatency: ext.current_status?.latency || 0,
        availability: avgAvailability,
        timeline: ext.timeline || [],
        mainError: mainError
      });
    } else {
      // 未映射的渠道也显示（标记为外部）
      const avgAvailability = calculateAvailability(ext.timeline);
      mapped.push({
        channelId: null,
        channelName: `${ext.provider}-${ext.channel.toUpperCase()}`,
        provider: ext.provider,
        service: ext.service,
        probeUrl: ext.probe_url,
        currentStatus: ext.current_status?.status || 0,
        currentLatency: ext.current_status?.latency || 0,
        availability: avgAvailability,
        timeline: ext.timeline || [],
        mainError: extractMainError(ext.timeline),
        isExternal: true
      });
    }
  }

  return mapped;
}

// 查找匹配的内部渠道
function findMatchingChannel(externalRecord) {
  try {
    const probeHost = new URL(externalRecord.probe_url).hostname;

    // 优先精确匹配endpoint
    let match = internalChannels.find(ch => {
      const endpoint = ch.endpoint || '';
      return endpoint.includes(probeHost);
    });

    // 回退：根据provider名称模糊匹配
    if (!match && externalRecord.provider) {
      const providerLower = externalRecord.provider.toLowerCase();
      match = internalChannels.find(ch => {
        const nameLower = (ch.name || '').toLowerCase();
        return nameLower.includes(providerLower) || providerLower.includes(nameLower);
      });
    }

    return match;
  } catch (error) {
    console.error('匹配渠道失败:', error, externalRecord);
    return null;
  }
}

// 计算平均可用性
function calculateAvailability(timeline) {
  if (!timeline || timeline.length === 0) return 0;

  const sum = timeline.reduce((acc, point) => acc + (point.availability || 0), 0);
  return sum / timeline.length;
}

// 提取主要错误类型
function extractMainError(timeline) {
  if (!timeline || timeline.length === 0) return null;

  const errorCounts = {
    auth_error: 0,
    server_error: 0,
    network_error: 0,
    rate_limit: 0,
    client_error: 0
  };

  timeline.forEach(point => {
    const counts = point.status_counts || {};
    Object.keys(errorCounts).forEach(key => {
      errorCounts[key] += counts[key] || 0;
    });
  });

  // 找出最多的错误类型
  const maxError = Object.entries(errorCounts)
    .filter(([_, count]) => count > 0)
    .sort((a, b) => b[1] - a[1])[0];

  if (!maxError) return null;

  const errorNames = {
    auth_error: '认证错误',
    server_error: '服务器错误',
    network_error: '网络错误',
    rate_limit: '限流',
    client_error: '客户端错误'
  };

  return `${errorNames[maxError[0]]}: ${maxError[1]}`;
}

// 加载并渲染健康数据
async function loadAndRenderHealthData(period) {
  try {
    // 显示加载状态
    showHealthMatrixLoading();

    // 获取外部数据
    const externalData = await fetchHealthData(period);

    // 映射到内部渠道
    healthData = mapToInternalChannels(externalData);

    // 渲染矩阵
    renderHealthMatrix(healthData);
  } catch (error) {
    console.error('加载健康数据失败:', error);
    showHealthMatrixError('数据加载失败，请稍后重试');
  }
}

// 渲染健康矩阵
function renderHealthMatrix(data) {
  // 过滤已隐藏渠道
  const visibleData = data.filter(d => !isChannelHidden(d));

  // 按service分组
  const ccData = visibleData.filter(d => d.service === 'cc');
  const cxData = visibleData.filter(d => d.service === 'cx');

  renderServiceGroup('grid-cc', ccData);
  renderServiceGroup('grid-cx', cxData);

  // 隐藏加载状态
  hideHealthMatrixLoading();
}

// 渲染服务分组
function renderServiceGroup(gridId, data) {
  const grid = document.getElementById(gridId);
  if (!grid) return;

  if (data.length === 0) {
    grid.innerHTML = '<div class="no-data-hint">暂无数据</div>';
    return;
  }

  grid.innerHTML = data.map(ch => {
    const statusClass = getStatusClass(ch.availability);
    const availText = ch.availability.toFixed(1);
    const latencyText = ch.currentLatency > 0 ? `${ch.currentLatency}ms` : '–';
    const uniqueId = getChannelUniqueId(ch);

    return `
      <div class="health-card ${statusClass}" data-channel-id="${ch.channelId || ''}">
        <button class="hide-channel-btn" onclick="hideChannel('${uniqueId}', '${escapeHtml(ch.channelName).replace(/'/g, "\\'")}')" title="隐藏此渠道">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24M1 1l22 22"/>
          </svg>
        </button>
        <div class="card-header">
          <div class="channel-info">
            <span class="status-dot"></span>
            <span class="channel-name" title="${ch.probeUrl}">${escapeHtml(ch.channelName)}</span>
          </div>
          <div class="health-metric">
            <span class="metric-value">${availText}%</span>
          </div>
        </div>
        <div class="card-stats">
          <span class="stat-item">${latencyText}</span>
          ${ch.mainError ? `<span class="stat-item error-text">${escapeHtml(ch.mainError)}</span>` : ''}
        </div>
        <div class="uptime-timeline">
          ${renderTimeline(ch.timeline)}
        </div>
      </div>
    `;
  }).join('');
}

// 获取状态CSS类
function getStatusClass(availability) {
  if (availability >= 90) return 'status-operational';
  if (availability >= 70) return 'status-degraded';
  return 'status-down';
}

// 格式化时间段（根据周期）
function formatTimeRange(timeStr, period) {
  try {
    // 如果timeStr已经包含" - "，说明已经是时间段格式，直接返回
    if (timeStr && timeStr.includes(' - ')) {
      return timeStr;
    }

    if (period === '24h') {
      // 24小时周期：time格式为"HH:MM"，提取小时部分
      const match = String(timeStr).match(/^(\d{1,2}):(\d{2})/);
      if (match) {
        const hour = parseInt(match[1], 10);
        const startHour = hour.toString().padStart(2, '0');
        const endHour = ((hour + 1) % 24).toString().padStart(2, '0');
        return `${startHour}:00 - ${endHour}:00`;
      }
    } else {
      // 7天/30天周期：尝试解析日期
      let date;
      if (typeof timeStr === 'number') {
        date = new Date(timeStr * 1000);
      } else {
        date = new Date(timeStr);
      }

      if (!isNaN(date.getTime())) {
        const month = (date.getMonth() + 1).toString().padStart(2, '0');
        const day = date.getDate().toString().padStart(2, '0');
        return `${month}-${day} 00:00 - 23:59`;
      }
    }

    // 如果都无法解析，返回原始字符串
    return timeStr || '未知时间';
  } catch (e) {
    console.error('格式化时间段失败:', e, timeStr);
    return timeStr || '未知时间';
  }
}

// 渲染时间线（Uptime Barcode）
function renderTimeline(timeline) {
  if (!timeline || timeline.length === 0) {
    return '<div class="uptime-bar status-nodata"></div>'.repeat(24);
  }

  return timeline.map(point => {
    const avail = point.availability || 0;
    let barClass = 'status-nodata';
    let availLevel = 'low';

    if (avail >= 90) {
      barClass = 'status-ok';
      availLevel = avail === 100 ? '100' : 'high';
    } else if (avail >= 50) {
      barClass = 'status-warn';
      availLevel = 'mid';
    } else if (avail > 0) {
      barClass = 'status-down';
      availLevel = 'low';
    }

    // 格式化为时间段
    const timeRange = formatTimeRange(point.time, currentPeriod);
    const tooltip = `${timeRange}\n可用性: ${avail.toFixed(0)}%\n延迟: ${point.latency || 0}ms`;

    return `<div class="uptime-bar ${barClass}" data-avail="${availLevel}" title="${escapeHtml(tooltip)}"></div>`;
  }).join('');
}

// 显示加载状态
function showHealthMatrixLoading() {
  const panel = document.querySelector('.health-matrix-panel');
  if (panel) {
    panel.classList.add('loading');
  }
}

// 隐藏加载状态
function hideHealthMatrixLoading() {
  const panel = document.querySelector('.health-matrix-panel');
  if (panel) {
    panel.classList.remove('loading');
  }
}

// 显示错误提示
function showHealthMatrixError(message) {
  const ccGrid = document.getElementById('grid-cc');
  const cxGrid = document.getElementById('grid-cx');

  if (ccGrid) ccGrid.innerHTML = `<div class="error-hint">${escapeHtml(message)}</div>`;
  if (cxGrid) cxGrid.innerHTML = '';

  hideHealthMatrixLoading();
}

// HTML转义（防XSS）
function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// 设置周期切换按钮（独立控制）
function setupPeriodButtons() {
  const buttons = document.querySelectorAll('.matrix-controls [data-period]');
  buttons.forEach(btn => {
    btn.addEventListener('click', async (e) => {
      const period = e.target.dataset.period;
      if (!period || period === currentPeriod) return;

      // 更新按钮状态
      buttons.forEach(b => b.classList.remove('active'));
      e.target.classList.add('active');

      // 切换周期并刷新数据
      currentPeriod = period;
      await refreshHealthMatrix();
    });
  });
}

// 设置 Tab 切换逻辑
function setupTabButtons() {
  const tabBtns = document.querySelectorAll('.matrix-controls [data-tab]');

  tabBtns.forEach(btn => {
    btn.addEventListener('click', (e) => {
      const targetTab = e.target.dataset.tab;
      if (!targetTab) return;

      // 1. 更新按钮状态
      tabBtns.forEach(b => b.classList.remove('active'));
      e.target.classList.add('active');

      // 2. 切换内容区域显示
      document.querySelectorAll('.tab-content').forEach(content => {
        content.classList.remove('active');
      });

      const targetContent = document.getElementById(`tab-${targetTab}`);
      if (targetContent) {
        targetContent.classList.add('active');
      }
    });
  });
}

// 刷新健康矩阵数据
async function refreshHealthMatrix() {
  await loadAndRenderHealthData(currentPeriod);
  // 重置倒计时
  healthMatrixCountdown = 300;
  updateHealthMatrixCountdownDisplay();
}

// 启动健康矩阵倒计时
function startHealthMatrixCountdown() {
  if (healthMatrixInterval) clearInterval(healthMatrixInterval);

  healthMatrixInterval = setInterval(() => {
    healthMatrixCountdown--;
    updateHealthMatrixCountdownDisplay();

    if (healthMatrixCountdown <= 0) {
      refreshHealthMatrix();
    }
  }, 1000);
}

// 更新倒计时显示
function updateHealthMatrixCountdownDisplay() {
  const countdownEl = document.getElementById('health-matrix-countdown');
  if (countdownEl) {
    const minutes = Math.floor(healthMatrixCountdown / 60);
    const seconds = healthMatrixCountdown % 60;
    countdownEl.textContent = `${minutes}:${seconds.toString().padStart(2, '0')}`;
  }
}

// 设置手动刷新按钮
function setupHealthMatrixRefreshButton() {
  const refreshBtn = document.getElementById('health-matrix-refresh-btn');
  if (refreshBtn) {
    refreshBtn.addEventListener('click', () => {
      refreshHealthMatrix();
    });
  }
}

// ============ 渠道隐藏功能 ============

// 获取渠道唯一ID（始终使用channelName，确保稳定性）
function getChannelUniqueId(channel) {
  // 使用 provider-channel 组合作为唯一标识，不依赖可变的channelId
  return channel.channelName;
}

// 检查渠道是否已隐藏
function isChannelHidden(channel) {
  return hiddenChannels.has(getChannelUniqueId(channel));
}

// 从localStorage加载隐藏列表
function loadHiddenChannels() {
  try {
    const saved = localStorage.getItem('ccload_hidden_channels');
    if (saved) {
      const data = JSON.parse(saved);
      hiddenChannels = new Map(data);
    }
  } catch (e) {
    console.error('加载隐藏列表失败:', e);
  }
}

// 保存隐藏列表到localStorage
function saveHiddenChannels() {
  try {
    const data = Array.from(hiddenChannels.entries());
    localStorage.setItem('ccload_hidden_channels', JSON.stringify(data));
  } catch (e) {
    console.error('保存隐藏列表失败:', e);
  }
}

// 隐藏渠道（全局函数，供HTML onclick调用）
window.hideChannel = function(id, name) {
  hiddenChannels.set(id, name);
  saveHiddenChannels();
  // 重新渲染
  renderHealthMatrix(healthData);
};

// 恢复渠道（全局函数，供HTML onclick调用）
window.restoreChannel = function(id) {
  hiddenChannels.delete(id);
  saveHiddenChannels();
  // 重新渲染健康矩阵和隐藏列表
  renderHealthMatrix(healthData);
  renderHiddenList();
};

// 打开隐藏管理模态框（全局函数，供HTML onclick调用）
window.openHiddenManager = function() {
  const modal = document.getElementById('hidden-channels-modal');
  if (modal) {
    renderHiddenList();
    modal.classList.add('show');
  }
};

// 关闭隐藏管理模态框（全局函数，供HTML onclick调用）
window.closeHiddenManager = function() {
  const modal = document.getElementById('hidden-channels-modal');
  if (modal) {
    modal.classList.remove('show');
  }
};

// 渲染已隐藏渠道列表
function renderHiddenList() {
  const listEl = document.getElementById('hidden-channels-list');
  if (!listEl) return;

  if (hiddenChannels.size === 0) {
    listEl.innerHTML = '<div class="hidden-empty">暂无隐藏渠道</div>';
    return;
  }

  listEl.innerHTML = Array.from(hiddenChannels.entries()).map(([id, name]) => {
    return `
      <div class="hidden-item">
        <span class="name">${escapeHtml(name)}</span>
        <button class="dash-btn" onclick="restoreChannel('${id}')">恢复</button>
      </div>
    `;
  }).join('');
}

// 设置隐藏管理按钮
function setupHiddenManager() {
  const manageBtn = document.getElementById('manage-hidden-btn');
  if (manageBtn) {
    manageBtn.addEventListener('click', window.openHiddenManager);
  }

  // 点击模态框背景关闭
  const modal = document.getElementById('hidden-channels-modal');
  if (modal) {
    modal.addEventListener('click', (e) => {
      if (e.target === modal) {
        window.closeHiddenManager();
      }
    });
  }
}
