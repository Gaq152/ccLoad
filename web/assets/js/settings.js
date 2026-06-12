// 系统设置页面
initTopbar('settings');

// ============================================================================
// Tab 切换逻辑
// ============================================================================
(function initTabs() {
  const tabBtns = document.querySelectorAll('.tab-btn[data-tab]');
  tabBtns.forEach(btn => {
    btn.addEventListener('click', () => {
      // 切换按钮状态
      tabBtns.forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      // 切换内容区
      document.querySelectorAll('.tab-content').forEach(tc => {
        tc.classList.remove('active');
        tc.style.display = 'none';
      });
      const target = document.getElementById('tab-' + btn.dataset.tab);
      if (target) {
        target.classList.add('active');
        target.style.display = 'block';
      }
      // 切换到模型计费 Tab 时加载数据
      if (btn.dataset.tab === 'pricing' && !pricingLoaded) {
        loadPricing();
      }
      // 切换到安全 Tab 时刷新 2FA 状态和 Turnstile 配置
      if (btn.dataset.tab === 'security') {
        loadSecurityTab();
      }
    });
  });
})();

// ============================================================================
// 基础设置 Tab
// ============================================================================
let originalSettings = {}; // 保存原始值用于比较

async function loadSettings() {
  try {
    const resp = await fetchDataWithAuth('/admin/settings');
    const data = resp.settings || resp;
    if (!Array.isArray(data)) throw new Error('响应不是数组');
    renderSettings(data);
  } catch (err) {
    console.error('加载配置异常:', err);
    showError('加载配置异常: ' + err.message);
  }
}

function renderSettings(settings) {
  const tbody = document.getElementById('settings-tbody');
  originalSettings = {};
  tbody.innerHTML = '';

  // 初始化事件委托（仅一次）
  initSettingsEventDelegation();

  settings.forEach(s => {
    // 安全相关配置项由安全 Tab 的专属卡片管理，不在通用表格渲染
    if (s.key.startsWith('turnstile_') || s.key === 'twofa_login_required') return;

    originalSettings[s.key] = s.value;
    const row = TemplateEngine.render('tpl-setting-row', {
      key: s.key,
      description: s.description,
      inputHtml: renderInput(s)
    });
    if (row) tbody.appendChild(row);
  });
}

// 初始化事件委托（替代 inline onclick）
function initSettingsEventDelegation() {
  const tbody = document.getElementById('settings-tbody');
  if (!tbody || tbody.dataset.delegated) return;
  tbody.dataset.delegated = 'true';

  // 重置按钮点击
  tbody.addEventListener('click', (e) => {
    const resetBtn = e.target.closest('.setting-reset-btn');
    if (resetBtn) {
      resetSetting(resetBtn.dataset.key);
    }
  });

  // 输入变更（支持 input 和 select）
  tbody.addEventListener('change', (e) => {
    const input = e.target.closest('input, select');
    if (input) {
      // 多选组件特殊处理
      if (input.dataset.multiselect) {
        markMultiselectChanged(input.dataset.multiselect);
      } else {
        markChanged(input);
      }
    }
  });
}

// 标记多选组件变更
function markMultiselectChanged(key) {
  const container = document.getElementById(key);
  if (!container) return;
  const row = container.closest('tr');
  const currentValue = getMultiselectValue(key);
  if (currentValue !== originalSettings[key]) {
    row.classList.add('row-highlight');
  } else {
    row.classList.remove('row-highlight');
  }
}

// 获取多选组件的值（逗号分隔）
function getMultiselectValue(key) {
  const checkboxes = document.querySelectorAll(`input[data-multiselect="${key}"]:checked`);
  return Array.from(checkboxes).map(cb => cb.value).join(',');
}

// 下拉选项配置（中文化）
const selectOptions = {
  'cooldown_mode': [
    { value: 'exponential', label: '递增（指数退避）' },
    { value: 'fixed', label: '固定（相同间隔）' }
  ]
};

