// 仪表盘状态
window.currentTimeRange = 'today'; // 全局时间范围（供健康矩阵同步）
let currentChannelType = ''; // 空表示全部
let refreshCountdown = 30;
let countdownInterval = null;
let heatmapTooltip = null;

const CHANNEL_TYPES = [
  { type: 'anthropic', name: 'Claude' },
  { type: 'codex', name: 'Codex' },
  { type: 'openai', name: 'OpenAI' },
  { type: 'gemini', name: 'Google' }
];

// 缓存配置
const INDEX_CACHE_KEY = 'index_dashboard_cache_v1';
const INDEX_CACHE_DURATION = 30; // 30秒

// 从 localStorage 加载缓存
function loadIndexCache() {
  try {
    const stored = localStorage.getItem(INDEX_CACHE_KEY);
    if (stored) return JSON.parse(stored);
  } catch (e) {
    console.warn('[Dashboard] 读取缓存失败:', e);
  }
  return null;
}

// 保存缓存到 localStorage
function saveIndexCache(timeRange, statsData, channelsData) {
  try {
    const cache = {
      timeRange: timeRange,
      stats: statsData,
      channels: channelsData,
      fetchedAt: Date.now()
    };
    localStorage.setItem(INDEX_CACHE_KEY, JSON.stringify(cache));
  } catch (e) {
    console.warn('[Dashboard] 保存缓存失败:', e);
  }
}

// 检查缓存是否有效
function isIndexCacheValid(cache, timeRange) {
  if (!cache || !cache.fetchedAt || cache.timeRange !== timeRange) return false;
  const elapsed = (Date.now() - cache.fetchedAt) / 1000;
  return elapsed < INDEX_CACHE_DURATION;
}

// 获取缓存剩余时间（秒）
function getIndexCacheRemaining(cache) {
  if (!cache || !cache.fetchedAt) return 0;
  const elapsed = (Date.now() - cache.fetchedAt) / 1000;
  return Math.max(0, Math.floor(INDEX_CACHE_DURATION - elapsed));
}

// 缓存的数据（用于保存）
let lastStatsData = null;
let lastChannelsData = null;

// 格式化数字
function formatNumber(num) {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M';
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K';
  return num.toString();
}

// 加载统计数据
async function loadStats() {
  try {
    const response = await fetch(`/public/summary?range=${window.currentTimeRange}`);
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const responseData = await response.json();
    const data = responseData.success ? (responseData.data || responseData) : responseData;
    lastStatsData = data; // 保存用于缓存
    updateStatsDisplay(data);
  } catch (error) {
    console.error('加载统计失败:', error);
  }
}

// 更新统计显示
function updateStatsDisplay(data) {
  const total = data.total_requests || 0;
  const success = data.success_requests || 0;
  const error = data.error_requests || 0;
  const rate = total > 0 ? ((success / total) * 100).toFixed(1) : '0.0';

  document.getElementById('stat-total').textContent = formatNumber(total);
  document.getElementById('stat-total-meta').textContent = `成功 ${formatNumber(success)} / 失败 ${formatNumber(error)}`;

  document.getElementById('stat-rate').textContent = rate + '%';
  const rateEl = document.getElementById('stat-rate');
  rateEl.className = 'stat-value ' + (parseFloat(rate) >= 95 ? 'success' : parseFloat(rate) >= 80 ? 'warning' : 'error');

  // Token 统计
  let totalInput = 0, totalOutput = 0, totalCost = 0;
  if (data.by_type) {
    Object.values(data.by_type).forEach(t => {
      totalInput += t.total_input_tokens || 0;
      totalOutput += t.total_output_tokens || 0;
      totalCost += t.total_cost || 0;
    });
  }

  document.getElementById('stat-tokens').textContent = formatNumber(totalInput + totalOutput);
  document.getElementById('stat-tokens-meta').textContent = `输入 ${formatNumber(totalInput)} / 输出 ${formatNumber(totalOutput)}`;

  document.getElementById('stat-cost').textContent = formatCost(totalCost);
  document.getElementById('stat-cost-meta').textContent = `本${window.currentTimeRange === 'today' ? '日' : window.currentTimeRange === 'this_week' ? '周' : '月'}累计`;
}

