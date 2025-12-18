function showAddModal() {
  editingChannelId = null;
  currentChannelKeyCooldowns = [];

  document.getElementById('modalTitle').textContent = '添加渠道';
  document.getElementById('channelForm').reset();
  document.getElementById('channelEnabled').checked = true;
  document.querySelector('input[name="channelType"][value="anthropic"]').checked = true;
  document.querySelector('input[name="keyStrategy"][value="sequential"]').checked = true;

  // 新建模式隐藏"测速"按钮（需要先保存才能测速）
  document.getElementById('manageEndpointsBtn').style.display = 'none';

  // 重置端点列表
  if (typeof resetInlineEndpoints === 'function') {
    resetInlineEndpoints();
  }

  redirectTableData = [];
  renderRedirectTable();

  inlineKeyTableData = [''];
  inlineKeyVisible = true;
  document.getElementById('inlineEyeIcon').style.display = 'none';
  document.getElementById('inlineEyeOffIcon').style.display = 'block';
  renderInlineKeyTable();

  // 重置用量监控配置
  resetQuotaConfig();

  document.getElementById('channelModal').classList.add('show');
}

async function editChannel(id) {
  const channel = channels.find(c => c.id === id);
  if (!channel) return;

  editingChannelId = id;

  document.getElementById('modalTitle').textContent = '编辑渠道';
  document.getElementById('channelName').value = channel.name;

  // 加载端点列表
  if (typeof loadEndpointsFromServer === 'function') {
    await loadEndpointsFromServer(id, channel.url);
  } else {
    document.getElementById('channelUrl').value = channel.url;
  }

  // 编辑模式显示"测速"按钮
  document.getElementById('manageEndpointsBtn').style.display = 'inline-flex';

  let apiKeys = [];
  try {
    const res = await fetchWithAuth(`/admin/channels/${id}/keys`);
    if (res.ok) {
      const data = await res.json();
      apiKeys = (data.success ? data.data : data) || [];
    }
  } catch (e) {
    console.error('获取API Keys失败', e);
  }

  const now = Date.now();
  currentChannelKeyCooldowns = apiKeys.map((apiKey, index) => {
    const cooldownUntilMs = (apiKey.cooldown_until || 0) * 1000;
    const remainingMs = Math.max(0, cooldownUntilMs - now);
    return {
      key_index: index,
      cooldown_remaining_ms: remainingMs
    };
  });

  inlineKeyTableData = apiKeys.map(k => k.api_key || k);
  if (inlineKeyTableData.length === 0) {
    inlineKeyTableData = [''];
    currentChannelKeyCooldowns = [];
  }

  inlineKeyVisible = true;
  document.getElementById('inlineEyeIcon').style.display = 'none';
  document.getElementById('inlineEyeOffIcon').style.display = 'block';
  renderInlineKeyTable();

  const channelType = channel.channel_type || 'anthropic';
  await window.ChannelTypeManager.renderChannelTypeRadios('channelTypeRadios', channelType);
  const keyStrategy = channel.key_strategy || 'sequential';
  const strategyRadio = document.querySelector(`input[name="keyStrategy"][value="${keyStrategy}"]`);
  if (strategyRadio) {
    strategyRadio.checked = true;
  }
  document.getElementById('channelPriority').value = channel.priority;
  document.getElementById('channelModels').value = channel.models.join(',');
  document.getElementById('channelEnabled').checked = channel.enabled;

  const modelRedirects = channel.model_redirects || {};
  redirectTableData = jsonToRedirectTable(modelRedirects);
  renderRedirectTable();

  // 加载用量监控配置
  loadQuotaConfig(channel.quota_config);

  document.getElementById('channelModal').classList.add('show');

  // 启动冷却倒计时（包括 Key 冷却）
  checkAndStartCooldownCountdown();
}

function closeModal() {
  document.getElementById('channelModal').classList.remove('show');
  editingChannelId = null;
}