// 多选下拉配置（逗号分隔的字符串值）
const multiSelectOptions = {
  'channel_stats_fields': [
    { value: 'calls', label: '调用数' },
    { value: 'rate', label: '成功率' },
    { value: 'first_byte', label: '首字节' },
    { value: 'input', label: '输入Token' },
    { value: 'output', label: '输出Token' },
    { value: 'cache_read', label: '缓存读' },
    { value: 'cache_creation', label: '缓存建' },
    { value: 'cost', label: '成本' }
  ],
  'nav_visible_pages': [
    { value: 'stats', label: '调用统计' },
    { value: 'trends', label: '请求趋势' },
    { value: 'model-test', label: '模型测试' },
    { value: 'monitor', label: '请求监控' }
  ]
};

function renderInput(setting) {
  const safeKey = escapeHtml(setting.key);
  const safeValue = escapeHtml(setting.value);
  const baseStyle = 'padding: 6px 10px; border: 1px solid var(--neutral-300); border-radius: 6px; background: var(--input-bg, rgba(255, 255, 255, 0.9)); color: var(--neutral-800); font-size: 13px;';

  // 多选下拉（逗号分隔值）
  if (multiSelectOptions[setting.key]) {
    const options = multiSelectOptions[setting.key];
    const selectedValues = setting.value.split(',').map(v => v.trim()).filter(Boolean);
    const checkboxes = options.map(opt => {
      const checked = selectedValues.includes(opt.value) ? 'checked' : '';
      return `<label style="display: inline-flex; align-items: center; margin-right: 12px; cursor: pointer;">
        <input type="checkbox" data-multiselect="${safeKey}" value="${escapeHtml(opt.value)}" ${checked} style="margin-right: 4px;">
        ${escapeHtml(opt.label)}
      </label>`;
    }).join('');
    return `<div id="${safeKey}" class="multiselect-group" style="display: flex; flex-wrap: wrap; gap: 4px;">${checkboxes}</div>`;
  }

  // 特定字段使用下拉选择框
  if (selectOptions[setting.key]) {
    const options = selectOptions[setting.key];
    const optionsHtml = options.map(opt =>
      `<option value="${escapeHtml(opt.value)}" ${opt.value === setting.value ? 'selected' : ''}>${escapeHtml(opt.label)}</option>`
    ).join('');
    return `<select id="${safeKey}" style="${baseStyle} width: 180px; cursor: pointer;">${optionsHtml}</select>`;
  }

  switch (setting.value_type) {
    case 'bool':
      const checked = setting.value === 'true' || setting.value === '1';
      return `<input type="checkbox" id="${safeKey}" ${checked ? 'checked' : ''} style="width: 18px; height: 18px; cursor: pointer;">`;
    case 'int':
    case 'duration':
      return `<input type="number" id="${safeKey}" value="${safeValue}" style="${baseStyle} width: 100px; text-align: right;">`;
    default:
      return `<input type="text" id="${safeKey}" value="${safeValue}" style="${baseStyle} width: 280px;">`;
  }
}

function markChanged(input) {
  const key = input.id;
  const row = input.closest('tr');

  // 支持 checkbox、select 和普通 input
  let currentValue;
  if (input.type === 'checkbox') {
    currentValue = input.checked ? 'true' : 'false';
  } else {
    currentValue = input.value;
  }

  if (currentValue !== originalSettings[key]) {
    row.classList.add('row-highlight');
  } else {
    row.classList.remove('row-highlight');
  }
}

