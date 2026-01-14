/**
 * channels-bulk.js
 * 渠道管理批量操作逻辑
 */

// 批量选择状态管理
const bulkState = {
  selectedIds: new Set(),
  isShiftPressed: false,
  lastCheckedId: null
};

// 监听 Shift 键实现连选功能
document.addEventListener('keydown', e => { if (e.key === 'Shift') bulkState.isShiftPressed = true; });
document.addEventListener('keyup', e => { if (e.key === 'Shift') bulkState.isShiftPressed = false; });

/**
 * 切换单个渠道选中状态
 */
function toggleChannelSelection(id, checked) {
  id = parseInt(id);

  // 处理 Shift 连选
  if (checked && bulkState.isShiftPressed && bulkState.lastCheckedId !== null) {
    handleShiftSelect(id);
  }

  if (checked) {
    bulkState.selectedIds.add(id);
    bulkState.lastCheckedId = id;
  } else {
    bulkState.selectedIds.delete(id);
  }

  updateBulkUI();
}

/**
 * 处理 Shift 连选逻辑
 */
function handleShiftSelect(currentId) {
  // 获取当前渲染的所有渠道ID（顺序很重要）
  const visibleCards = Array.from(document.querySelectorAll('.channel-card'));
  const visibleIds = visibleCards.map(card => parseInt(card.dataset.channelId));

  const startIdx = visibleIds.indexOf(bulkState.lastCheckedId);
  const endIdx = visibleIds.indexOf(currentId);

  if (startIdx === -1 || endIdx === -1) return;

  const [min, max] = [Math.min(startIdx, endIdx), Math.max(startIdx, endIdx)];
  const rangeIds = visibleIds.slice(min, max + 1);

  rangeIds.forEach(id => {
    bulkState.selectedIds.add(id);
    // 同步更新 DOM
    const checkbox = document.querySelector(`.channel-checkbox[data-id="${id}"]`);
    if (checkbox) checkbox.checked = true;
  });
}

/**
 * 全选/取消全选 (针对当前可见列表)
 */
function toggleSelectAll(checked) {
  const visibleCards = document.querySelectorAll('.channel-card');

  visibleCards.forEach(card => {
    const id = parseInt(card.dataset.channelId);
    const checkbox = card.querySelector('.channel-checkbox');

    if (checked) {
      bulkState.selectedIds.add(id);
      if (checkbox) checkbox.checked = true;
    } else {
      bulkState.selectedIds.delete(id);
      if (checkbox) checkbox.checked = false;
    }
  });

  if (checked && visibleCards.length > 0) {
    bulkState.lastCheckedId = parseInt(visibleCards[visibleCards.length - 1].dataset.channelId);
  }

  updateBulkUI();
}

/**
 * 更新 UI 状态 (操作栏显示、全选框状态)
 */
function updateBulkUI() {
  const count = bulkState.selectedIds.size;
  const bar = document.getElementById('bulkActionBar');
  const countSpan = document.getElementById('bulkSelectedCount');
  const selectAllCheckbox = document.getElementById('selectAllChannels');

  // 更新操作栏
  if (bar) {
    if (count > 0) {
      bar.classList.add('visible');
      if (countSpan) countSpan.textContent = count;
    } else {
      bar.classList.remove('visible');
    }
  }

  // 更新全选框状态 (如果所有可见的都被选中)
  if (selectAllCheckbox) {
    const visibleCards = document.querySelectorAll('.channel-card');
    const visibleCount = visibleCards.length;
    if (visibleCount > 0) {
      const allVisibleSelected = Array.from(visibleCards).every(card =>
        bulkState.selectedIds.has(parseInt(card.dataset.channelId))
      );
      selectAllCheckbox.checked = allVisibleSelected;
      selectAllCheckbox.indeterminate = count > 0 && !allVisibleSelected;
    } else {
      selectAllCheckbox.checked = false;
      selectAllCheckbox.indeterminate = false;
    }
  }
}

/**
 * 取消所有选择
 */
function clearBulkSelection() {
  bulkState.selectedIds.clear();
  bulkState.lastCheckedId = null;

  // 取消所有 checkbox 选中状态
  document.querySelectorAll('.channel-checkbox').forEach(cb => cb.checked = false);

  // 更新全选框
  const selectAllCheckbox = document.getElementById('selectAllChannels');
  if (selectAllCheckbox) {
    selectAllCheckbox.checked = false;
    selectAllCheckbox.indeterminate = false;
  }

  updateBulkUI();
}

/**
 * 执行批量操作
 * @param {string} action - 'enable' | 'disable' | 'delete'
 */
async function executeBulkAction(action) {
  const ids = Array.from(bulkState.selectedIds);
  if (ids.length === 0) return;

  if (action === 'delete') {
    if (!confirm(`确定要删除选中的 ${ids.length} 个渠道吗？此操作不可恢复！`)) return;
  }

  const btnId = `bulk${action.charAt(0).toUpperCase() + action.slice(1)}Btn`;
  const btn = document.getElementById(btnId);
  const originalText = btn ? btn.innerHTML : '';
  if (btn) {
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-small"></span> 处理中...';
  }

  let successCount = 0;
  let failCount = 0;

  try {
    // 限制并发数，避免浏览器阻塞或服务器压力过大 (每批 5 个)
    const batchSize = 5;
    for (let i = 0; i < ids.length; i += batchSize) {
      const batch = ids.slice(i, i + batchSize);
      await Promise.all(batch.map(async (id) => {
        try {
          if (action === 'delete') {
            const res = await fetchAPIWithAuth(`/admin/channels/${id}`, { method: 'DELETE' });
            if (res.success) successCount++; else failCount++;
          } else {
            const enabled = action === 'enable';
            const res = await fetchAPIWithAuth(`/admin/channels/${id}`, {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ enabled: enabled })
            });
            if (res.success) successCount++; else failCount++;
          }
        } catch (e) {
          console.error(`Bulk action failed for ${id}:`, e);
          failCount++;
        }
      }));
    }

    showToast(`批量操作完成: 成功 ${successCount} 个，失败 ${failCount} 个`, failCount > 0 ? 'warning' : 'success');

    // 刷新列表并清空选择
    await loadChannels();
    clearBulkSelection();

  } catch (err) {
    showToast('批量操作异常: ' + err.message, 'error');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerHTML = originalText;
    }
  }
}

// 初始化：绑定列表容器的点击事件（事件委托）
document.addEventListener('DOMContentLoaded', () => {
  const container = document.getElementById('channels-container');
  if (container) {
    container.addEventListener('change', (e) => {
      if (e.target.classList.contains('channel-checkbox')) {
        toggleChannelSelection(e.target.dataset.id, e.target.checked);
      }
    });
  }
});

// 导出到全局
window.toggleChannelSelection = toggleChannelSelection;
window.toggleSelectAll = toggleSelectAll;
window.clearBulkSelection = clearBulkSelection;
window.executeBulkAction = executeBulkAction;
window.updateBulkUI = updateBulkUI;
window.bulkState = bulkState;
