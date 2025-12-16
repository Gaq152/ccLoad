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
    enabled: document.getElementById('channelEnabled').checked
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

    // 保存多端点（如果有多个）
    if (channelId && endpoints.length > 1 && typeof saveEndpointsToServer === 'function') {
      await saveEndpointsToServer(channelId);
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

function copyChannel(id, name) {
  const channel = channels.find(c => c.id === id);
  if (!channel) return;

  const copiedName = generateCopyName(name);

  editingChannelId = null;
  currentChannelKeyCooldowns = [];
  document.getElementById('modalTitle').textContent = '复制渠道';
  document.getElementById('channelName').value = copiedName;

  // 设置端点（使用原渠道URL）
  if (typeof setInlineEndpoints === 'function') {
    setInlineEndpoints([channel.url]);
  } else {
    document.getElementById('channelUrl').value = channel.url;
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