async function saveAllSettings() {
  // 收集所有变更
  const updates = {};
  const needsRestartKeys = [];
  let needsPageReload = false;

  for (const key of Object.keys(originalSettings)) {
    const input = document.getElementById(key);
    if (!input) continue;

    let currentValue;
    // 多选组件
    if (input.classList.contains('multiselect-group')) {
      currentValue = getMultiselectValue(key);
    } else if (input.type === 'checkbox') {
      currentValue = input.checked ? 'true' : 'false';
    } else {
      currentValue = input.value;
    }

    if (currentValue !== originalSettings[key]) {
      updates[key] = currentValue;

      // 导航栏配置修改需要刷新页面
      if (key === 'nav_visible_pages') {
        needsPageReload = true;
      }

      // 检查是否需要重启（从 DOM 中读取 description）
      const row = input.closest('tr');
      if (row?.querySelector('td')?.textContent?.includes('[需重启]')) {
        needsRestartKeys.push(key);
      }
    }
  }

  if (Object.keys(updates).length === 0) {
    showInfo('没有需要保存的更改');
    return;
  }

  // 使用批量更新接口（单次请求，事务保护）
  try {
    await fetchDataWithAuth('/admin/settings/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates)
    });
    let msg = `已保存 ${Object.keys(updates).length} 项配置`;
    if (needsRestartKeys.length > 0) {
      msg += `\n\n以下配置需要重启服务才能生效:\n${needsRestartKeys.join(', ')}`;
    }
    if (needsPageReload) {
      msg += '\n\n导航栏配置已更新，页面即将刷新...';
    }
    showSuccess(msg);

    // 导航栏配置修改后自动刷新页面
    if (needsPageReload) {
      setTimeout(() => {
        location.reload();
      }, 500);
    } else {
      loadSettings();
    }
  } catch (err) {
    console.error('保存异常:', err);
    showError('保存异常: ' + err.message);
  }
}

async function resetSetting(key) {
  if (!await showConfirm({
    title: '重置确认',
    message: `确定要重置 "${key}" 为默认值吗?`,
    type: 'warning'
  })) return;

  try {
    await fetchDataWithAuth(`/admin/settings/${key}/reset`, { method: 'POST' });
    showSuccess(`配置 ${key} 已重置为默认值`);
    loadSettings();
  } catch (err) {
    console.error('重置异常:', err);
    showError('重置异常: ' + err.message);
  }
}

// showSuccess/showError 已在 ui.js 中定义（toast 通知），无需重复定义
function showInfo(msg) {
  window.showNotification(msg, 'info');
}

// ============================================================================
// 模型计费 Tab
// ============================================================================
let pricingLoaded = false;
let pricingData = []; // 完整数据（用于前端筛选）

const channelTypeLabels = {
  'anthropic': 'Claude',
  'openai': 'OpenAI',
  'gemini': 'Gemini'
};

async function loadPricing() {
  try {
    const resp = await fetchDataWithAuth('/admin/pricing');
    pricingData = resp.entries || [];
    pricingLoaded = true;
    renderPricing();
    initPricingEventDelegation();
  } catch (err) {
    console.error('加载定价异常:', err);
    showError('加载定价异常: ' + err.message);
  }
}

function renderPricing() {
  const tbody = document.getElementById('pricing-tbody');
  const emptyDiv = document.getElementById('pricing-empty');
  tbody.innerHTML = '';

  // 筛选
  const search = (document.getElementById('pricing-search')?.value || '').toLowerCase();
  const typeFilter = document.getElementById('pricing-type-filter')?.value || '';

  const filtered = pricingData.filter(e => {
    if (search && !e.model.toLowerCase().includes(search) && !(e.display_name || '').toLowerCase().includes(search)) {
      return false;
    }
    if (typeFilter && e.channel_type !== typeFilter) {
      return false;
    }
    return true;
  });

  if (filtered.length === 0) {
    emptyDiv.style.display = pricingData.length === 0 ? 'block' : 'block';
    if (pricingData.length > 0 && filtered.length === 0) {
      emptyDiv.textContent = '没有匹配的结果';
    } else {
      emptyDiv.textContent = '暂无定价数据，点击"导入默认定价"快速初始化';
    }
    return;
  }

  emptyDiv.style.display = 'none';

  filtered.forEach(e => {
    const aliasesList = e.aliases || [];
    const aliasesFull = aliasesList.join(', ');
    const aliasesDisplay = aliasesList.length > 0
      ? aliasesList.slice(0, 3).join(', ') + (aliasesList.length > 3 ? ` +${aliasesList.length - 3}` : '')
      : '<span style="color:var(--neutral-400);">-</span>';

    const predefinedBadge = e.is_predefined
      ? '<span style="font-size:10px;padding:1px 5px;border-radius:3px;background:var(--success-100,#dcfce7);color:var(--success-700,#15803d);font-weight:normal;">预定义</span>'
      : '';

    const row = TemplateEngine.render('tpl-pricing-row', {
      id: e.id,
      model: e.model,
      display_name: e.display_name || e.model,
      channel_type: e.channel_type,
      channel_type_label: channelTypeLabels[e.channel_type] || e.channel_type,
      input_price: formatPrice(e.input_price),
      output_price: formatPrice(e.output_price),
      high_input_display: e.input_price_high > 0 ? '$' + formatPrice(e.input_price_high) : '<span style="color:var(--neutral-400);">-</span>',
      high_output_display: e.output_price_high > 0 ? '$' + formatPrice(e.output_price_high) : '<span style="color:var(--neutral-400);">-</span>',
      aliases_display: aliasesDisplay,
      aliases_full: aliasesFull,
      predefined_badge: predefinedBadge
    });
    if (row) tbody.appendChild(row);
  });
}

