/**
 * channels-bulk.js
 * 渠道管理批量操作逻辑
 */

// 批量选择状态管理
const bulkState = {
  selectedIds: new Set(),
  isShiftPressed: false,
  lastCheckedId: null,

  // 持久化相关
  storageKey: 'ccload_bulk_selected_channels',

  /**
   * 从 localStorage 加载选中状态
   */
  loadFromStorage() {
    try {
      const stored = localStorage.getItem(this.storageKey);
      if (stored) {
        const ids = JSON.parse(stored);
        this.selectedIds = new Set(ids);
        console.log('[批量选择] 已恢复选中状态:', ids.length, '个渠道');
      }
    } catch (e) {
      console.error('[批量选择] 加载状态失败:', e);
    }
  },

  /**
   * 保存选中状态到 localStorage
   */
  saveToStorage() {
    try {
      const ids = Array.from(this.selectedIds);
      localStorage.setItem(this.storageKey, JSON.stringify(ids));
    } catch (e) {
      console.error('[批量选择] 保存状态失败:', e);
    }
  },

  /**
   * 清除持久化状态
   */
  clearStorage() {
    try {
      localStorage.removeItem(this.storageKey);
    } catch (e) {
      console.error('[批量选择] 清除状态失败:', e);
    }
  }
};

// 页面加载时恢复选中状态
document.addEventListener('DOMContentLoaded', () => {
  bulkState.loadFromStorage();
});

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

  // 保存到 localStorage
  bulkState.saveToStorage();

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

  // 保存到 localStorage
  bulkState.saveToStorage();

  updateBulkUI();
  updatePriorityLaneCheckboxes();
}

/**
 * 全选/取消全选优先级泳道内的渠道
 * @param {string} type - 渠道类型
 * @param {number} priority - 优先级
 * @param {boolean} checked - 是否选中
 */