async function saveChannel(event) {
  event.preventDefault();

  const validKeys = inlineKeyTableData.filter(k => k && k.trim());
  if (validKeys.length === 0) {
    alert('请至少添加一个有效的API Key');
    return;
  }

  document.getElementById('channelApiKey').value = validKeys.join(',');

  // 获取端点列表
  const endpoints = typeof getInlineEndpoints === 'function' ? getInlineEndpoints() : [];
  const primaryUrl = endpoints[0] || document.getElementById('channelUrl').value.trim();

  if (!primaryUrl) {
    if (window.showError) showError('请至少添加一个API URL');
    return;
  }

  const modelRedirects = redirectTableToJSON();

  const channelType = document.querySelector('input[name="channelType"]:checked')?.value || 'anthropic';
  const keyStrategy = document.querySelector('input[name="keyStrategy"]:checked')?.value || 'sequential';

  const formData = {
    name: document.getElementById('channelName').value.trim(),
    url: primaryUrl, // 使用第一个端点作为主URL
    api_key: validKeys.join(','),
    channel_type: channelType,
    key_strategy: keyStrategy,
    priority: parseInt(document.getElementById('channelPriority').value) || 0,
    models: document.getElementById('channelModels').value.split(',').map(m => m.trim()).filter(m => m),
    model_redirects: modelRedirects,
    enabled: document.getElementById('channelEnabled').checked,
    quota_config: getQuotaConfig()
  };

  if (!formData.name || !formData.url || !formData.api_key || formData.models.length === 0) {
    if (window.showError) showError('请填写所有必填字段');
    return;
  }

  try {
    let res;
    let channelId = editingChannelId;

    if (editingChannelId) {
      res = await fetchWithAuth(`/admin/channels/${editingChannelId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(formData)
      });
    } else {
      res = await fetchWithAuth('/admin/channels', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(formData)
      });
    }

    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || 'HTTP ' + res.status);
    }

    // 新建时获取返回的渠道ID
    if (!editingChannelId) {
      const result = await res.json();
      channelId = result.data?.id || result.id;
    }

    // 保存端点（始终同步端点表，确保数据一致性）
    if (channelId && endpoints.length > 0 && typeof saveEndpointsToServer === 'function') {
      await saveEndpointsToServer(channelId);
    }

    // 立即更新本地 channels 数组，确保再次打开编辑框时显示最新数据
    if (editingChannelId) {
      const ch = channels.find(c => c.id === editingChannelId);
      if (ch) {
        Object.assign(ch, formData);
      }
    }

    closeModal();
    clearChannelsCache();
    await loadChannels(filters.channelType);
    if (window.showSuccess) showSuccess(editingChannelId ? '渠道已更新' : '渠道已添加');
  } catch (e) {
    console.error('保存渠道失败', e);
    if (window.showError) showError('保存失败: ' + e.message);
  }
}

function deleteChannel(id, name) {
  deletingChannelId = id;
  document.getElementById('deleteChannelName').textContent = name;
  document.getElementById('deleteModal').classList.add('show');
}

function closeDeleteModal() {
  document.getElementById('deleteModal').classList.remove('show');
  deletingChannelId = null;
}

async function confirmDelete() {
  if (!deletingChannelId) return;

  try {
    const res = await fetchWithAuth(`/admin/channels/${deletingChannelId}`, {
      method: 'DELETE'
    });

    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || 'HTTP ' + res.status);
    }

    closeDeleteModal();
    clearChannelsCache();
    await loadChannels(filters.channelType);
    if (window.showSuccess) showSuccess('渠道已删除');
  } catch (e) {
    console.error('删除渠道失败', e);
    if (window.showError) showError('删除失败: ' + e.message);
  }
}

async function toggleChannel(id, enabled) {
  try {
    const res = await fetchWithAuth(`/admin/channels/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled })
    });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    clearChannelsCache();
    await loadChannels(filters.channelType);
    if (window.showSuccess) showSuccess(enabled ? '渠道已启用' : '渠道已禁用');
  } catch (e) {
    console.error('切换失败', e);
    if (window.showError) showError('操作失败');
  }
}

