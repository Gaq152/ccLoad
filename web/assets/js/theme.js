/**
 * Theme System - ccLoad
 * 主题切换系统：支持 Dark/Light 模式，持久化存储，系统偏好跟随
 * 默认暗色主题
 */
const ThemeManager = (() => {
  const STORAGE_KEY = 'ccload_theme';
  const THEME_ATTR = 'data-theme';

  // SVG 图标
  const icons = {
    sun: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="12" r="5"></circle>
      <line x1="12" y1="1" x2="12" y2="3"></line>
      <line x1="12" y1="21" x2="12" y2="23"></line>
      <line x1="4.22" y1="4.22" x2="5.64" y2="5.64"></line>
      <line x1="18.36" y1="18.36" x2="19.78" y2="19.78"></line>
      <line x1="1" y1="12" x2="3" y2="12"></line>
      <line x1="21" y1="12" x2="23" y2="12"></line>
      <line x1="4.22" y1="19.78" x2="5.64" y2="18.36"></line>
      <line x1="18.36" y1="5.64" x2="19.78" y2="4.22"></line>
    </svg>`,
    moon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"></path>
    </svg>`
  };

  // 获取系统主题偏好
  function getSystemTheme() {
    return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }

  // 获取存储的主题
  function getStoredTheme() {
    return localStorage.getItem(STORAGE_KEY);
  }

  // 获取当前应该使用的主题
  function getCurrentTheme() {
    const stored = getStoredTheme();
    if (stored === 'light' || stored === 'dark') {
      return stored;
    }
    // 未存储或存储为 'system'，跟随系统（默认暗色）
    return getSystemTheme();
  }

  // 应用主题
  function applyTheme(theme, animate = false) {
    const root = document.documentElement;

    // 移除 theme-transition 类添加逻辑
    // 性能优化：避免 600+ 渠道场景下触发全页通配符过渡导致的严重卡顿
    // 仅保留 html 标签的背景色过渡和 View Transitions API

    if (theme === 'light') {
      root.setAttribute(THEME_ATTR, 'light');
    } else {
      root.removeAttribute(THEME_ATTR); // 暗色为默认，移除属性
    }

    updateToggleButton(theme);
  }

  // 更新切换按钮图标
  function updateToggleButton(theme) {
    const btn = document.getElementById('theme-toggle-btn');
    if (!btn) return;

    // 暗色模式显示太阳图标（点击切换到浅色）
    // 浅色模式显示月亮图标（点击切换到暗色）
    const icon = theme === 'dark' ? icons.sun : icons.moon;
    const title = theme === 'dark' ? '切换到浅色模式' : '切换到暗色模式';

    btn.innerHTML = icon;
    btn.setAttribute('title', title);
    btn.setAttribute('aria-label', title);

    // 添加旋转动画
    btn.classList.add('rotating');
    setTimeout(() => btn.classList.remove('rotating'), 500);
  }

  // 切换主题（支持 View Transitions API 圆形扩展动画）
  function toggle(event) {
    const current = getCurrentTheme();
    const next = current === 'dark' ? 'light' : 'dark';

    // 性能检查：如果元素过多（600+ 渠道卡片），禁用复杂动画
    // View Transitions 需要对整个页面截图，在大量 DOM 场景下会导致明显卡顿
    const cardCount = document.querySelectorAll('.glass-card').length;
    const isHeavyPage = cardCount > 200;

    // 降级处理：浏览器不支持 View Transitions 或页面元素过多
    if (!document.startViewTransition || !event || isHeavyPage) {
      localStorage.setItem(STORAGE_KEY, next);
      applyTheme(next, true);

      // 如果是因为元素过多而降级，在控制台提示（仅开发环境）
      if (isHeavyPage && console.debug) {
        console.debug(`[Theme] 检测到 ${cardCount} 个卡片元素，已禁用 View Transition 动画以优化性能`);
      }
      return;
    }

    // 获取点击位置坐标
    const x = event.clientX;
    const y = event.clientY;

    // 计算覆盖整个屏幕所需的最大半径
    const endRadius = Math.hypot(
      Math.max(x, innerWidth - x),
      Math.max(y, innerHeight - y)
    );

    // 启动视图过渡
    const transition = document.startViewTransition(() => {
      localStorage.setItem(STORAGE_KEY, next);
      applyTheme(next, false); // 禁用原有 CSS 过渡，避免冲突
    });

    // 准备就绪后执行自定义剪裁动画
    transition.ready.then(() => {
      document.documentElement.animate(
        {
          clipPath: [
            `circle(0px at ${x}px ${y}px)`,
            `circle(${endRadius}px at ${x}px ${y}px)`
          ]
        },
        {
          duration: 500,
          easing: 'ease-out',
          pseudoElement: '::view-transition-new(root)'
        }
      );
    });
  }

  // 设置指定主题
  function setTheme(theme) {
    if (theme === 'system') {
      localStorage.removeItem(STORAGE_KEY);
      applyTheme(getSystemTheme(), true);
    } else {
      localStorage.setItem(STORAGE_KEY, theme);
      applyTheme(theme, true);
    }
  }

  // 初始化
  function init() {
    // 立即应用主题（无动画，避免闪烁）
    applyTheme(getCurrentTheme(), false);

    // 监听系统主题变化
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', (e) => {
      const stored = getStoredTheme();
      // 仅当用户未手动设置主题时跟随系统
      if (!stored || stored === 'system') {
        applyTheme(e.matches ? 'light' : 'dark', true);
      }
    });

    // 页面加载完成后更新按钮
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => updateToggleButton(getCurrentTheme()));
    } else {
      updateToggleButton(getCurrentTheme());
    }
  }

  // 立即初始化
  init();

  // 暴露 API
  return {
    toggle,
    setTheme,
    getCurrentTheme,
    isDark: () => getCurrentTheme() === 'dark',
    isLight: () => getCurrentTheme() === 'light',
    refreshButton: () => updateToggleButton(getCurrentTheme())
  };
})();

// 全局访问
window.ThemeManager = ThemeManager;
