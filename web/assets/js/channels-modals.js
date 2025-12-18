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

  // 初始化 Codex OAuth 区块（默认隐藏）
  handleChannelTypeChange('anthropic');
  updateCodexTokenUI(null);
  initChannelTypeEventListener();
  toggleCodexAuthMode('oauth');

  // 添加 OAuth 回调消息监听
  window.addEventListener('message', handleCodexOAuthMessage);

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

  // 初始化渠道类型相关 UI（Codex OAuth 区块）
  initChannelTypeEventListener();

  // Codex 渠道：根据预设类型设置 UI
  if (channelType === 'codex') {
    // 从后端获取预设类型（新字段），如果没有则根据数据推断
    let preset = channel.preset || '';

    // 如果后端没有返回 preset（兼容旧数据），根据 OAuth Token 存在与否推断
    if (!preset && apiKeys.length > 0) {
      const firstKey = apiKeys[0];
      // 检查是否有 OAuth Token（新字段方式）
      if (firstKey?.access_token) {
        preset = 'official';
      } else {
        // 检查是否是 JSON 格式（旧方式，存在 api_key 字段中）
        const apiKeyStr = firstKey?.api_key || firstKey;
        if (apiKeyStr && typeof apiKeyStr === 'string' && apiKeyStr.trim().startsWith('{')) {
          preset = 'official';
        } else {
          preset = 'custom';
        }
      }
    }

    // 默认官方预设
    if (!preset) preset = 'official';

    // 设置预设单选按钮
    const presetRadio = document.querySelector(`input[name="channelPreset"][value="${preset}"]`);
    if (presetRadio) {
      presetRadio.checked = true;
    }

    // 应用渠道类型切换（会显示预设选项并应用当前预设）
    handleChannelTypeChange(channelType);

    // 加载 OAuth Token UI
    if (preset === 'official' && apiKeys.length > 0) {
      const firstKey = apiKeys[0];
      // 优先使用新字段
      if (firstKey?.access_token) {
        const token = {
          access_token: firstKey.access_token,
          id_token: firstKey.id_token || '',
          refresh_token: firstKey.refresh_token || '',
          expires_at: firstKey.token_expires_at || 0
        };
        updateCodexTokenUI(token);
      } else {
        // 兼容旧数据：从 api_key 字段解析 JSON
        const apiKeyStr = firstKey?.api_key || firstKey;
        if (apiKeyStr && typeof apiKeyStr === 'string' && apiKeyStr.trim().startsWith('{')) {
          try {
            const token = JSON.parse(apiKeyStr);
            updateCodexTokenUI(token);
          } catch (e) {
            console.warn('解析 Codex Token 失败', e);
            updateCodexTokenUI(null);
          }
        } else {
          updateCodexTokenUI(null);
        }
      }
    } else {
      updateCodexTokenUI(null);
    }
  } else {
    // 非 Codex 渠道
    handleChannelTypeChange(channelType);
    updateCodexTokenUI(null);
  }

  // 添加 OAuth 回调消息监听
  window.addEventListener('message', handleCodexOAuthMessage);

  document.getElementById('channelModal').classList.add('show');

  // 启动冷却倒计时（包括 Key 冷却）
  checkAndStartCooldownCountdown();
}

function closeModal() {
  document.getElementById('channelModal').classList.remove('show');
  editingChannelId = null;

  // 移除 OAuth 回调消息监听
  window.removeEventListener('message', handleCodexOAuthMessage);
}