async function copyChannel(id, name) {
  const channel = channels.find(c => c.id === id);
  if (!channel) return;

  const copiedName = generateCopyName(name);

  editingChannelId = null;
  currentChannelKeyCooldowns = [];
  document.getElementById('modalTitle').textContent = '复制渠道';
  document.getElementById('channelName').value = copiedName;

  // 获取源渠道的端点列表并复制
  let endpointUrls = [channel.url]; // 默认使用渠道URL
  try {
    const res = await fetchWithAuth(`/admin/channels/${id}/endpoints`);
    if (res.ok) {
      const data = await res.json();
      // API 返回格式: {data: [...], auto_select_endpoint: bool}
      const endpoints = Array.isArray(data) ? data : (data.data || []);
      if (endpoints.length > 0) {
        // 确保 active 端点在首位（保持原渠道的 active 优先级）
        const sorted = [...endpoints].sort((a, b) => {
          if (a.is_active && !b.is_active) return -1;
          if (!a.is_active && b.is_active) return 1;
          return (a.sort_order || 0) - (b.sort_order || 0);
        });
        endpointUrls = sorted.map(ep => ep.url);
      }
    }
  } catch (e) {
    console.error('获取源渠道端点失败，使用默认URL', e);
  }

  // 设置端点（使用完整的端点列表）
  if (typeof setInlineEndpoints === 'function') {
    setInlineEndpoints(endpointUrls);
  } else {
    document.getElementById('channelUrl').value = endpointUrls[0] || channel.url;
  }

  // 复制模式隐藏"测速"按钮（复制是新建操作）
  document.getElementById('manageEndpointsBtn').style.display = 'none';

  inlineKeyTableData = parseKeys(channel.api_key);
  if (inlineKeyTableData.length === 0) {
    inlineKeyTableData = [''];
  }

  inlineKeyVisible = true;
  document.getElementById('inlineEyeIcon').style.display = 'none';
  document.getElementById('inlineEyeOffIcon').style.display = 'block';
  renderInlineKeyTable();

  const channelType = channel.channel_type || 'anthropic';
  const radioButton = document.querySelector(`input[name="channelType"][value="${channelType}"]`);
  if (radioButton) {
    radioButton.checked = true;
  }
  const keyStrategy = channel.key_strategy || 'sequential';
  const strategyRadio = document.querySelector(`input[name="keyStrategy"][value="${keyStrategy}"]`);
  if (strategyRadio) {
    strategyRadio.checked = true;
  }
  document.getElementById('channelPriority').value = channel.priority;
  document.getElementById('channelModels').value = channel.models.join(',');
  document.getElementById('channelEnabled').checked = true;

  const modelRedirects = channel.model_redirects || {};
  redirectTableData = jsonToRedirectTable(modelRedirects);
  renderRedirectTable();

  document.getElementById('channelModal').classList.add('show');
}

function generateCopyName(originalName) {
  const copyPattern = /^(.+?)(?:\s*-\s*复制(?:\s*(\d+))?)?$/;
  const match = originalName.match(copyPattern);

  if (!match) {
    return originalName + ' - 复制';
  }

  const baseName = match[1];
  const copyNumber = match[2] ? parseInt(match[2]) + 1 : 1;

  const proposedName = copyNumber === 1 ? `${baseName} - 复制` : `${baseName} - 复制 ${copyNumber}`;

  const existingNames = channels.map(c => c.name.toLowerCase());
  if (existingNames.includes(proposedName.toLowerCase())) {
    return generateCopyName(proposedName);
  }

  return proposedName;
}

function addRedirectRow() {
  redirectTableData.push({ from: '', to: '' });
  renderRedirectTable();
  
  setTimeout(() => {
    const tbody = document.getElementById('redirectTableBody');
    const lastRow = tbody.lastElementChild;
    if (lastRow) {
      const firstInput = lastRow.querySelector('input');
      if (firstInput) firstInput.focus();
    }
  }, 50);
}

function deleteRedirectRow(index) {
  redirectTableData.splice(index, 1);
  renderRedirectTable();
}

function updateRedirectRow(index, field, value) {
  if (redirectTableData[index]) {
    redirectTableData[index][field] = value.trim();
  }
}

/**
 * 使用模板引擎创建重定向行元素
 * @param {Object} redirect - 重定向数据
 * @param {number} index - 索引
 * @returns {HTMLElement|null} 表格行元素
 */
function createRedirectRow(redirect, index) {
  const rowData = {
    index: index,
    from: redirect.from || '',
    to: redirect.to || ''
  };

  const row = TemplateEngine.render('tpl-redirect-row', rowData);
  if (!row) {
    // 降级：模板不存在时使用原有方式
    console.warn('[Channels] Template tpl-redirect-row not found, using legacy rendering');
    return createRedirectRowLegacy(redirect, index);
  }

  return row;
}

/**
 * 初始化重定向表格事件委托 (替代inline onchange/onclick)
 */