function formatPrice(val) {
  if (val === 0) return '0';
  if (val < 0.01) return val.toFixed(4);
  if (val < 1) return val.toFixed(3);
  return val.toFixed(2);
}

function initPricingEventDelegation() {
  const tbody = document.getElementById('pricing-tbody');
  if (!tbody || tbody.dataset.delegated) return;
  tbody.dataset.delegated = 'true';

  tbody.addEventListener('click', (e) => {
    const editBtn = e.target.closest('.pricing-edit-btn');
    if (editBtn) {
      const id = parseInt(editBtn.dataset.id);
      const entry = pricingData.find(p => p.id === id);
      if (entry) openPricingDrawer(entry);
      return;
    }

    const deleteBtn = e.target.closest('.pricing-delete-btn');
    if (deleteBtn) {
      deletePricingEntry(parseInt(deleteBtn.dataset.id), deleteBtn.dataset.model);
    }
  });

  // 搜索和筛选
  document.getElementById('pricing-search')?.addEventListener('input', renderPricing);
  document.getElementById('pricing-type-filter')?.addEventListener('change', renderPricing);
}

// ============================================================================
// 定价抽屉（新增/编辑）
// ============================================================================
function openPricingDrawer(entry) {
  const isEdit = !!entry;
  document.getElementById('pricingDrawerTitle').textContent = isEdit ? '编辑模型定价' : '新增模型定价';
  document.getElementById('pricingDrawerId').value = isEdit ? entry.id : '';
  document.getElementById('pricingModel').value = isEdit ? entry.model : '';
  document.getElementById('pricingModel').readOnly = isEdit; // 编辑时不允许修改模型名
  document.getElementById('pricingDisplayName').value = isEdit ? (entry.display_name || '') : '';
  document.getElementById('pricingChannelType').value = isEdit ? entry.channel_type : 'anthropic';
  document.getElementById('pricingInputPrice').value = isEdit ? entry.input_price : '';
  document.getElementById('pricingOutputPrice').value = isEdit ? entry.output_price : '';
  document.getElementById('pricingInputPriceHigh').value = isEdit ? entry.input_price_high : 0;
  document.getElementById('pricingOutputPriceHigh').value = isEdit ? entry.output_price_high : 0;
  document.getElementById('pricingCacheReadMul').value = isEdit ? entry.cache_read_multiplier : 0;
  document.getElementById('pricingCacheWriteMul').value = isEdit ? entry.cache_write_multiplier : 0;

  // 别名（数组 → 换行分隔文本）
  const aliases = isEdit ? (entry.aliases || []) : [];
  document.getElementById('pricingAliases').value = aliases.join('\n');

  // 预定义列表开关
  document.getElementById('pricingIsPredefined').checked = isEdit ? !!entry.is_predefined : false;

  document.getElementById('pricingDrawerOverlay').classList.add('show');
  document.getElementById('pricingDrawer').classList.add('open');
}

function closePricingDrawer() {
  document.getElementById('pricingDrawerOverlay').classList.remove('show');
  document.getElementById('pricingDrawer').classList.remove('open');
}