async function saveChannel(event) {
  event.preventDefault();

  const channelType = document.querySelector('input[name="channelType"]:checked')?.value || 'anthropic';

  // 预设类型（仅 Codex 渠道）
  let preset = '';
  let accessToken = '';
  let idToken = '';
  let refreshToken = '';
  let tokenExpiresAt = 0;

  // Codex 渠道：根据预设类型决定认证方式
  let validKeys;
  if (channelType === 'codex') {
    preset = document.querySelector('input[name="channelPreset"]:checked')?.value || 'custom';

    if (preset === 'official') {
      // 官方预设：使用 OAuth Token
      const tokenJson = document.getElementById('channelApiKey').value;
      if (!tokenJson || !tokenJson.startsWith('{')) {
        if (window.showError) {
          showError('请先完成 Codex OAuth 授权');
        } else {
          alert('请先完成 Codex OAuth 授权');
        }
        return;
      }

      // 解析 OAuth Token JSON
      try {
        const token = JSON.parse(tokenJson);
        accessToken = token.access_token || '';
        idToken = token.id_token || '';
        refreshToken = token.refresh_token || '';
        tokenExpiresAt = token.expires_at || 0;
      } catch (e) {
        if (window.showError) {
          showError('OAuth Token 格式错误');
        }
        return;
      }

      validKeys = []; // 官方预设不使用 api_key 字段
    } else {
      // 自定义预设：使用 API Key
      validKeys = inlineKeyTableData.filter(k => k && k.trim());
      if (validKeys.length === 0) {
        if (window.showError) {
          showError('请至少添加一个 API Key');
        } else {
          alert('请至少添加一个 API Key');
        }
        return;
      }
    }
  } else {
    // 其他渠道：使用标准 API Key 列表
    validKeys = inlineKeyTableData.filter(k => k && k.trim());
    if (validKeys.length === 0) {
      alert('请至少添加一个有效的API Key');
      return;
    }
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

  // channelType 已在函数开头定义
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
    quota_config: getQuotaConfig(),
    // Codex 预设相关字段
    preset: preset,
    access_token: accessToken,
    id_token: idToken,
    refresh_token: refreshToken,
    token_expires_at: tokenExpiresAt
  };

  // 验证必填字段（官方预设使用 OAuth，不需要 api_key）
  const needsApiKey = channelType !== 'codex' || preset !== 'official';
  if (!formData.name || !formData.url || formData.models.length === 0) {
    if (window.showError) showError('请填写所有必填字段');
    return;
  }
  if (needsApiKey && !formData.api_key) {
    if (window.showError) showError('请填写API Key');
    return;
  }
  if (preset === 'official' && !accessToken) {
    if (window.showError) showError('请完成OAuth授权');
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

  // 初始化渠道类型相关 UI（Codex OAuth 区块）
  initChannelTypeEventListener();

  // Codex 渠道：复制时保留原有预设
  if (channelType === 'codex') {
    const sourcePreset = channel.preset || 'custom';
    const presetRadio = document.querySelector(`input[name="channelPreset"][value="${sourcePreset}"]`);
    if (presetRadio) {
      presetRadio.checked = true;
    }
  }

  handleChannelTypeChange(channelType);

  // Codex 渠道：根据预设类型决定是否需要重新配置
  if (channelType === 'codex') {
    const sourcePreset = channel.preset || 'custom';
    if (sourcePreset === 'official') {
      // 官方预设：清空 OAuth Token，需要重新授权
      updateCodexTokenUI(null);
      if (window.showWarning) {
        showWarning('Codex 官方预设复制后需要重新进行 OAuth 授权');
      }
    } else {
      // 自定义预设：API Key 已复制，无需额外操作
      updateCodexTokenUI(null);
    }
  } else {
    updateCodexTokenUI(null);
  }

  // 添加 OAuth 回调消息监听
  window.addEventListener('message', handleCodexOAuthMessage);

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

  if (!channelUrl) {
    if (window.showError) {
      showError('请先填写API URL');
    } else {
      alert('请先填写API URL');
    }
    return;
  }

  // 获取认证信息（区分 Codex 官方预设和其他渠道）
  let apiKey = '';
  let accessToken = '';

  if (channelType === 'codex') {
    const preset = document.querySelector('input[name="channelPreset"]:checked')?.value || 'custom';
    if (preset === 'official') {
      // Codex 官方预设：从 OAuth Token 获取 access_token
      const tokenJson = document.getElementById('channelApiKey').value;
      if (tokenJson && tokenJson.startsWith('{')) {
        try {
          const token = JSON.parse(tokenJson);
          accessToken = token.access_token || '';
        } catch (e) {
          console.warn('解析 Codex Token 失败', e);
        }
      }
      if (!accessToken) {
        if (window.showError) {
          showError('请先完成 Codex OAuth 授权');
        } else {
          alert('请先完成 Codex OAuth 授权');
        }
        return;
      }
    } else {
      // Codex 自定义预设：使用 API Key
      apiKey = inlineKeyTableData
        .map(key => (typeof key === 'string' ? key : '').trim())
        .filter(Boolean)[0] || '';
      if (!apiKey) {
        if (window.showError) {
          showError('请至少添加一个API Key');
        } else {
          alert('请至少添加一个API Key');
        }
        return;
      }
    }
  } else {
    // 其他渠道：使用 API Key
    apiKey = inlineKeyTableData
      .map(key => (typeof key === 'string' ? key : '').trim())
      .filter(Boolean)[0] || '';
    if (!apiKey) {
      if (window.showError) {
        showError('请至少添加一个API Key');
      } else {
        alert('请至少添加一个API Key');
      }
      return;
    }
  }

  const endpoint = '/admin/channels/models/fetch';
  const fetchOptions = {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      channel_type: channelType,
      url: channelUrl,
      api_key: apiKey,
      access_token: accessToken
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
  // 检查是否为 HTML 响应（Cloudflare 拦截等）
  if (typeof response === 'string' && response.trim().startsWith('<')) {
    if (response.includes('Just a moment') || response.includes('cf-challenge')) {
      return { isValid: false, error: "被 Cloudflare 拦截，请检查 IP 或稍后重试" };
    }
    return { isValid: false, error: "响应不是 JSON 格式" };
  }
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
  // 检查是否为 HTML 响应（Cloudflare 拦截等）
  if (typeof response === 'string' && response.trim().startsWith('<')) {
    if (response.includes('Just a moment') || response.includes('cf-challenge')) {
      return { isValid: false, error: "被 Cloudflare 拦截，请检查 IP 或稍后重试" };
    }
    return { isValid: false, error: "响应不是 JSON 格式" };
  }
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
  // 检查是否为 HTML 响应（Cloudflare 拦截等）
  if (typeof response === 'string' && response.trim().startsWith('<')) {
    if (response.includes('Just a moment') || response.includes('cf-challenge')) {
      return { isValid: false, error: "被 Cloudflare 拦截，请检查 IP 或稍后重试" };
    }
    return { isValid: false, error: "响应不是 JSON 格式" };
  }
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
  },

  // Codex 官方预设模板（使用 OAuth Token）
  codex: {
    name: 'Codex 官方',
    absoluteUrl: 'https://chatgpt.com/backend-api/wham/usage',  // 绝对 URL
    method: 'GET',
    // headers 动态生成（从 OAuth Token 获取）
    headers: [],
    extractor: `function(response) {
  const data = typeof response === 'string' ? JSON.parse(response) : response;

  // 检查 rate_limit 结构
  if (!data.rate_limit) {
    return { isValid: false, error: "响应格式错误：缺少 rate_limit" };
  }

  const rl = data.rate_limit;
  const primary = rl.primary_window;

  // 只使用 5h 窗口（primary_window）的数据
  if (!primary) {
    return { isValid: false, error: "响应格式错误：缺少 primary_window" };
  }

  const remaining = 100 - primary.used_percent;
  const resetTime = new Date(primary.reset_at * 1000).toLocaleString();

  return {
    isValid: true,
    remaining: remaining,
    unit: '%',
    detail: '重置时间: ' + resetTime,
    limitReached: rl.limit_reached || false
  };
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

  // Codex 官方模板特殊处理
  if (templateKey === 'codex') {
    applyCodexQuotaTemplate(template);
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

/**
 * 应用 Codex 官方用量监控模板
 * 从 OAuth Token 动态生成 headers
 * @param {Object} template - 模板对象
 */
function applyCodexQuotaTemplate(template) {
  // 检查是否为 Codex 官方预设
  const channelType = document.querySelector('input[name="channelType"]:checked')?.value;
  const preset = document.querySelector('input[name="channelPreset"]:checked')?.value;

  if (channelType !== 'codex' || preset !== 'official') {
    if (window.showError) {
      showError('Codex 官方模板仅适用于 Codex 官方预设渠道');
    }
    return;
  }

  // 从 OAuth Token 获取认证信息
  const tokenJson = document.getElementById('channelApiKey').value;
  if (!tokenJson || !tokenJson.startsWith('{')) {
    if (window.showError) {
      showError('请先完成 Codex OAuth 授权');
    }
    return;
  }

  let token;
  try {
    token = JSON.parse(tokenJson);
  } catch (e) {
    if (window.showError) {
      showError('OAuth Token 格式错误');
    }
    return;
  }

  if (!token.access_token) {
    if (window.showError) {
      showError('OAuth Token 缺少 access_token');
    }
    return;
  }

  // 提取 account_id
  const accountId = token.account_id || extractAccountIdFromToken(token.access_token);
  if (!accountId) {
    if (window.showWarning) {
      showWarning('无法从 Token 中提取 account_id，请手动填写');
    }
  }

  // 填充 URL（使用绝对 URL）
  document.getElementById('quotaUrl').value = template.absoluteUrl;
  document.getElementById('quotaMethod').value = template.method;

  // 动态生成请求头
  quotaHeadersData = [
    { key: 'Authorization', value: `Bearer ${token.access_token}` },
    { key: 'chatgpt-account-id', value: accountId || '请手动填写' }
  ];
  renderQuotaHeaders();

  // 填充提取器脚本
  document.getElementById('quotaExtractor').value = template.extractor;

  if (window.showSuccess) {
    showSuccess('已应用 Codex 官方模板，认证信息已自动填充');
  }
}

// ==================== Codex OAuth 授权 ====================

/**
 * 获取渠道类型对应的官方预设标签名称
 */
function getOfficialPresetLabel(type) {
  const labels = {
    'codex': 'OpenAI 官方',
    'anthropic': 'Anthropic 官方',
    'gemini': 'Google 官方'
  };
  return labels[type] || '官方';
}

/**
 * 处理渠道类型切换
 * 控制预设选项、标准 Key 表格和 OAuth 区块的显示
 */
function handleChannelTypeChange(type) {
  const standardKeyContainer = document.getElementById('standardKeyContainer');
  const codexOAuthSection = document.getElementById('codexOAuthSection');
  const codexAuthSwitch = document.getElementById('codexAuthSwitch');
  const channelPresetContainer = document.getElementById('channelPresetContainer');
  const officialPresetLabel = document.getElementById('officialPresetLabel');

  // 所有渠道类型都显示预设选项
  if (channelPresetContainer) channelPresetContainer.style.display = 'block';

  // 更新官方预设的标签名称
  if (officialPresetLabel) {
    officialPresetLabel.textContent = getOfficialPresetLabel(type);
  }

  // 检查是否已选择预设，如果没有则默认选择自定义预设
  const currentPreset = document.querySelector('input[name="channelPreset"]:checked')?.value;
  if (!currentPreset) {
    const customRadio = document.querySelector('input[name="channelPreset"][value="custom"]');
    if (customRadio) {
      customRadio.checked = true;
      handlePresetChange('custom');
    }
  } else {
    // 重新应用当前预设逻辑
    handlePresetChange(currentPreset);
  }
}

/**
 * 获取渠道类型对应的官方 URL
 */
function getOfficialUrl(type) {
  const urls = {
    'codex': 'https://chatgpt.com/backend-api/codex',
    'anthropic': 'https://api.anthropic.com',
    'gemini': 'https://generativelanguage.googleapis.com'
  };
  return urls[type] || '';
}

/**
 * 检查当前 URL 是否为官方 URL
 */
function isOfficialUrl(url, type) {
  const officialUrls = {
    'codex': 'chatgpt.com/backend-api/codex',
    'anthropic': 'api.anthropic.com',
    'gemini': 'generativelanguage.googleapis.com'
  };
  const pattern = officialUrls[type];
  return pattern && url && url.includes(pattern);
}

/**
 * 处理预设切换（通用）
 * official: 自动填写官方 URL，Codex 显示 OAuth，其他渠道显示 API Key
 * custom: 用户自填 URL，显示 API Key
 */
function handlePresetChange(preset) {
  const isOfficial = preset === 'official';
  const channelType = document.querySelector('input[name="channelType"]:checked')?.value || 'anthropic';
  const codexAuthSwitch = document.getElementById('codexAuthSwitch');
  const standardKeyContainer = document.getElementById('standardKeyContainer');
  const codexOAuthSection = document.getElementById('codexOAuthSection');
  const codexQuotaTemplateBtn = document.getElementById('codexQuotaTemplateBtn');
  const quotaTemplateHint = document.getElementById('quotaTemplateHint');

  // 预设模式下隐藏手动切换开关（预设决定了认证方式）
  if (codexAuthSwitch) codexAuthSwitch.style.display = 'none';

  if (isOfficial) {
    // 官方预设：自动填写官方 URL
    const officialUrl = getOfficialUrl(channelType);
    if (officialUrl && typeof setInlineEndpoints === 'function') {
      setInlineEndpoints([officialUrl]);
    }

    // Codex 官方预设：显示 OAuth 区块
    if (channelType === 'codex') {
      if (standardKeyContainer) standardKeyContainer.style.display = 'none';
      if (codexOAuthSection) codexOAuthSection.style.display = 'block';
      if (codexQuotaTemplateBtn) codexQuotaTemplateBtn.style.display = 'inline-flex';
      if (quotaTemplateHint) quotaTemplateHint.textContent = 'Codex 官方自动填充认证信息';
    } else {
      // 其他渠道官方预设：目前仍使用 API Key（OAuth 暂不支持）
      if (standardKeyContainer) standardKeyContainer.style.display = 'block';
      if (codexOAuthSection) codexOAuthSection.style.display = 'none';
      if (codexQuotaTemplateBtn) codexQuotaTemplateBtn.style.display = 'none';
      if (quotaTemplateHint) quotaTemplateHint.textContent = '选择后请替换占位符';
    }
  } else {
    // 自定义预设：
    // 1. 清空 URL 让用户自填（如果当前是官方 URL）
    const endpoints = typeof getInlineEndpoints === 'function' ? getInlineEndpoints() : [];
    if (endpoints.length > 0 && endpoints[0] && isOfficialUrl(endpoints[0], channelType)) {
      if (typeof setInlineEndpoints === 'function') {
        setInlineEndpoints(['']);
      }
    }

    // 2. 显示 API Key 表格，隐藏 OAuth 区块
    if (standardKeyContainer) standardKeyContainer.style.display = 'block';
    if (codexOAuthSection) codexOAuthSection.style.display = 'none';

    // 3. 隐藏 Codex 官方用量模板按钮
    if (codexQuotaTemplateBtn) codexQuotaTemplateBtn.style.display = 'none';
    if (quotaTemplateHint) quotaTemplateHint.textContent = '选择后请替换占位符';
  }
}

// 兼容旧代码的别名
function handleCodexPresetChange(preset) {
  handlePresetChange(preset);
}

/**
 * 更新 Codex Token 状态 UI
 */
function updateCodexTokenUI(token) {
  const statusBadge = document.getElementById('codexTokenStatusBadge');
  const tokenInfo = document.getElementById('codexTokenInfo');
  const accountIdEl = document.getElementById('codexAccountId');
  const expiresAtEl = document.getElementById('codexExpiresAt');

  const startBtn = document.getElementById('startCodexOAuthBtn');
  const refreshBtn = document.getElementById('refreshCodexTokenBtn');
  const clearBtn = document.getElementById('clearCodexTokenBtn');

  if (token && token.access_token) {
    // 已授权
    statusBadge.textContent = '✓ 已授权';
    statusBadge.style.background = 'var(--success-100)';
    statusBadge.style.color = 'var(--success-700)';

    tokenInfo.style.display = 'block';
    accountIdEl.textContent = token.account_id || extractAccountIdFromToken(token.access_token) || '未知';

    if (token.expires_at) {
      const expDate = new Date(token.expires_at * 1000);
      expiresAtEl.textContent = expDate.toLocaleString();
    } else if (token.expires_in) {
      expiresAtEl.textContent = `约 ${Math.floor(token.expires_in / 3600)} 小时后`;
    } else {
      expiresAtEl.textContent = '未知';
    }

    startBtn.style.display = 'none';
    refreshBtn.style.display = 'inline-flex';
    clearBtn.style.display = 'inline-flex';

    // 更新隐藏的 input 值
    document.getElementById('channelApiKey').value = JSON.stringify(token);

  } else {
    // 未授权
    statusBadge.textContent = '未授权';
    statusBadge.style.background = 'var(--neutral-200)';
    statusBadge.style.color = 'var(--neutral-600)';

    tokenInfo.style.display = 'none';

    startBtn.style.display = 'inline-flex';
    refreshBtn.style.display = 'none';
    clearBtn.style.display = 'none';
  }
}

/**
 * 开始 Codex OAuth 流程
 */
async function startCodexOAuth() {
  // Codex CLI 的 OAuth 应用只注册了 localhost:1455 作为回调地址
  // 必须使用这个固定地址，否则 OpenAI 会报 unknown_error
  const FIXED_REDIRECT_URI = 'http://localhost:1455/auth/callback';

  const config = {
    authorizeUrl: 'https://auth.openai.com/oauth/authorize',
    clientId: 'app_EMoamEEZ73f0CkXaXp7hrann',
    redirectUri: FIXED_REDIRECT_URI, // 必须固定！
    scope: 'openid profile email offline_access',
    response_type: 'code',
    code_challenge_method: 'S256'
  };

  // 检查当前是否运行在 localhost:1455
  const isLocalhost1455 = window.location.hostname === 'localhost' && window.location.port === '1455';

  // 如果不是 localhost:1455，提示用户手动复制 code
  if (!isLocalhost1455) {
    alert(
      '注意：授权成功后，浏览器会跳转到 localhost:1455（可能无法访问）。\n\n' +
      '请从地址栏复制 code=xxx 后面的值，然后回来粘贴到"手动输入授权码"中。'
    );
  }

  // 生成 PKCE
  const { codeVerifier, codeChallenge } = await generatePKCE();
  localStorage.setItem('codex_oauth_verifier', codeVerifier);

  // 构建 URL
  const params = new URLSearchParams({
    client_id: config.clientId,
    redirect_uri: config.redirectUri,
    response_type: config.response_type,
    scope: config.scope,
    code_challenge: codeChallenge,
    code_challenge_method: config.code_challenge_method,
    state: Math.random().toString(36).substring(2)
  });

  const fullUrl = `${config.authorizeUrl}?${params.toString()}`;

  // 打开新窗口
  const width = 600;
  const height = 700;
  const left = (window.screen.width - width) / 2;
  const top = (window.screen.height - height) / 2;
  window.open(fullUrl, 'codex_oauth', `width=${width},height=${height},left=${left},top=${top}`);

  if (window.showSuccess) showSuccess('已打开授权窗口，请登录并授权');
}

/**
 * 处理 OAuth 回调消息
 */
async function handleCodexOAuthMessage(event) {
  const data = event.data;
  if (!data || !data.code) return;

  await exchangeCodeForToken(data.code);
}

/**
 * 用 Code 换取 Token
 */
async function exchangeCodeForToken(code) {
  const codeVerifier = localStorage.getItem('codex_oauth_verifier');
  if (!codeVerifier) {
    if (window.showError) showError('找不到 PKCE Verifier，请重新授权');
    return;
  }

  const startBtn = document.getElementById('startCodexOAuthBtn');
  const originalText = startBtn.textContent;
  startBtn.disabled = true;
  startBtn.textContent = '获取 Token 中...';

  try {
    // redirect_uri 必须与授权请求时使用的完全一致（固定值）
    const FIXED_REDIRECT_URI = 'http://localhost:1455/auth/callback';

    const body = new URLSearchParams({
      grant_type: 'authorization_code',
      code: code,
      redirect_uri: FIXED_REDIRECT_URI,
      client_id: 'app_EMoamEEZ73f0CkXaXp7hrann',
      code_verifier: codeVerifier
    });

    // 通过后端代理请求
    const res = await fetchWithAuth('/admin/oauth/token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        token_url: 'https://auth.openai.com/oauth/token',
        body: body.toString(),
        content_type: 'application/x-www-form-urlencoded'
      })
    });

    const result = await res.json();

    if (result.success && result.data) {
      let tokenData = typeof result.data === 'string' ? JSON.parse(result.data) : result.data;

      // 补充信息
      tokenData.type = 'oauth';
      tokenData.created_at = Math.floor(Date.now() / 1000);
      if (tokenData.expires_in) {
        tokenData.expires_at = tokenData.created_at + tokenData.expires_in;
      }
      tokenData.account_id = extractAccountIdFromToken(tokenData.access_token);

      updateCodexTokenUI(tokenData);
      localStorage.removeItem('codex_oauth_verifier');

      if (window.showSuccess) showSuccess('授权成功！');
    } else {
      throw new Error(result.error || '换取 Token 失败');
    }

  } catch (e) {
    console.error('OAuth Error:', e);
    if (window.showError) showError('授权失败: ' + e.message);
  } finally {
    startBtn.disabled = false;
    startBtn.textContent = originalText;
  }
}

/**
 * 刷新 Token
 */
async function refreshCodexToken() {
  const tokenJson = document.getElementById('channelApiKey').value;
  if (!tokenJson) return;

  let token;
  try {
    token = JSON.parse(tokenJson);
  } catch (e) { return; }

  if (!token.refresh_token) {
    if (window.showError) showError('没有 Refresh Token，请重新授权');
    return;
  }

  const refreshBtn = document.getElementById('refreshCodexTokenBtn');
  refreshBtn.disabled = true;
  refreshBtn.textContent = '刷新中...';

  try {
    const body = new URLSearchParams({
      grant_type: 'refresh_token',
      refresh_token: token.refresh_token,
      client_id: 'pdlLIX2Y72MIl2rhLhTE9VV9bN905kBh' // 刷新使用不同的 client_id
    });

    // 刷新使用 auth0.openai.com
    const res = await fetchWithAuth('/admin/oauth/token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        token_url: 'https://auth0.openai.com/oauth/token',
        body: body.toString(),
        content_type: 'application/x-www-form-urlencoded'
      })
    });

    const result = await res.json();

    if (result.success && result.data) {
      let newTokenData = typeof result.data === 'string' ? JSON.parse(result.data) : result.data;

      // 合并新旧数据（保留 refresh_token 如果新的没返回）
      const updatedToken = {
        ...token,
        ...newTokenData,
        type: 'oauth',
        created_at: Math.floor(Date.now() / 1000)
      };

      if (newTokenData.expires_in) {
        updatedToken.expires_at = updatedToken.created_at + newTokenData.expires_in;
      }

      // 如果新响应没有 refresh_token，保留旧的
      if (!newTokenData.refresh_token && token.refresh_token) {
        updatedToken.refresh_token = token.refresh_token;
      }

      // 更新 account_id
      updatedToken.account_id = extractAccountIdFromToken(updatedToken.access_token) || token.account_id;

      updateCodexTokenUI(updatedToken);
      if (window.showSuccess) showSuccess('Token 刷新成功');
    } else {
      throw new Error(result.error || '刷新失败');
    }

  } catch (e) {
    console.error('Refresh Error:', e);
    if (window.showError) showError('刷新失败: ' + e.message);
  } finally {
    refreshBtn.disabled = false;
    refreshBtn.textContent = '🔄 刷新 Token';
  }
}

/**
 * 清除 Codex 授权
 */
function clearCodexToken() {
  if (confirm('确定要清除授权信息吗？')) {
    document.getElementById('channelApiKey').value = '';
    updateCodexTokenUI(null);
  }
}

/**
 * 工具函数：生成 PKCE
 */
async function generatePKCE() {
  const array = new Uint8Array(32);
  crypto.getRandomValues(array);
  const codeVerifier = base64UrlEncode(array);

  const encoder = new TextEncoder();
  const data = encoder.encode(codeVerifier);
  const hash = await crypto.subtle.digest('SHA-256', data);
  const codeChallenge = base64UrlEncode(new Uint8Array(hash));

  return { codeVerifier, codeChallenge };
}

/**
 * Base64 URL 编码
 */
function base64UrlEncode(buffer) {
  let str = '';
  const bytes = new Uint8Array(buffer);
  for (let i = 0; i < bytes.length; i++) {
    str += String.fromCharCode(bytes[i]);
  }
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}

/**
 * 从 JWT 提取 account_id
 */
function extractAccountIdFromToken(token) {
  if (!token) return null;
  try {
    const parts = token.split('.');
    if (parts.length !== 3) return null;
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')));
    return payload['https://api.openai.com/auth']?.chatgpt_account_id || null;
  } catch (e) {
    return null;
  }
}

/**
 * 初始化渠道类型切换事件监听
 * 使用事件委托，避免重复绑定
 */
function initChannelTypeEventListener() {
  const container = document.getElementById('channelTypeRadios');
  if (!container || container.dataset.codexListenerAdded) return;

  container.dataset.codexListenerAdded = 'true';

  container.addEventListener('change', (e) => {
    const radio = e.target.closest('input[name="channelType"]');
    if (radio) {
      handleChannelTypeChange(radio.value);
    }
  });
}

/**
 * 手动提交 Codex 授权码
 */
async function submitManualCodexCode() {
  const input = document.getElementById('codexManualCodeInput');
  const code = input?.value?.trim();

  if (!code) {
    if (window.showError) showError('请输入授权码');
    return;
  }

  // 提取 code（支持粘贴完整 URL 或只粘贴 code 值）
  let authCode = code;
  if (code.includes('code=')) {
    const match = code.match(/code=([^&]+)/);
    authCode = match ? match[1] : code;
  }

  await exchangeCodeForToken(authCode);
  input.value = '';
}

/**
 * 切换 Codex 鉴权模式
 * @param {string} mode 'oauth' | 'apikey'
 */
function toggleCodexAuthMode(mode) {
  const oauthContainer = document.getElementById('codexOAuthContainer');
  const standardKeyContainer = document.getElementById('standardKeyContainer');
  const oauthToggle = document.getElementById('codexAuthToggleOAuth');
  const keyToggle = document.getElementById('codexAuthToggleApiKey');

  if (mode === 'oauth') {
    // OAuth 模式：显示 OAuth 容器，隐藏标准 Key 表格
    if (oauthContainer) oauthContainer.style.display = 'block';
    if (standardKeyContainer) standardKeyContainer.style.display = 'none';

    // 更新切换按钮样式（使用 CSS class）
    if (oauthToggle) {
      oauthToggle.classList.add('active');
      oauthToggle.querySelector('input').checked = true;
    }
    if (keyToggle) {
      keyToggle.classList.remove('active');
    }
  } else {
    // API Key 模式：隐藏 OAuth 容器，显示标准 Key 表格
    if (oauthContainer) oauthContainer.style.display = 'none';
    if (standardKeyContainer) standardKeyContainer.style.display = 'block';

    // 更新切换按钮样式（使用 CSS class）
    if (keyToggle) {
      keyToggle.classList.add('active');
      keyToggle.querySelector('input').checked = true;
    }
    if (oauthToggle) {
      oauthToggle.classList.remove('active');
    }
  }
}