function initRedirectTableEventDelegation() {
  const tbody = document.getElementById('redirectTableBody');
  if (!tbody || tbody.dataset.delegated) return;

  tbody.dataset.delegated = 'true';

  // 处理输入框变更
  tbody.addEventListener('change', (e) => {
    const fromInput = e.target.closest('.redirect-from-input');
    if (fromInput) {
      const index = parseInt(fromInput.dataset.index);
      updateRedirectRow(index, 'from', fromInput.value);
      return;
    }

    const toInput = e.target.closest('.redirect-to-input');
    if (toInput) {
      const index = parseInt(toInput.dataset.index);
      updateRedirectRow(index, 'to', toInput.value);
    }
  });

  // 处理删除按钮点击
  tbody.addEventListener('click', (e) => {
    const deleteBtn = e.target.closest('.redirect-delete-btn');
    if (deleteBtn) {
      const index = parseInt(deleteBtn.dataset.index);
      deleteRedirectRow(index);
    }
  });

  // 处理删除按钮悬停样式
  tbody.addEventListener('mouseover', (e) => {
    const btn = e.target.closest('.redirect-delete-btn');
    if (btn) {
      btn.style.background = 'var(--error-50)';
      btn.style.borderColor = 'var(--error-500)';
    }
  });

  tbody.addEventListener('mouseout', (e) => {
    const btn = e.target.closest('.redirect-delete-btn');
    if (btn) {
      btn.style.background = 'white';
      btn.style.borderColor = 'var(--error-300)';
    }
  });
}

function renderRedirectTable() {
  const tbody = document.getElementById('redirectTableBody');
  const countSpan = document.getElementById('redirectCount');

  const validCount = redirectTableData.filter(r => r.from && r.to).length;
  countSpan.textContent = validCount;

  // 初始化事件委托（仅一次）
  initRedirectTableEventDelegation();

  if (redirectTableData.length === 0) {
    const emptyRow = TemplateEngine.render('tpl-redirect-empty', {
      message: '暂无重定向规则，点击"添加"按钮创建'
    });
    if (emptyRow) {
      tbody.innerHTML = '';
      tbody.appendChild(emptyRow);
    } else {
      // 降级：模板不存在时使用简单HTML
      tbody.innerHTML = '<tr><td colspan="3" style="padding: 20px; text-align: center; color: var(--neutral-500);">暂无重定向规则，点击"添加"按钮创建</td></tr>';
    }
    return;
  }

  // 使用DocumentFragment优化批量DOM操作
  const fragment = document.createDocumentFragment();
  redirectTableData.forEach((redirect, index) => {
    const row = createRedirectRow(redirect, index);
    if (row) fragment.appendChild(row);
  });

  tbody.innerHTML = '';
  tbody.appendChild(fragment);
}

function redirectTableToJSON() {
  const result = {};
  redirectTableData.forEach(redirect => {
    if (redirect.from && redirect.to) {
      result[redirect.from] = redirect.to;
    }
  });
  return result;
}

function jsonToRedirectTable(json) {
  if (!json || typeof json !== 'object') return [];
  return Object.entries(json).map(([from, to]) => ({ from, to }));
}

async function fetchModelsFromAPI() {
  const channelUrl = document.getElementById('channelUrl').value.trim();
  const channelType = document.querySelector('input[name="channelType"]:checked')?.value || 'anthropic';
  const firstValidKey = inlineKeyTableData
    .map(key => (key || '').trim())
    .filter(Boolean)[0];

  if (!channelUrl) {
    if (window.showError) {
      showError('请先填写API URL');
    } else {
      alert('请先填写API URL');
    }
    return;
  }

  if (!firstValidKey) {
    if (window.showError) {
      showError('请至少添加一个API Key');
    } else {
      alert('请至少添加一个API Key');
    }
    return;
  }

  const endpoint = '/admin/channels/models/fetch';
  const fetchOptions = {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      channel_type: channelType,
      url: channelUrl,
      api_key: firstValidKey
    })
  };

  const modelsTextarea = document.getElementById('channelModels');
  const originalValue = modelsTextarea.value;
  const originalPlaceholder = modelsTextarea.placeholder;

  modelsTextarea.disabled = true;
  modelsTextarea.placeholder = '正在获取模型列表...';

  try {
    const res = await fetchWithAuth(endpoint, fetchOptions);

    if (!res.ok) {
      const errorData = await res.json().catch(() => ({}));
      throw new Error(errorData.error || `HTTP ${res.status}`);
    }

    const response = await res.json();

    if (response.success === false) {
      throw new Error(response.error || '获取模型列表失败');
    }

    const data = response.data || response;

    if (!data.models || data.models.length === 0) {
      throw new Error('未获取到任何模型');
    }

    const existingModels = originalValue.split(',').map(m => m.trim()).filter(m => m);
    const allModels = [...new Set([...existingModels, ...data.models])];

    modelsTextarea.value = allModels.join(',');

    const source = data.source === 'api' ? '从API获取' : '预定义列表';
    if (window.showSuccess) {
      showSuccess(`成功获取 ${data.models.length} 个模型 (${source})`);
    } else {
      alert(`成功获取 ${data.models.length} 个模型 (${source})`);
    }

  } catch (error) {
    console.error('获取模型列表失败', error);

    modelsTextarea.value = originalValue;

    if (window.showError) {
      showError('获取模型列表失败: ' + error.message);
    } else {
      alert('获取模型列表失败: ' + error.message);
    }
  } finally {
    modelsTextarea.disabled = false;
    modelsTextarea.placeholder = originalPlaceholder;
  }
}