async function savePricingEntry() {
  const id = document.getElementById('pricingDrawerId').value;
  const isEdit = !!id;

  const payload = {
    model: document.getElementById('pricingModel').value.trim(),
    display_name: document.getElementById('pricingDisplayName').value.trim(),
    channel_type: document.getElementById('pricingChannelType').value,
    input_price: parseFloat(document.getElementById('pricingInputPrice').value) || 0,
    output_price: parseFloat(document.getElementById('pricingOutputPrice').value) || 0,
    input_price_high: parseFloat(document.getElementById('pricingInputPriceHigh').value) || 0,
    output_price_high: parseFloat(document.getElementById('pricingOutputPriceHigh').value) || 0,
    cache_read_multiplier: parseFloat(document.getElementById('pricingCacheReadMul').value) || 0,
    cache_write_multiplier: parseFloat(document.getElementById('pricingCacheWriteMul').value) || 0,
    aliases: document.getElementById('pricingAliases').value.split('\n').map(s => s.trim()).filter(Boolean),
    is_predefined: document.getElementById('pricingIsPredefined').checked,
  };

  if (!payload.model) {
    showError('模型名称不能为空');
    return;
  }

  try {
    const url = isEdit ? `/admin/pricing/${id}` : '/admin/pricing';
    const method = isEdit ? 'PUT' : 'POST';
    await fetchDataWithAuth(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    showSuccess(isEdit ? '定价已更新' : '定价已创建');
    closePricingDrawer();
    loadPricing();
  } catch (err) {
    console.error('保存定价异常:', err);
    showError('保存失败: ' + err.message);
  }
}

async function deletePricingEntry(id, modelName) {
  if (!await showConfirm({
    title: '删除确认',
    message: `确定要删除模型 "${modelName}" 的定价吗？\n删除后将回退到内置默认定价。`,
    type: 'danger'
  })) return;

  try {
    await fetchDataWithAuth(`/admin/pricing/${id}`, { method: 'DELETE' });
    showSuccess(`模型 ${modelName} 定价已删除`);
    loadPricing();
  } catch (err) {
    console.error('删除定价异常:', err);
    showError('删除失败: ' + err.message);
  }
}

async function importDefaultPricing() {
  if (!await showConfirm({
    title: '导入默认定价',
    message: '将从内置数据导入所有模型定价。已存在的模型将被覆盖更新。',
    type: 'warning'
  })) return;

  try {
    const resp = await fetchDataWithAuth('/admin/pricing/defaults', { method: 'POST' });
    showSuccess(resp.message || '导入完成');
    loadPricing();
  } catch (err) {
    console.error('导入默认定价异常:', err);
    showError('导入失败: ' + err.message);
  }
}

// ============================================================================
// 安全 Tab：两步验证(TOTP) + Turnstile 人机验证
// ============================================================================
let tfaRecoveryCodes = []; // 激活后返回的恢复码明文（仅本次会话展示用）
let tfaEnabled = false;    // 当前2FA状态（改密码弹窗据此显示验证码输入框）

async function loadSecurityTab() {
  loadTfaStatus();
  loadTurnstileSettings();
}

// ---- 两步验证 ----

async function loadTfaStatus() {
  const badge = document.getElementById('tfa-status-badge');
  const bindBtn = document.getElementById('tfa-bind-btn');
  const unbindBtn = document.getElementById('tfa-unbind-btn');
  try {
    const data = await fetchDataWithAuth('/admin/2fa/status');
    tfaEnabled = !!data.enabled;
    // 登录验证开关仅绑定后显示
    document.getElementById('tfa-login-toggle-row').style.display = tfaEnabled ? 'inline-flex' : 'none';
    if (data.enabled) {
      badge.textContent = '已开启';
      badge.style.background = 'var(--success-100, #dcfce7)';
      badge.style.color = 'var(--success-700, #15803d)';
      bindBtn.style.display = 'none';
      unbindBtn.style.display = 'inline-flex';
    } else {
      badge.textContent = '未开启';
      badge.style.background = 'var(--neutral-100)';
      badge.style.color = 'var(--neutral-500)';
      bindBtn.style.display = 'inline-flex';
      unbindBtn.style.display = 'none';
    }
  } catch (err) {
    console.error('加载2FA状态异常:', err);
    badge.textContent = '加载失败';
  }
}

async function openTfaBindModal() {
  try {
    // 生成待激活 secret + 二维码（回填验证码确认前不生效）
    const data = await fetchDataWithAuth('/admin/2fa/setup', { method: 'POST' });
    document.getElementById('tfa-qr-img').src = data.qr_image;
    document.getElementById('tfa-secret-text').textContent = data.secret;
    document.getElementById('tfa-activate-code').value = '';
    document.getElementById('tfa-bind-step-scan').style.display = 'block';
    document.getElementById('tfa-bind-step-recovery').style.display = 'none';
    document.getElementById('tfaBindModal').classList.add('show');
    setTimeout(() => document.getElementById('tfa-activate-code').focus(), 200);
  } catch (err) {
    console.error('生成绑定二维码异常:', err);
    showError('生成二维码失败: ' + err.message);
  }
}

function closeTfaBindModal(refresh) {
  document.getElementById('tfaBindModal').classList.remove('show');
  tfaRecoveryCodes = [];
  if (refresh) loadTfaStatus();
}

async function activateTfa() {
  const code = document.getElementById('tfa-activate-code').value.trim();
  if (!code) {
    showError('请输入验证器 App 中的 6 位验证码');
    return;
  }
  try {
    const data = await fetchDataWithAuth('/admin/2fa/activate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code })
    });
    // 绑定成功 → 同一弹窗切换为恢复码展示（明文仅此一次）
    tfaRecoveryCodes = data.recovery_codes || [];
    const container = document.getElementById('tfa-recovery-codes');
    container.innerHTML = '';
    tfaRecoveryCodes.forEach(c => {
      const div = document.createElement('div');
      div.textContent = c;
      container.appendChild(div);
    });
    document.getElementById('tfa-bind-step-scan').style.display = 'none';
    document.getElementById('tfa-bind-step-recovery').style.display = 'block';
    loadTfaStatus();
    showSuccess('两步验证绑定成功');
  } catch (err) {
    console.error('激活2FA异常:', err);
    showError(err.message || '验证码错误，请确认手机时间准确');
    document.getElementById('tfa-activate-code').value = '';
  }
}

