// 系统设置页面
initTopbar('settings');

let originalSettings = {}; // 保存原始值用于比较

async function loadSettings() {
  try {
    const resp = await fetchWithAuth('/admin/settings');
    const data = await resp.json();

    if (!data.success || !data.data?.settings) {
      console.error('加载配置失败:', data);
      showError('加载配置失败: ' + (data.error || '未知错误'));
      return;
    }

    renderSettings(data.data.settings);
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
    { value: 'model-test', label: '模型测试' }
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
    const resp = await fetchWithAuth('/admin/settings/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates)
    });

    const data = await resp.json();
    if (data.success) {
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
    } else {
      showError('保存失败: ' + (data.error || '未知错误'));
    }
  } catch (err) {
    console.error('保存异常:', err);
    showError('保存异常: ' + err.message);
  }
}

async function resetSetting(key) {
  if (!confirm(`确定要重置 "${key}" 为默认值吗?`)) return;

  try {
    const resp = await fetchWithAuth(`/admin/settings/${key}/reset`, { method: 'POST' });
    const data = await resp.json();
    if (data.success) {
      showSuccess(`配置 ${key} 已重置为默认值`);
      loadSettings();
    } else {
      showError('重置失败: ' + (data.error || '未知错误'));
    }
  } catch (err) {
    console.error('重置异常:', err);
    showError('重置异常: ' + err.message);
  }
}

// showSuccess/showError 已在 ui.js 中定义（toast 通知），无需重复定义
function showInfo(msg) {
  window.showNotification(msg, 'info');
}

// 页面加载时执行
loadSettings();
