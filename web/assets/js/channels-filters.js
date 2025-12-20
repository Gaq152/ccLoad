// Filter channels based on current filters
function filterChannels() {
  const filtered = channels.filter(channel => {
    if (filters.search && !channel.name.toLowerCase().includes(filters.search.toLowerCase())) {
      return false;
    }

    // ID 前缀匹配（支持逗号分隔多个值，每个值都是前缀匹配）
    if (filters.id) {
      const idStr = filters.id.trim();
      if (idStr) {
        const prefixes = idStr.split(',').map(id => id.trim()).filter(id => id);
        if (prefixes.length > 0) {
          const channelIdStr = String(channel.id);
          // 任意一个前缀匹配即可
          const matched = prefixes.some(prefix => channelIdStr.startsWith(prefix));
          if (!matched) {
            return false;
          }
        }
      }
    }

    if (filters.channelType !== 'all') {
      const channelType = channel.channel_type || 'anthropic';
      if (channelType !== filters.channelType) {
        return false;
      }
    }

    if (filters.status !== 'all') {
      if (filters.status === 'enabled' && !channel.enabled) return false;
      if (filters.status === 'disabled' && channel.enabled) return false;
      if (filters.status === 'cooldown' && !(channel.cooldown_remaining_ms > 0)) return false;
    }

    if (filters.model !== 'all' && !channel.models.includes(filters.model)) {
      return false;
    }

    return true;
  });

  filtered.sort((a, b) => {
    if (b.priority !== a.priority) {
      return b.priority - a.priority;
    }
    const typeA = (a.channel_type || 'anthropic').toLowerCase();
    const typeB = (b.channel_type || 'anthropic').toLowerCase();
    if (typeA !== typeB) {
      return typeA.localeCompare(typeB);
    }
    return a.name.localeCompare(b.name);
  });

  renderChannels(filtered);
  updateFilterInfo(filtered.length, channels.length);

  // 渲染完成后，检查并启动冷却倒计时
  checkAndStartCooldownCountdown();

  // 从缓存更新用量徽章（筛选后 DOM 重新渲染，需要重新应用缓存数据）
  if (window.QuotaManager) {
    window.QuotaManager.updateBadgesFromCache();
  }
}

// Update filter info display
function updateFilterInfo(filtered, total) {
  document.getElementById('filteredCount').textContent = filtered;
  document.getElementById('totalCount').textContent = total;
}

// Update model filter options
function updateModelOptions() {
  const modelSet = new Set();
  channels.forEach(channel => {
    if (Array.isArray(channel.models)) {
      channel.models.forEach(model => modelSet.add(model));
    }
  });
  
  const modelFilter = document.getElementById('modelFilter');
  const currentValue = modelFilter.value;
  modelFilter.innerHTML = '<option value="all">所有模型</option>';
  
  Array.from(modelSet).sort().forEach(model => {
    const option = document.createElement('option');
    option.value = model;
    option.textContent = model;
    modelFilter.appendChild(option);
  });
  
  modelFilter.value = currentValue;
}

// Setup filter event listeners
function setupFilterListeners() {
  const searchInput = document.getElementById('searchInput');
  const clearSearchBtn = document.getElementById('clearSearchBtn');

  const debouncedFilter = debounce(() => {
    filters.search = searchInput.value;
    filterChannels();
    updateClearButton();
  }, 300);

  searchInput.addEventListener('input', debouncedFilter);

  clearSearchBtn.addEventListener('click', () => {
    searchInput.value = '';
    filters.search = '';
    filterChannels();
    updateClearButton();
    searchInput.focus();
  });

  function updateClearButton() {
    clearSearchBtn.style.opacity = searchInput.value ? '1' : '0';
  }

  const idFilter = document.getElementById('idFilter');
  const debouncedIdFilter = debounce(() => {
    filters.id = idFilter.value;
    filterChannels();
  }, 300);
  idFilter.addEventListener('input', debouncedIdFilter);

  document.getElementById('statusFilter').addEventListener('change', (e) => {
    filters.status = e.target.value;
    if (typeof saveChannelsFilters === 'function') saveChannelsFilters();
    filterChannels();
  });

  document.getElementById('modelFilter').addEventListener('change', (e) => {
    filters.model = e.target.value;
    if (typeof saveChannelsFilters === 'function') saveChannelsFilters();
    filterChannels();
  });

  // 回车键触发筛选
  ['searchInput', 'idFilter'].forEach(id => {
    const el = document.getElementById(id);
    if (el) {
      el.addEventListener('keydown', e => {
        if (e.key === 'Enter') {
          filters.search = document.getElementById('searchInput').value;
          filters.id = document.getElementById('idFilter').value;
          if (typeof saveChannelsFilters === 'function') saveChannelsFilters();
          filterChannels();
        }
      });
    }
  });
}