function togglePriorityLaneSelectAll(type, priority, checked) {
  const lane = document.querySelector(`.priority-group[data-type="${type}"][data-priority="${priority}"]`);
  if (!lane) return;

  const cards = lane.querySelectorAll('.channel-card');
  cards.forEach(card => {
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

  if (checked && cards.length > 0) {
    bulkState.lastCheckedId = parseInt(cards[cards.length - 1].dataset.channelId);
  }

  // 保存到 localStorage
  bulkState.saveToStorage();

  updateBulkUI();
}

/**
 * 全选/取消全选某个渠道类型的所有渠道
 * @param {string} type - 渠道类型
 * @param {boolean} checked - 是否选中
 */
function toggleTypeSelectAll(type, checked) {
  const typeGroup = document.querySelector(`.channel-type-group[data-type="${type}"]`);
  if (!typeGroup) return;

  const cards = typeGroup.querySelectorAll('.channel-card');
  cards.forEach(card => {
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

  if (checked && cards.length > 0) {
    bulkState.lastCheckedId = parseInt(cards[cards.length - 1].dataset.channelId);
  }

  // 保存到 localStorage
  bulkState.saveToStorage();

  updateBulkUI();
}

/**
 * 更新所有优先级泳道的全选复选框状态
 */
function updatePriorityLaneCheckboxes() {
  document.querySelectorAll('.priority-select-all').forEach(checkbox => {
    const type = checkbox.dataset.type;
    const priority = checkbox.dataset.priority;
    const lane = document.querySelector(`.priority-group[data-type="${type}"][data-priority="${priority}"]`);

    if (lane) {
      const cards = lane.querySelectorAll('.channel-card');
      const selectedCount = Array.from(cards).filter(card =>
        bulkState.selectedIds.has(parseInt(card.dataset.channelId))
      ).length;

      if (selectedCount === 0) {
        checkbox.checked = false;
        checkbox.indeterminate = false;
      } else if (selectedCount === cards.length) {
        checkbox.checked = true;
        checkbox.indeterminate = false;
      } else {
        checkbox.checked = false;
        checkbox.indeterminate = true;
      }
    }
  });
}

/**
 * 更新所有渠道类型的全选复选框状态
 */
function updateTypeSelectAllCheckboxes() {
  document.querySelectorAll('.type-select-all').forEach(checkbox => {
    const type = checkbox.dataset.type;
    const typeGroup = document.querySelector(`.channel-type-group[data-type="${type}"]`);

    if (typeGroup) {
      const cards = typeGroup.querySelectorAll('.channel-card');
      const selectedCount = Array.from(cards).filter(card =>
        bulkState.selectedIds.has(parseInt(card.dataset.channelId))
      ).length;

      if (selectedCount === 0) {
        checkbox.checked = false;
        checkbox.indeterminate = false;
      } else if (selectedCount === cards.length) {
        checkbox.checked = true;
        checkbox.indeterminate = false;
      } else {
        checkbox.checked = false;
        checkbox.indeterminate = true;
      }
    }
  });
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

  // 更新优先级泳道的全选复选框
  updatePriorityLaneCheckboxes();

  // 更新渠道类型的全选复选框
  updateTypeSelectAllCheckboxes();
}

/**
 * 取消所有选择
 */
function clearBulkSelection() {
  bulkState.selectedIds.clear();
  bulkState.lastCheckedId = null;

  // 清除持久化状态
  bulkState.clearStorage();

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
    // 显示批量删除确认模态框
    document.getElementById('bulkDeleteCount').textContent = ids.length;
    document.getElementById('bulkDeleteModal').classList.add('show');
    return;
  }

  await performBulkAction(action, ids);
}

/**
 * 关闭批量删除确认模态框
 */
function closeBulkDeleteModal() {
  document.getElementById('bulkDeleteModal').classList.remove('show');
}

/**
 * 确认批量删除
 */
async function confirmBulkDelete() {
  closeBulkDeleteModal();
  const ids = Array.from(bulkState.selectedIds);
  await performBulkAction('delete', ids);
}

/**
 * 执行批量操作（内部实现）
 */
async function performBulkAction(action, ids) {
  // 保存当前滚动位置
  const scrollPosition = window.scrollY || document.documentElement.scrollTop;

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

    // 清除缓存并刷新列表
    if (typeof invalidateChannelsCache === 'function') {
      invalidateChannelsCache();
    }
    await loadChannels('all', true);  // 强制刷新

    // 恢复滚动位置
    window.scrollTo(0, scrollPosition);

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
    // 监听复选框的 change 事件
    container.addEventListener('change', (e) => {
      if (e.target.classList.contains('channel-checkbox')) {
        toggleChannelSelection(e.target.dataset.id, e.target.checked);
      }
    });

    // 监听复选框容器的点击事件，扩大可点击区域
    container.addEventListener('click', (e) => {
      const checkboxCol = e.target.closest('.col-checkbox');
      if (checkboxCol) {
        const checkbox = checkboxCol.querySelector('.channel-checkbox');
        if (checkbox && e.target !== checkbox) {
          // 切换复选框状态
          checkbox.checked = !checkbox.checked;
          // 触发 change 事件
          checkbox.dispatchEvent(new Event('change', { bubbles: true }));
        }
      }
    });
  }
});

// 导出到全局
window.toggleChannelSelection = toggleChannelSelection;
window.toggleSelectAll = toggleSelectAll;
window.togglePriorityLaneSelectAll = togglePriorityLaneSelectAll;
window.clearBulkSelection = clearBulkSelection;
window.executeBulkAction = executeBulkAction;
window.closeBulkDeleteModal = closeBulkDeleteModal;
window.confirmBulkDelete = confirmBulkDelete;
window.updateBulkUI = updateBulkUI;
window.updatePriorityLaneCheckboxes = updatePriorityLaneCheckboxes;
window.bulkState = bulkState;
