/**
 * 筛选栏交互功能
 * - 滚动时添加 stuck 状态类
 * - 显示/隐藏返回顶部按钮
 */

(function() {
  'use strict';

  document.addEventListener('DOMContentLoaded', () => {
    const filterBar = document.querySelector('.filter-bar');
    const scrollToTopBtn = document.getElementById('scrollToTopBtn');
    if (!filterBar) return;

    // 1. 监听滚动，添加/移除 stuck 状态类，显示/隐藏返回顶部按钮
    let isStuck = false;
    const observer = new IntersectionObserver(
      ([entry]) => {
        const shouldBeStuck = entry.intersectionRatio < 1;
        if (shouldBeStuck !== isStuck) {
          isStuck = shouldBeStuck;
          filterBar.classList.toggle('is-stuck', isStuck);

          // 显示/隐藏返回顶部按钮
          if (scrollToTopBtn) {
            scrollToTopBtn.style.display = isStuck ? 'inline-flex' : 'none';
          }
        }
      },
      { threshold: [1], rootMargin: '-1px 0px 0px 0px' }
    );
    observer.observe(filterBar);

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