async function copyRecoveryCodes() {
  try {
    await navigator.clipboard.writeText(tfaRecoveryCodes.join('\n'));
    showSuccess('恢复码已复制到剪贴板');
  } catch (_) {
    showError('复制失败，请手动抄录');
  }
}

// 下载恢复码为本地 .txt 文件（纯前端 Blob 生成，不经过服务器）
function downloadRecoveryCodes() {
  const content = [
    'ccLoad 两步验证恢复码',
    '生成时间: ' + new Date().toLocaleString(),
    '',
    '每个恢复码只能使用一次。手机验证器不可用时，登录第二步输入恢复码即可。',
    '请妥善保管，不要与他人共享。',
    '',
    ...tfaRecoveryCodes
  ].join('\n');

  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'ccload-recovery-codes.txt';
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
  showSuccess('恢复码文件已下载');
}

function openTfaUnbindModal() {
  document.getElementById('tfa-unbind-code').value = '';
  document.getElementById('tfaUnbindModal').classList.add('show');
  setTimeout(() => document.getElementById('tfa-unbind-code').focus(), 200);
}

function closeTfaUnbindModal() {
  document.getElementById('tfaUnbindModal').classList.remove('show');
}

async function confirmUnbindTfa() {
  const code = document.getElementById('tfa-unbind-code').value.trim();
  if (!code) {
    showError('请输入验证码或恢复码');
    return;
  }
  try {
    await fetchDataWithAuth('/admin/2fa/disable', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code })
    });
    closeTfaUnbindModal();
    loadTfaStatus();
    showSuccess('两步验证已解绑');
  } catch (err) {
    console.error('解绑2FA异常:', err);
    showError(err.message || '验证码错误');
    document.getElementById('tfa-unbind-code').value = '';
  }
}

// 切换「登录是否需要两步验证码」（即时保存，热生效）
async function saveTfaLoginRequired(checkbox) {
  try {
    await fetchDataWithAuth('/admin/settings/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ twofa_login_required: checkbox.checked ? 'true' : 'false' })
    });
    showSuccess(checkbox.checked ? '已开启：登录需要两步验证码' : '已关闭：登录仅需密码（修改密码仍需验证码）');
  } catch (err) {
    console.error('保存2FA登录开关异常:', err);
    checkbox.checked = !checkbox.checked; // 保存失败回滚UI状态
    showError('保存失败: ' + err.message);
  }
}

