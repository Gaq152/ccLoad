/**
 * 自动测速倒计时悬浮窗拖拽与位置持久化
 */

(function() {
  'use strict';

  const STORAGE_KEY = 'ccload_timer_badge_position';
  const SNAP_THRESHOLD = 30; // 靠边吸附阈值（像素）
  const DEFAULT_POSITION = { top: 74, left: 20 }; // 默认位置

  let isDragging = false;
  let startX = 0;
  let startY = 0;
  let initialLeft = 0;
  let initialTop = 0;

  document.addEventListener('DOMContentLoaded', () => {
    const badge = document.getElementById('autoTestTimer');
    if (!badge) return;

    // 加载保存的位置
    loadPosition(badge);

    // 绑定拖拽事件
    badge.addEventListener('mousedown', handleMouseDown);
    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);

    // 防止拖拽时触发点击事件
    badge.addEventListener('click', (e) => {
      if (isDragging) {
        e.stopPropagation();
        e.preventDefault();
      }
    });
  });

  /**
   * 从 localStorage 加载位置
   */
  function loadPosition(badge) {
    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      if (stored) {
        const position = JSON.parse(stored);
        badge.style.top = `${position.top}px`;
        badge.style.left = `${position.left}px`;
        badge.style.right = 'auto'; // 清除默认的 right 定位
        console.log('[拖拽] 已恢复位置:', position);
      } else {
        // 使用默认位置
        badge.style.top = `${DEFAULT_POSITION.top}px`;
        badge.style.left = `${DEFAULT_POSITION.left}px`;
        badge.style.right = 'auto';
      }
    } catch (e) {
      console.error('[拖拽] 加载位置失败:', e);
    }
  }

  /**
   * 保存位置到 localStorage
   */
  function savePosition(top, left) {
    try {
      const position = { top, left };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(position));
      console.log('[拖拽] 已保存位置:', position);
    } catch (e) {
      console.error('[拖拽] 保存位置失败:', e);
    }
  }

  /**
   * 鼠标按下事件
   */
  function handleMouseDown(e) {
    const badge = e.currentTarget;
    isDragging = true;

    // 记录初始位置
    startX = e.clientX;
    startY = e.clientY;
    initialLeft = badge.offsetLeft;
    initialTop = badge.offsetTop;

    // 添加拖拽样式
    badge.classList.add('dragging');

    e.preventDefault();
  }

  /**
   * 鼠标移动事件
   */
  function handleMouseMove(e) {
    if (!isDragging) return;

    const badge = document.getElementById('autoTestTimer');
    if (!badge) return;

    // 计算新位置
    const deltaX = e.clientX - startX;
    const deltaY = e.clientY - startY;
    let newLeft = initialLeft + deltaX;
    let newTop = initialTop + deltaY;

    // 边界限制（不超出视口）
    const maxLeft = window.innerWidth - badge.offsetWidth;
    const maxTop = window.innerHeight - badge.offsetHeight;

    newLeft = Math.max(0, Math.min(newLeft, maxLeft));
    newTop = Math.max(0, Math.min(newTop, maxTop));

    // 应用新位置
    badge.style.left = `${newLeft}px`;
    badge.style.top = `${newTop}px`;
    badge.style.right = 'auto'; // 清除 right 定位

    e.preventDefault();
  }

  /**
   * 鼠标释放事件
   */
  function handleMouseUp(e) {
    if (!isDragging) return;

    const badge = document.getElementById('autoTestTimer');
    if (!badge) return;

    isDragging = false;
    badge.classList.remove('dragging');

    // 靠边吸附
    let finalLeft = badge.offsetLeft;
    let finalTop = badge.offsetTop;

    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const badgeWidth = badge.offsetWidth;
    const badgeHeight = badge.offsetHeight;

    // 左边吸附
    if (finalLeft < SNAP_THRESHOLD) {
      finalLeft = 10;
    }

    // 右边吸附
    if (finalLeft + badgeWidth > viewportWidth - SNAP_THRESHOLD) {
      finalLeft = viewportWidth - badgeWidth - 10;
    }

    // 上边吸附
    if (finalTop < SNAP_THRESHOLD) {
      finalTop = 10;
    }

    // 下边吸附
    if (finalTop + badgeHeight > viewportHeight - SNAP_THRESHOLD) {
      finalTop = viewportHeight - badgeHeight - 10;
    }

    // 应用吸附后的位置
    badge.style.left = `${finalLeft}px`;
    badge.style.top = `${finalTop}px`;

    // 保存位置
    savePosition(finalTop, finalLeft);

    e.preventDefault();
  }

  // 窗口大小改变时，确保徽章不超出视口
  window.addEventListener('resize', () => {
    const badge = document.getElementById('autoTestTimer');
    if (!badge) return;

    const maxLeft = window.innerWidth - badge.offsetWidth;
    const maxTop = window.innerHeight - badge.offsetHeight;

    let currentLeft = badge.offsetLeft;
    let currentTop = badge.offsetTop;

    if (currentLeft > maxLeft) {
      currentLeft = maxLeft - 10;
      badge.style.left = `${currentLeft}px`;
    }

    if (currentTop > maxTop) {
      currentTop = maxTop - 10;
      badge.style.top = `${currentTop}px`;
    }

    // 保存调整后的位置
    savePosition(currentTop, currentLeft);
  });
})();
