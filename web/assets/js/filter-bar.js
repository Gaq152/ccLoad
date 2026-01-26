/**
 * 筛选栏交互功能
 * - 滚动时添加 stuck 状态类
 * - 点击空白区域滚动到顶部
 */

(function() {
  'use strict';

  document.addEventListener('DOMContentLoaded', () => {
    const filterBar = document.querySelector('.filter-bar');
    if (!filterBar) return;

    // 1. 监听滚动，添加/移除 stuck 状态类
    let isStuck = false;
    const observer = new IntersectionObserver(
      ([entry]) => {
        const shouldBeStuck = entry.intersectionRatio < 1;
        if (shouldBeStuck !== isStuck) {
          isStuck = shouldBeStuck;
          filterBar.classList.toggle('is-stuck', isStuck);
        }
      },
      { threshold: [1], rootMargin: '-1px 0px 0px 0px' }
    );
    observer.observe(filterBar);

    // 2. 点击空白区域滚动到顶部
    filterBar.addEventListener('click', (e) => {
      // 只在点击空白区域时触发（不是按钮、输入框等交互元素）
      const isInteractiveElement = e.target.closest('button, input, select, label, a, .header-checkbox-wrapper');

      // 只在筛选栏处于 stuck 状态（即已滚动）时才响应
      if (!isInteractiveElement && isStuck) {
        window.scrollTo({
          top: 0,
          behavior: 'smooth'
        });
      }
    });

    // 3. 添加视觉提示：鼠标悬停在空白区域时显示提示
    filterBar.addEventListener('mousemove', (e) => {
      const isInteractiveElement = e.target.closest('button, input, select, label, a, .header-checkbox-wrapper');

      if (!isInteractiveElement && isStuck) {
        filterBar.style.cursor = 'pointer';
        filterBar.title = '点击空白区域返回顶部';
      } else {
        filterBar.style.cursor = '';
        filterBar.title = '';
      }
    });
  });
})();