function clearAllModels() {
  if (confirm('确定要清除所有模型吗？此操作不可恢复！')) {
    const modelsTextarea = document.getElementById('channelModels');
    modelsTextarea.value = '';
    modelsTextarea.focus();
  }
}

/**
 * 从编辑弹窗打开端点管理
 * 使用当前正在编辑的渠道 ID
 */
function openEndpointModalFromEdit() {
  if (!editingChannelId) {
    if (window.showError) {
      showError('请先保存渠道后再管理端点');
    }
    return;
  }

  // 调用端点管理模块的打开函数
  if (typeof window.openEndpointModal === 'function') {
    window.openEndpointModal(editingChannelId);
  } else {
    console.error('openEndpointModal 函数未定义');
    if (window.showError) {
      showError('端点管理功能未加载');
    }
  }
}

// ==================== 用量监控配置 ====================

// 用量监控请求头数据
let quotaHeadersData = [];

/**
 * 切换用量配置面板显示
 */
function toggleQuotaConfig() {
  const enabled = document.getElementById('quotaEnabled').checked;
  const panel = document.getElementById('quotaConfigPanel');
  const testBtn = document.getElementById('quotaTestBtn');

  panel.style.display = enabled ? 'block' : 'none';
  testBtn.style.display = enabled ? 'inline-flex' : 'none';
}

/**
 * 重置用量配置
 */
function resetQuotaConfig() {
  document.getElementById('quotaEnabled').checked = false;
  document.getElementById('quotaConfigPanel').style.display = 'none';
  document.getElementById('quotaTestBtn').style.display = 'none';
  document.getElementById('quotaUrl').value = '';
  document.getElementById('quotaMethod').value = 'GET';
  document.getElementById('quotaInterval').value = '300';
  document.getElementById('quotaExtractor').value = '';
  quotaHeadersData = [];
  renderQuotaHeaders();
}

/**
 * 加载用量配置
 */
function loadQuotaConfig(config) {
  if (!config) {
    resetQuotaConfig();
    return;
  }

  document.getElementById('quotaEnabled').checked = config.enabled || false;
  document.getElementById('quotaUrl').value = config.request_url || '';
  document.getElementById('quotaMethod').value = config.request_method || 'GET';
  document.getElementById('quotaInterval').value = String(config.interval_seconds || 300);
  document.getElementById('quotaExtractor').value = config.extractor_script || '';

  // 加载请求头
  quotaHeadersData = [];
  if (config.request_headers) {
    for (const [key, value] of Object.entries(config.request_headers)) {
      quotaHeadersData.push({ key, value });
    }
  }
  renderQuotaHeaders();

  // 切换面板显示
  toggleQuotaConfig();
}

/**
 * 获取用量配置
 */
function getQuotaConfig() {
  const enabled = document.getElementById('quotaEnabled').checked;

  if (!enabled) {
    return null;
  }

  const headers = {};
  quotaHeadersData.forEach(h => {
    if (h.key && h.key.trim()) {
      headers[h.key.trim()] = h.value || '';
    }
  });

  return {
    enabled: true,
    request_url: document.getElementById('quotaUrl').value.trim(),
    request_method: document.getElementById('quotaMethod').value,
    request_headers: headers,
    extractor_script: document.getElementById('quotaExtractor').value,
    interval_seconds: parseInt(document.getElementById('quotaInterval').value) || 300
  };
}

