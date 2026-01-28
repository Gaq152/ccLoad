function highlightFromHash() {
  const m = (location.hash || '').match(/^#channel-(\d+)$/);
  if (!m) return;
  const el = document.getElementById(`channel-${m[1]}`);
  if (!el) return;
  el.scrollIntoView({ behavior: 'smooth', block: 'center' });
  // 使用 CSS 动画类替代内联样式
  el.classList.add('input-highlight-anim');
  setTimeout(() => {
    el.classList.remove('input-highlight-anim');
  }, 2000);
}

// 从URL参数获取目标渠道ID，查询其类型并返回
async function getTargetChannelType() {
  const params = new URLSearchParams(location.search);
  const channelId = params.get('id');
  if (!channelId) return null;

  try {
    const channel = await fetchDataWithAuth(`/admin/channels/${channelId}`);
    return channel?.channel_type || 'anthropic';
  } catch (e) {
    console.error('获取渠道类型失败:', e);
    return null;
  }
}

// localStorage key for channels page filters
const CHANNELS_FILTER_KEY = 'channels.filters';

function saveChannelsFilters() {
  try {
    localStorage.setItem(CHANNELS_FILTER_KEY, JSON.stringify({
      channelType: filters.channelType,
      status: filters.status,
      model: filters.model
    }));
  } catch (_) {}
}

function loadChannelsFilters() {
  try {
    const saved = localStorage.getItem(CHANNELS_FILTER_KEY);
    if (saved) return JSON.parse(saved);
  } catch (_) {}
  return null;
}

document.addEventListener('DOMContentLoaded', async () => {
  if (window.initTopbar) initTopbar('channels');
  setupFilterListeners();
  setupImportExport();
  setupKeyImportPreview();

  await window.ChannelTypeManager.renderChannelTypeRadios('channelTypeRadios');

  // 优先从 localStorage 恢复，其次检查 URL 参数，最后默认 claude
  const savedFilters = loadChannelsFilters();
  const targetChannelType = await getTargetChannelType();
  const initialType = targetChannelType || (savedFilters?.channelType) || 'anthropic';

  filters.channelType = initialType;
  if (savedFilters) {
    filters.status = savedFilters.status || 'all';
    filters.model = savedFilters.model || 'all';
    document.getElementById('statusFilter').value = filters.status;
    document.getElementById('modelFilter').value = filters.model;
  }

  // 初始化渠道类型 Tab（不包含"全部"选项）
  await initChannelTypeTabs(initialType);

  await loadDefaultTestContent();
  await loadChannelStatsFields();

  await loadChannels(initialType);
  await loadChannelStats();
  highlightFromHash();
  window.addEventListener('hashchange', highlightFromHash);

  // 启动冷却事件 SSE 订阅
  startCooldownSSE();

  // 启动自动测速倒计时
  AutoTestTimer.init();

  // 监听"支持的模型"输入框变化，实时更新模型列表（用于模型重定向下拉选择）
  const modelsInput = document.getElementById('channelModels');
  if (modelsInput && typeof updateModelDatalist === 'function') {
    modelsInput.addEventListener('input', updateModelDatalist);
    modelsInput.addEventListener('change', updateModelDatalist);
  }

  // 页面可见性监听（后台标签页暂停倒计时，节省CPU）
  document.addEventListener('visibilitychange', function() {
    if (document.hidden) {
      stopCooldownCountdown();
      stopCooldownSSE();
      AutoTestTimer.stop();
    } else {
      // 页面重新可见时，重新加载数据并启动倒计时
      clearChannelsCache();
      loadChannels(filters.channelType);
      startCooldownSSE();
      AutoTestTimer.init();
    }
  });
});

// 初始化渠道类型 Tab 切换（不包含"全部"选项）
async function initChannelTypeTabs(initialType) {
  const container = document.getElementById('channelTypeTabs');
  if (!container) return;

  const types = await window.ChannelTypeManager.getChannelTypes();

  // 渠道类型图标映射
  const typeIcons = {
    'anthropic': '🟠', // Claude 橙色
    'codex': '⚪',     // Codex 白色/灰色
    'gemini': '🔷',    // Gemini 蓝色菱形
  };

  // 只添加各渠道类型 Tab，不添加"全部"
  types.forEach(type => {
    const tab = document.createElement('button');
    tab.className = 'channel-type-tab' + (type.value === initialType ? ' active' : '');
    tab.dataset.type = type.value;
    tab.title = type.description || type.display_name;

    const icon = typeIcons[type.value] || '🔘';
    tab.innerHTML = `
      <span class="channel-type-tab-icon">${icon}</span>
      <span>${type.display_name}</span>
    `;

    tab.addEventListener('click', () => switchChannelType(type.value));
    container.appendChild(tab);
  });
}

// 切换渠道类型（Tab 切换）
function switchChannelType(type) {
  // 更新 Tab 激活状态
  const tabs = document.querySelectorAll('.channel-type-tab');
  tabs.forEach(tab => {
    if (tab.dataset.type === type) {
      tab.classList.add('active');
    } else {
      tab.classList.remove('active');
    }
  });

  // 更新筛选器并加载渠道
  filters.channelType = type;
  filters.model = 'all';
  const modelFilter = document.getElementById('modelFilter');
  if (modelFilter) {
    modelFilter.value = 'all';
  }
  saveChannelsFilters();
  loadChannels(type);
}

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    closeModal();
    closeDeleteModal();
    closeTestModal();
    closeKeyImportModal();
  }
});
