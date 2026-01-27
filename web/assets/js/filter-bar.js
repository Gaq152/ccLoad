/**
 * 筛选栏交互功能
 * - 滚动时添加 stuck 状态类
 * - 显示/隐藏返回顶部按钮
 */

(function() {
  'use strict';

  document.addEventListener('DOMContentLoaded', () => {
    const filterBar = document.querySelector('.filter-bar');
    const sentinel = document.getElementById('filter-bar-sentinel');
    const scrollToTopBtn = document.getElementById('scrollToTopBtn');
    if (!filterBar || !sentinel) return;

    // 1. 监听滚动 (观察哨兵元素)，添加/移除 stuck 状态类，显示/隐藏返回顶部按钮
    let isStuck = false;
    const observer = new IntersectionObserver(
      ([entry]) => {
        // 当哨兵元素滚出视口上方时 (top < 0)，说明下方的 filterBar 应该处于 sticky 状态
        const shouldBeStuck = !entry.isIntersecting && entry.boundingClientRect.top < 0;

        if (shouldBeStuck !== isStuck) {
          isStuck = shouldBeStuck;
          filterBar.classList.toggle('is-stuck', isStuck);

          // 显示/隐藏返回顶部按钮
          if (scrollToTopBtn) {
            scrollToTopBtn.style.display = isStuck ? 'inline-flex' : 'none';
          }
        }
      }
    );
    observer.observe(sentinel);

    // 2. 返回顶部按钮点击事件
    if (scrollToTopBtn) {
      scrollToTopBtn.addEventListener('click', () => {
        window.scrollTo({
          top: 0,
          behavior: 'smooth'
        });
      });
    }
  });
})();