/**
 * 添加用量请求头
 */
function addQuotaHeader() {
  quotaHeadersData.push({ key: '', value: '' });
  renderQuotaHeaders();

  // 聚焦到新行的第一个输入框
  setTimeout(() => {
    const container = document.getElementById('quotaHeadersContainer');
    const inputs = container.querySelectorAll('input');
    if (inputs.length >= 2) {
      inputs[inputs.length - 2].focus();
    }
  }, 50);
}

/**
 * 删除用量请求头
 */
function deleteQuotaHeader(index) {
  quotaHeadersData.splice(index, 1);
  renderQuotaHeaders();
}

/**
 * 更新用量请求头
 */
function updateQuotaHeader(index, field, value) {
  if (quotaHeadersData[index]) {
    quotaHeadersData[index][field] = value;
  }
}

/**
 * 渲染用量请求头列表
 */
function renderQuotaHeaders() {
  const container = document.getElementById('quotaHeadersContainer');

  if (quotaHeadersData.length === 0) {
    container.innerHTML = '<div style="color: var(--neutral-400); font-size: 12px; padding: 8px; text-align: center;">暂无请求头</div>';
    return;
  }

  container.innerHTML = quotaHeadersData.map((h, i) => `
    <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 6px;">
      <input type="text" class="form-input" placeholder="Header名称" value="${escapeHtml(h.key)}"
             onchange="updateQuotaHeader(${i}, 'key', this.value)"
             style="flex: 1; font-size: 12px; padding: 6px 8px;">
      <input type="text" class="form-input" placeholder="值" value="${escapeHtml(h.value)}"
             onchange="updateQuotaHeader(${i}, 'value', this.value)"
             style="flex: 2; font-size: 12px; padding: 6px 8px;">
      <button type="button" onclick="deleteQuotaHeader(${i})"
              style="width: 24px; height: 24px; border-radius: 4px; border: 1px solid var(--error-300);
                     background: white; color: var(--error-500); cursor: pointer; padding: 0;
                     display: flex; align-items: center; justify-content: center;"
              onmouseover="this.style.background='var(--error-50)'; this.style.borderColor='var(--error-500)';"
              onmouseout="this.style.background='white'; this.style.borderColor='var(--error-300)';">
        <svg width="12" height="12" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
          <path d="M4 4L12 12M4 12L12 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
        </svg>
      </button>
    </div>
  `).join('');
}

/**
 * 测试用量获取（支持新建渠道和编辑渠道）
 */
async function testQuotaFetch() {
  const testBtn = document.getElementById('quotaTestBtn');
  const originalText = testBtn.textContent;
  testBtn.disabled = true;
  testBtn.textContent = '测试中...';

  try {
    // 获取表单中当前填写的配置
    const config = getQuotaConfig();
    if (!config || !config.request_url) {
      throw new Error('请填写请求URL');
    }
    if (!config.extractor_script) {
      throw new Error('请填写提取器脚本');
    }

    // 使用 channelId=0 表示测试模式，或使用已有的渠道ID
    const channelId = editingChannelId || 0;

    // 调用后端代理API，发送表单中的配置
    const res = await fetchWithAuth(`/admin/channels/${channelId}/quota/fetch`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ quota_config: config })
    });

    const result = await res.json();

    if (!result.success) {
      throw new Error(result.error || '请求失败');
    }

    // 执行提取器脚本
    try {
      // 解析 body（后端返回的是 JSON 字符串）
      let responseBody = result.body;
      if (typeof responseBody === 'string') {
        try {
          responseBody = JSON.parse(responseBody);
        } catch (parseErr) {
          console.warn('body 解析失败，保持原样');
        }
      }

      const extractorFn = new Function('response', `return (${config.extractor_script})(response)`);
      const quotaData = extractorFn(responseBody);

      if (quotaData && quotaData.isValid) {
        if (window.showSuccess) {
          showSuccess(`测试成功！余额: ${quotaData.remaining} ${quotaData.unit || ''}`);
        }
      } else {
        if (window.showWarning) {
          showWarning('提取器返回无效数据，请检查脚本逻辑');
        } else if (window.showError) {
          showError('提取器返回无效数据');
        }
      }
    } catch (extractError) {
      console.error('提取器执行失败', extractError);
      if (window.showWarning) {
        showWarning('请求成功 (HTTP ' + result.status_code + ')，但提取器执行失败: ' + extractError.message);
      } else if (window.showError) {
        showError('提取器执行失败: ' + extractError.message);
      }
    }

  } catch (error) {
    console.error('测试用量获取失败', error);
    if (window.showError) showError('测试失败: ' + error.message);
  } finally {
    testBtn.disabled = false;
    testBtn.textContent = originalText;
  }
}