// 加载渠道状态
async function loadChannelStatus() {
  try {
    const response = await fetchWithAuth('/admin/channels');
    if (!response.ok) return;
    const result = await response.json();
    const channels = result.success ? result.data : result;
    lastChannelsData = channels; // 保存用于缓存

    const list = document.getElementById('channel-status-list');
    if (!channels || channels.length === 0) {
      list.innerHTML = '<div class="dash-text-muted" style="padding: 20px; text-align: center;">暂无渠道</div>';
      return;
    }

    list.innerHTML = channels.slice(0, 10).map(ch => {
      const statusClass = ch.cooldown_remaining_ms > 0 ? 'cooldown' : (ch.enabled ? 'active' : 'error');
      const statusText = ch.cooldown_remaining_ms > 0 ? '冷却中' : (ch.enabled ? '正常' : '禁用');
      return `
        <div class="status-item">
          <div class="status-icon ${statusClass}"></div>
          <div class="status-name">${escapeHtml(ch.name)}</div>
          <div class="status-metric">${statusText}</div>
        </div>
      `;
    }).join('');
  } catch (error) {
    console.error('加载渠道状态失败:', error);
  }
}

// 初始化渠道类型筛选
function initChannelTypeFilter() {
  const container = document.getElementById('channel-type-filter');
  if (!container) return;

  container.innerHTML = `<button class="filter-btn active" data-type="">全部</button>` +
    CHANNEL_TYPES.map(ct => `<button class="filter-btn" data-type="${ct.type}">${ct.name}</button>`).join('');

  container.querySelectorAll('.filter-btn').forEach(btn => {
    btn.addEventListener('click', function() {
      container.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
      this.classList.add('active');
      currentChannelType = this.dataset.type;
      loadTrafficTrend();
    });
  });
}