// ---- 修改管理密码 ----

function openChangePasswordModal() {
  document.getElementById('cp-old-password').value = '';
  document.getElementById('cp-new-password').value = '';
  document.getElementById('cp-confirm-password').value = '';
  document.getElementById('cp-totp-code').value = '';
  // 已绑定2FA时强制验证动态码
  document.getElementById('cp-totp-group').style.display = tfaEnabled ? 'block' : 'none';
  document.getElementById('changePasswordModal').classList.add('show');
  setTimeout(() => document.getElementById('cp-old-password').focus(), 200);
}

function closeChangePasswordModal() {
  document.getElementById('changePasswordModal').classList.remove('show');
}

async function confirmChangePassword() {
  const oldPassword = document.getElementById('cp-old-password').value;
  const newPassword = document.getElementById('cp-new-password').value;
  const confirmPassword = document.getElementById('cp-confirm-password').value;
  const totpCode = document.getElementById('cp-totp-code').value.trim();

  if (!oldPassword || !newPassword) {
    showError('请填写当前密码和新密码');
    return;
  }
  if (newPassword.length < 8) {
    showError('新密码长度至少 8 位');
    return;
  }
  if (newPassword !== confirmPassword) {
    showError('两次输入的新密码不一致');
    return;
  }
  if (tfaEnabled && !totpCode) {
    showError('已开启两步验证，请输入动态验证码');
    return;
  }

  try {
    await fetchDataWithAuth('/admin/password/change', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        old_password: oldPassword,
        new_password: newPassword,
        totp_code: totpCode
      })
    });
    closeChangePasswordModal();
    showSuccess('密码已修改，所有会话已失效，即将跳转登录页...');
    // 所有会话已被吊销，清理本地凭证并跳转登录
    setTimeout(() => {
      localStorage.removeItem('ccload_token');
      localStorage.removeItem('ccload_token_expiry');
      window.location.href = '/web/login.html';
    }, 1500);
  } catch (err) {
    console.error('修改密码异常:', err);
    showError(err.message || '修改失败');
  }
}

// ---- Turnstile 配置（独立卡片，不走通用设置表格） ----

async function loadTurnstileSettings() {
  try {
    const resp = await fetchDataWithAuth('/admin/settings');
    const settings = resp.settings || resp;
    const map = {};
    settings.forEach(s => { map[s.key] = s.value; });
    document.getElementById('ts-enabled').checked = map['turnstile_enabled'] === 'true' || map['turnstile_enabled'] === '1';
    document.getElementById('ts-site-key').value = map['turnstile_site_key'] || '';
    document.getElementById('ts-secret-key').value = map['turnstile_secret_key'] || '';
    // 2FA 登录验证开关（缺省视为开启，与后端默认一致）
    const loginReq = map['twofa_login_required'];
    document.getElementById('tfa-login-required').checked = loginReq === undefined || loginReq === 'true' || loginReq === '1';
  } catch (err) {
    console.error('加载Turnstile配置异常:', err);
    showError('加载 Turnstile 配置失败: ' + err.message);
  }
}

async function saveTurnstileSettings() {
  const enabled = document.getElementById('ts-enabled').checked;
  const siteKey = document.getElementById('ts-site-key').value.trim();
  const secretKey = document.getElementById('ts-secret-key').value.trim();

  if (enabled && (!siteKey || !secretKey)) {
    showError('开启 Turnstile 需要同时填写 Site Key 和 Secret Key');
    return;
  }

  try {
    await fetchDataWithAuth('/admin/settings/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        turnstile_enabled: enabled ? 'true' : 'false',
        turnstile_site_key: siteKey,
        turnstile_secret_key: secretKey
      })
    });
    showSuccess('Turnstile 配置已保存，立即生效');
  } catch (err) {
    console.error('保存Turnstile配置异常:', err);
    showError('保存失败: ' + err.message);
  }
}

// ============================================================================
// 页面初始化
// ============================================================================
loadSettings();