/**
 * HTML转义（防止XSS，包括属性注入）
 */
function escapeHtml(text) {
  // 注意：不能用 !text 判断，否则数值 0 会被当作 false
  if (text === null || text === undefined) return '';
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

/**
 * 用量监控预设模板
 */
const QUOTA_TEMPLATES = {
  // NEWAPI 模板
  newapi: {
    name: 'NEWAPI',
    endpoint: '/api/user/self',  // 只存路径，自动拼接渠道URL
    method: 'GET',
    headers: [
      { key: 'Content-Type', value: 'application/json' },
      { key: 'Authorization', value: 'Bearer 你的Token' },
      { key: 'New-Api-User', value: '你的用户ID' }
    ],
    extractor: `function(response) {
  const data = typeof response === 'string' ? JSON.parse(response) : response;
  if (data.success && data.data) {
    return {
      isValid: true,
      remaining: data.data.quota / 500000,
      unit: "USD"
    };
  }
  return { isValid: false, error: data.message || "查询失败" };
}`
  },

  // Veloera 模板
  veloera: {
    name: 'Veloera',
    endpoint: '/api/user/self',
    method: 'GET',
    headers: [
      { key: 'Content-Type', value: 'application/json' },
      { key: 'Authorization', value: 'Bearer 你的Token' },
      { key: 'Veloera-User', value: '你的用户ID' }
    ],
    extractor: `function(response) {
  const data = typeof response === 'string' ? JSON.parse(response) : response;
  if (data.success && data.data) {
    return {
      isValid: true,
      remaining: data.data.quota / 500000,
      unit: "USD"
    };
  }
  return { isValid: false, error: data.message || "查询失败" };
}`
  },

  // OneAPI 模板
  oneapi: {
    name: 'OneAPI',
    endpoint: '/api/user/self',
    method: 'GET',
    headers: [
      { key: 'Content-Type', value: 'application/json' },
      { key: 'Authorization', value: 'Bearer 你的Token' }
    ],
    extractor: `function(response) {
  const data = typeof response === 'string' ? JSON.parse(response) : response;
  if (data.success && data.data) {
    // OneAPI 的 quota 单位是 500000 = 1 USD
    return {
      isValid: true,
      remaining: data.data.quota / 500000,
      unit: "USD"
    };
  }
  return { isValid: false, error: data.message || "查询失败" };
}`
  }
};

/**
 * 应用用量监控模板
 * @param {string} templateKey - 模板键名
 */
function applyQuotaTemplate(templateKey) {
  const template = QUOTA_TEMPLATES[templateKey];
  if (!template) {
    console.error('未知模板:', templateKey);
    return;
  }

  // 从渠道URL自动生成用量查询URL
  let quotaUrl = '';

  // 优先使用端点列表的第一个URL，否则使用channelUrl输入框
  const endpoints = typeof getInlineEndpoints === 'function' ? getInlineEndpoints() : [];
  let baseUrl = endpoints[0] || document.getElementById('channelUrl')?.value?.trim() || '';

  if (baseUrl) {
    // 移除末尾的斜杠和可能的 /v1 路径
    baseUrl = baseUrl.replace(/\/+$/, '').replace(/\/v1$/, '');
    quotaUrl = baseUrl + template.endpoint;
  } else {
    // 没有渠道URL时使用占位符
    quotaUrl = 'https://你的域名' + template.endpoint;
  }

  // 填充URL和方法
  document.getElementById('quotaUrl').value = quotaUrl;
  document.getElementById('quotaMethod').value = template.method;

  // 填充请求头
  quotaHeadersData = template.headers.map(h => ({ key: h.key, value: h.value }));
  renderQuotaHeaders();

  // 填充提取器脚本
  document.getElementById('quotaExtractor').value = template.extractor;

  if (window.showSuccess) {
    const msg = baseUrl
      ? `已应用 ${template.name} 模板，请填写Token和用户ID`
      : `已应用 ${template.name} 模板，请先填写渠道URL`;
    showSuccess(msg);
  }
}