// 连接日志 SSE 流（带历史日志加载）
function connectLogStream() {
  const token = localStorage.getItem('ccload_token');
  const tbody = document.getElementById('logs-tbody');
  if (window.logEventSource) window.logEventSource.close();

  // 使用今天0点作为 since_ms，获取今日历史日志
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const sinceMs = today.getTime();

  let url = '/admin/logs/stream?since_ms=' + sinceMs;
  if (token) url += '&token=' + encodeURIComponent(token);
  const es = new EventSource(url);
  window.logEventSource = es;

  // 后端发送 event: log，必须用 addEventListener
  es.addEventListener('log', function(e) {
    if (!e.data) return;
    try {
      const log = JSON.parse(e.data);
      const time = new Date(log.time * 1000).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
      const statusClass = log.status_code >= 500 ? 'status-5xx' : log.status_code >= 400 ? 'status-4xx' : 'status-2xx';
      const tokens = (log.input_tokens || 0) + (log.output_tokens || 0);
      const duration = log.first_byte_time ? log.first_byte_time.toFixed(2) + 's' : '--';
      const cost = log.cost ? formatCost(log.cost) : '--';

      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td class="dash-text-muted">${time}</td>
        <td class="${statusClass}">${log.status_code || '--'}</td>
        <td>${escapeHtml(log.channel_name || '--')}</td>
        <td class="text-highlight">${escapeHtml(log.model || '--')}</td>
        <td>${formatNumber(tokens)}</td>
        <td>${duration}</td>
        <td>${cost}</td>
      `;
      tbody.prepend(tr);
      if (tbody.children.length > 20) tbody.removeChild(tbody.lastElementChild);
      const empty = tbody.querySelector('td[colspan="7"]');
      if (empty) empty.parentElement.remove();
    } catch (err) { console.error('日志解析失败:', err); }
  });

  es.onerror = function() {
    es.close();
    setTimeout(connectLogStream, 5000);
  };
}

// 计算热力图颜色等级（5个等级）
function getHeatLevel(count, maxCount) {
  if (count === 0) return 0;
  if (maxCount === 0) return 0;
  const ratio = count / maxCount;
  if (ratio >= 0.75) return 4;
  if (ratio >= 0.5) return 3;
  if (ratio >= 0.25) return 2;
  if (ratio > 0) return 1;
  return 0;
}

// 渲染热力图
function renderHeatmap(data, timeRange) {
  const container = document.getElementById('trafficHeatmap');
  if (!container) return;

  container.innerHTML = '';
  container.className = `heatmap-grid view-${timeRange}`;

  let cells = [];
  const now = new Date();

  if (timeRange === 'today') {
    for (let hour = 0; hour < 24; hour++) {
      const bucket = data.find(b => new Date(b.ts).getHours() === hour);
      const total = bucket ? (bucket.success || 0) + (bucket.error || 0) : 0;
      cells.push({ label: `${hour}:00`, count: total, time: `${hour}:00` });
    }
  } else if (timeRange === 'this_week') {
    const weekDays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
    for (let i = 0; i < 7; i++) {
      const date = new Date(now);
      date.setDate(date.getDate() - date.getDay() + i);
      const dayStart = new Date(date.setHours(0, 0, 0, 0));
      const dayEnd = new Date(date.setHours(23, 59, 59, 999));
      const dayData = data.filter(b => {
        const ts = new Date(b.ts);
        return ts >= dayStart && ts <= dayEnd;
      });
      const total = dayData.reduce((sum, b) => sum + (b.success || 0) + (b.error || 0), 0);
      cells.push({ label: weekDays[i], count: total, time: `${date.getMonth() + 1}/${date.getDate()} ${weekDays[i]}` });
    }
  } else if (timeRange === 'this_month') {
    const year = now.getFullYear();
    const month = now.getMonth();
    const daysInMonth = new Date(year, month + 1, 0).getDate();
    for (let day = 1; day <= daysInMonth; day++) {
      const date = new Date(year, month, day);
      const dayStart = new Date(date.setHours(0, 0, 0, 0));
      const dayEnd = new Date(date.setHours(23, 59, 59, 999));
      const dayData = data.filter(b => {
        const ts = new Date(b.ts);
        return ts >= dayStart && ts <= dayEnd;
      });
      const total = dayData.reduce((sum, b) => sum + (b.success || 0) + (b.error || 0), 0);
      cells.push({ label: `${month + 1}/${day}`, count: total, time: `${month + 1}月${day}日` });
    }
  }

  const maxCount = Math.max(...cells.map(c => c.count), 1);

  cells.forEach(cell => {
    const div = document.createElement('div');
    div.className = `heatmap-cell level-${getHeatLevel(cell.count, maxCount)}`;
    div.dataset.time = cell.time;
    div.dataset.count = cell.count;
    div.addEventListener('mouseenter', showTooltip);
    div.addEventListener('mouseleave', hideTooltip);
    container.appendChild(div);
  });
}

function showTooltip(e) {
  const cell = e.target;
  if (!heatmapTooltip) {
    heatmapTooltip = document.createElement('div');
    heatmapTooltip.className = 'heatmap-tooltip';
    document.body.appendChild(heatmapTooltip);
  }
  heatmapTooltip.innerHTML = `<div class="tooltip-time">${cell.dataset.time}</div><div class="tooltip-count">${cell.dataset.count} 次请求</div>`;
  const rect = cell.getBoundingClientRect();
  heatmapTooltip.style.left = `${rect.left + rect.width / 2 - heatmapTooltip.offsetWidth / 2}px`;
  heatmapTooltip.style.top = `${rect.top - heatmapTooltip.offsetHeight - 8}px`;
  heatmapTooltip.style.display = 'block';
}

function hideTooltip() {
  if (heatmapTooltip) heatmapTooltip.style.display = 'none';
}

// 加载趋势数据
async function loadTrafficTrend() {
  try {
    const bucketMin = window.currentTimeRange === 'today' ? 60 : 1440;
    let url = `/admin/metrics?range=${window.currentTimeRange}&bucket_min=${bucketMin}`;
    if (currentChannelType) url += `&channel_type=${currentChannelType}`;
    const response = await fetchWithAuth(url);
    if (!response.ok) return;
    const result = await response.json();
    const buckets = result.success ? result.data : result;
    if (!buckets || !buckets.length) return;
    renderHeatmap(buckets, window.currentTimeRange);
  } catch (e) { console.error('加载趋势失败:', e); }
}

// 刷新所有数据
async function refreshAll() {
  await Promise.all([
    loadStats(),
    loadChannelStatus()
  ]);
  // 保存缓存
  if (lastStatsData && lastChannelsData) {
    saveIndexCache(window.currentTimeRange, lastStatsData, lastChannelsData);
  }
  refreshCountdown = 30;
}

// 从缓存初始化或刷新
async function initWithCache() {
  const cache = loadIndexCache();
  if (isIndexCacheValid(cache, window.currentTimeRange)) {
    // 缓存有效，使用缓存数据
    lastStatsData = cache.stats;
    lastChannelsData = cache.channels;
    updateStatsDisplay(cache.stats);
    renderChannelStatus(cache.channels);
    refreshCountdown = getIndexCacheRemaining(cache);
    console.log(`[Dashboard] 使用缓存数据，剩余 ${refreshCountdown}s`);
  } else {
    // 缓存无效，重新获取
    await refreshAll();
  }
}

// 渲染渠道状态（从缓存数据）
function renderChannelStatus(channels) {
  const list = document.getElementById('channel-status-list');
  if (!channels || channels.length === 0) {
    list.innerHTML = '<div class="dash-text-muted" style="padding: 20px; text-align: center;">暂无渠道</div>';
    return;
  }
  list.innerHTML = channels.slice(0, 10).map(ch => {
    const statusClass = ch.cooldown_remaining_ms > 0 ? 'cooldown' : (ch.enabled ? 'active' : 'error');
    const statusText = ch.cooldown_remaining_ms > 0 ? '冷却中' : (ch.enabled ? '正常' : '禁用');
    return `
      <div class="status-item">
        <div class="status-icon ${statusClass}"></div>
        <div class="status-name">${escapeHtml(ch.name)}</div>
        <div class="status-metric">${statusText}</div>
      </div>
    `;
  }).join('');
}

// 倒计时
function startCountdown() {
  if (countdownInterval) clearInterval(countdownInterval);
  countdownInterval = setInterval(() => {
    refreshCountdown--;
    document.getElementById('refresh-countdown').textContent = refreshCountdown + 's';
    if (refreshCountdown <= 0) {
      refreshAll();
    }
  }, 1000);
}

// 时间范围切换
function initTimeRangeButtons() {
  document.querySelectorAll('.dash-btn[data-range]').forEach(btn => {
    btn.addEventListener('click', function() {
      document.querySelectorAll('.dash-btn[data-range]').forEach(b => b.classList.remove('active'));
      this.classList.add('active');
      window.currentTimeRange = this.dataset.range;
      refreshAll();
    });
  });
}

// 页面可见性
document.addEventListener('visibilitychange', function() {
  if (document.hidden) {
    if (countdownInterval) clearInterval(countdownInterval);
  } else {
    // 返回页面时也使用缓存逻辑
    initWithCache();
    startCountdown();
  }
});

// 初始化
document.addEventListener('DOMContentLoaded', async function() {
  if (window.initTopbar) initTopbar('index');
  initTimeRangeButtons();
  connectLogStream();
  await initWithCache();
  // 立即更新倒计时显示
  document.getElementById('refresh-countdown').textContent = refreshCountdown + 's';
  startCountdown();

  // 初始化渠道健康矩阵
  if (typeof initHealthMatrix === 'function') {
    await initHealthMatrix();
  }

  // 手动刷新按钮
  document.getElementById('refresh-btn').addEventListener('click', function() {
    refreshAll();
  });
});
