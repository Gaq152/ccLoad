/**
 * ccLoad UI & Core Library
 * 重构版本：引入 App 命名空间，优化交互体验
 *
 * 命名空间结构：
 * - App.util  - 通用工具函数
 * - App.auth  - 认证管理
 * - App.ui    - UI 交互组件
 * - App.channel - 渠道类型管理
 */

(function() {
  // 初始化全局命名空间
  window.App = window.App || {};

  // ============================================================
  // App.util - 通用工具函数
  // ============================================================
  App.util = {
    /**
     * 防抖函数
     * @param {Function} func - 要防抖的函数
     * @param {number} wait - 等待时间(ms)
     * @returns {Function} 防抖后的函数
     */
    debounce: function(func, wait) {
      let timeout;
      return function executedFunction(...args) {
        const later = () => {
          clearTimeout(timeout);
          func(...args);
        };
        clearTimeout(timeout);
        timeout = setTimeout(later, wait);
      };
    },

    /**
     * 格式化成本（美元）
     * @param {number} cost - 成本值
     * @returns {string} 格式化后的字符串
     */
    formatCost: function(cost) {
      if (cost === 0) return '$0.00';
      if (cost < 0.001) {
        if (cost < 0.000001) {
          return '$' + cost.toExponential(2);
        }
        return '$' + cost.toFixed(6).replace(/\.0+$/, '');
      }
      if (cost >= 1.0) {
        return '$' + cost.toFixed(2);
      }
      return '$' + cost.toFixed(4).replace(/\.0+$/, '');
    },

    /**
     * HTML转义（防XSS）
     * @param {string} str - 需要转义的字符串
     * @returns {string} 转义后的安全字符串
     */
    escapeHtml: function(str) {
      if (str == null) return '';
      return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
    },

    /**
     * 复制文本到剪贴板（优先使用现代 API，降级支持旧浏览器）
     * @param {string} text - 要复制的文本
     * @param {string} [successMsg='已复制到剪贴板'] - 成功提示消息
     * @returns {Promise<boolean>} 是否成功
     */
    copyToClipboard: async function(text, successMsg) {
      successMsg = successMsg || '已复制到剪贴板';
      try {
        await navigator.clipboard.writeText(text);
        if (App.ui && App.ui.showToast) {
          App.ui.showToast(successMsg, 'success');
        }
        return true;
      } catch (err) {
        // 降级方案
        var textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();
        try {
          document.execCommand('copy');
          if (App.ui && App.ui.showToast) {
            App.ui.showToast(successMsg, 'success');
          }
          return true;
        } catch (e) {
          console.error('复制失败:', e);
          return false;
        } finally {
          document.body.removeChild(textarea);
        }
      }
    }
  };

  // ============================================================
  // App.auth - 认证管理
  // ============================================================
  App.auth = {
    /**
     * 跳转到登录页，并携带当前URL作为返回地址
     */
    redirectToLogin: function() {
      // 清除本地 Token
      localStorage.removeItem('ccload_token');
      localStorage.removeItem('ccload_token_expiry');

      // 构建带 returnUrl 的登录地址
      const currentUrl = window.location.href;
      // 避免循环重定向
      if (currentUrl.includes('/login.html')) return;

      const loginUrl = '/web/login.html?returnUrl=' + encodeURIComponent(currentUrl);
      window.location.href = loginUrl;
    },

    /**
     * 检查是否已登录
     * @returns {boolean}
     */
    isLoggedIn: function() {
      const token = localStorage.getItem('ccload_token');
      const expiry = localStorage.getItem('ccload_token_expiry');
      return token && (!expiry || Date.now() <= parseInt(expiry));
    },

    /**
     * 注销登录
     */
    logout: async function() {
      if (!await showConfirm({
        title: '注销确认',
        message: '确定要注销吗？',
        type: 'warning'
      })) return;

      const token = localStorage.getItem('ccload_token');
      // 先清理本地，避免 UI 闪烁
      localStorage.removeItem('ccload_token');
      localStorage.removeItem('ccload_token_expiry');

      if (token) {
        try {
          await fetch('/logout', {
            method: 'POST',
            headers: { 'Authorization': `Bearer ${token}` }
          });
        } catch (error) {
          console.error('Logout error:', error);
        }
      }
      window.location.href = '/web/login.html';
    },

    /**
     * 带 Token 认证的 fetch 封装
     * @param {string} url - 请求URL
     * @param {Object} options - fetch选项
     * @returns {Promise<Response>}
     */
    fetch: async function(url, options = {}) {
      const token = localStorage.getItem('ccload_token');
      const expiry = localStorage.getItem('ccload_token_expiry');

      // 检查 Token 是否存在或过期
      if (!token || (expiry && Date.now() > parseInt(expiry))) {
        App.auth.redirectToLogin();
        throw new Error('Token expired');
      }

      const headers = {
        ...options.headers,
        'Authorization': `Bearer ${token}`,
      };

      const response = await fetch(url, { ...options, cache: 'no-store', headers });

      // 处理 401 未授权
      if (response.status === 401) {
        App.auth.redirectToLogin();
        throw new Error('Unauthorized');
      }

      return response;
    }
  };

  // ============================================================
  // App.ui - UI 交互组件
  // ============================================================
  App.ui = {
    _bgAnimElement: null,

    /**
     * 设置按钮加载状态
     * @param {HTMLElement} btn - 按钮元素
     * @param {boolean} loading - 加载状态
     */
    setButtonLoading: function(btn, loading) {
      if (!btn) return;
      if (loading) {
        if (btn.classList.contains('loading')) return;
        btn.classList.add('loading');
        btn.disabled = true;
        // 备份 onclick 防止执行
        if (btn.onclick) {
          btn.dataset.onclickBackup = btn.onclick.toString();
          btn.onclick = null;
        }
      } else {
        btn.classList.remove('loading');
        btn.disabled = false;
        // 不恢复 onclick（现代事件绑定不需要）
        delete btn.dataset.onclickBackup;
      }
    },

    /**
     * 生成表格骨架行 HTML
     * @param {number} cols - 列数
     * @param {number} rows - 行数（默认5）
     * @returns {string} HTML 字符串
     */
    renderSkeletonRows: function(cols, rows) {
      rows = rows || 5;
      var html = '';
      for (var i = 0; i < rows; i++) {
        html += '<tr>';
        for (var j = 0; j < cols; j++) {
          var width = 60 + Math.floor(Math.random() * 30);
          html += '<td style="padding: 12px 16px;"><div class="loading-skeleton" style="height: 20px; width: ' + width + '%; border-radius: 4px;"></div></td>';
        }
        html += '</tr>';
      }
      return html;
    },

    /**
     * 导航菜单配置
     */
    navs: [
      { key: 'index', label: '概览', href: '/web/index.html', icon: 'home', required: true },
      { key: 'channels', label: '渠道管理', href: '/web/channels.html', icon: 'settings', required: true },
      { key: 'tokens', label: 'API令牌', href: '/web/tokens.html', icon: 'key', required: true },
      { key: 'stats', label: '调用统计', href: '/web/stats.html', icon: 'bars' },
      { key: 'trends', label: '请求趋势', href: '/web/trend.html', icon: 'trend' },
      { key: 'logs', label: '日志', href: '/web/logs.html', icon: 'alert', required: true },
      { key: 'monitor', label: '监控', href: '/web/monitor.html', icon: 'monitor' },
      { key: 'model-test', label: '模型测试', href: '/web/model-test.html', icon: 'test' },
      { key: 'settings', label: '设置', href: '/web/settings.html', icon: 'cog', required: true },
    ],

    /**
     * SVG 图标生成器
     */
    icons: {
      home: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2H5a2 2 0 00-2-2z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 5a2 2 0 012-2h4a2 2 0 012 2v0a2 2 0 01-2 2H10a2 2 0 01-2-2v0z"/>`),
      settings: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/>`),
      bars: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/>`),
      trend: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 12l3-3 3 3 4-4"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 21l4-4 4 4"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 4h18"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4h16v12a1 1 0 01-1 1H5a1 1 0 01-1-1V4z"/>`),
      alert: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.864-.833-2.634 0L4.18 16.5c-.77.833.192 2.5 1.732 2.5z"/>`),
      key: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z"/>`),
      cog: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/>`),
      test: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>`),
      monitor: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/>`),
      doc: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/>`),
      github: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.166 6.839 9.489.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.34-3.369-1.34-.454-1.156-1.11-1.463-1.11-1.463-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.831.092-.646.35-1.086.636-1.336-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.578 9.578 0 0112 6.836c.85.004 1.705.114 2.504.336 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.203 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.163 22 16.418 22 12c0-5.523-4.477-10-10-10z"/>`)
    },

    /**
     * SVG 元素创建辅助函数
     * @private
     */
    _svg: function(inner) {
      const el = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      el.setAttribute('fill', 'none');
      el.setAttribute('stroke', 'currentColor');
      el.setAttribute('viewBox', '0 0 24 24');
      el.classList.add('w-5', 'h-5');
      el.innerHTML = inner;
      return el;
    },

    /**
     * DOM 元素构造辅助函数
     * @param {string} tag - 标签名
     * @param {Object} attrs - 属性对象
     * @param {Array|string} children - 子元素
     * @returns {HTMLElement}
     */
    h: function(tag, attrs = {}, children = []) {
      const el = document.createElement(tag);
      Object.entries(attrs).forEach(([k, v]) => {
        if (k === 'class') el.className = v;
        else if (k === 'style') el.style.cssText = v;
        else if (k === 'innerHTML') el.innerHTML = v;
        else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
        else el.setAttribute(k, v);
      });
      (Array.isArray(children) ? children : [children]).forEach((c) => {
        if (c == null) return;
        if (typeof c === 'string') el.appendChild(document.createTextNode(c));
        else el.appendChild(c);
      });
      return el;
    },

    /**
     * 获取导航栏可见页面配置
     * @returns {Promise<string[]|null>}
     */
    getVisiblePages: async function() {
      try {
        const setting = await fetchDataWithAuth('/admin/settings/nav_visible_pages');
        return (setting?.value || '').split(',').map(s => s.trim()).filter(Boolean);
      } catch { return null; }
    },

    /**
     * 初始化顶部导航栏
     * @param {string} activeKey - 当前激活的导航项 key
     */
    initTopbar: async function(activeKey) {
      document.body.classList.add('top-layout');

      // 隐藏旧的侧边栏与移动按钮（如果存在）
      const sidebar = document.getElementById('sidebar');
      if (sidebar) sidebar.style.display = 'none';
      const mobileBtn = document.getElementById('mobile-menu-btn');
      if (mobileBtn) mobileBtn.style.display = 'none';

      const visiblePages = await App.ui.getVisiblePages();
      const topbar = App.ui._buildTopbar(activeKey, visiblePages);
      document.body.appendChild(topbar);

      // 更新主题切换按钮图标
      if (window.ThemeManager && ThemeManager.refreshButton) {
        ThemeManager.refreshButton();
      }

      // 注入背景动效
      App.ui.injectBackground();
    },

    /**
     * 构建顶部导航栏
     * @private
     */
    _buildTopbar: function(active, visiblePages) {
      const { h, icons } = App.ui;
      const bar = h('header', { class: 'topbar' });
      const left = h('div', { class: 'topbar-left' }, [
        h('div', { class: 'brand' }, [
          h('img', { class: 'brand-icon', src: '/web/favicon.svg', alt: 'Logo' }),
          h('div', { class: 'brand-text' }, 'AI Proxy'),
          h('a', {
            href: 'https://github.com/Gaq152/ccLoad',
            target: '_blank',
            title: 'GitHub',
            class: 'brand-github'
          }, [icons.github()])
        ])
      ]);

      // 过滤导航项：必选项始终显示，可选项根据配置显示
      const filteredNavs = App.ui.navs.filter(n => n.required || !visiblePages || visiblePages.includes(n.key));
      const nav = h('nav', { class: 'topnav' }, [
        ...filteredNavs.map(n => h('a', {
          class: `topnav-link ${n.key === active || (n.key === 'trends' && active === 'trend') ? 'active' : ''}`,
          href: n.href
        }, [icons[n.icon](), h('span', {}, n.label)]))
      ]);

      const loggedIn = App.auth.isLoggedIn();
      const themeBtn = h('button', {
        class: 'theme-toggle',
        id: 'theme-toggle-btn',
        onclick: (e) => window.ThemeManager && ThemeManager.toggle(e),
        title: '切换主题'
      });

      const docBtn = h('button', {
        class: 'btn btn-icon',
        onclick: () => {
          const guide = document.getElementById('guide-section');
          if (guide) {
            guide.scrollIntoView({ behavior: 'smooth' });
          } else {
            window.location.href = '/web/index.html#guide-section';
          }
        },
        title: '使用文档',
        style: 'padding: 6px; margin-right: 8px;'
      }, [icons.doc()]);

      // 版本徽章 - 开发模式显示 DEV，构建时由 GitHub Actions 替换为实际版本
      const versionBadge = h('span', {
        class: 'version-badge',
        style: 'cursor: pointer;',
        title: '点击检查更新',
        onclick: () => App.ui.checkUpdate(versionBadge.textContent)
      }, '>DEV<');

      const right = h('div', { class: 'topbar-right' }, [
        versionBadge,
        docBtn,
        themeBtn,
        h('button', {
          class: 'btn btn-secondary btn-sm',
          onclick: loggedIn ? App.auth.logout : () => window.location.href = '/web/login.html'
        }, loggedIn ? '注销' : '登录')
      ]);

      bar.appendChild(left);
      bar.appendChild(nav);
      bar.appendChild(right);
      return bar;
    },

    /**
     * 注入背景动画
     */
    injectBackground: function() {
      if (document.querySelector('.bg-anim')) return;
      App.ui._bgAnimElement = App.ui.h('div', { class: 'bg-anim' });
      document.body.appendChild(App.ui._bgAnimElement);
    },

    /**
     * 暂停背景动画（性能优化）
     */
    pauseBackgroundAnimation: function() {
      if (App.ui._bgAnimElement) {
        App.ui._bgAnimElement.style.animationPlayState = 'paused';
      }
    },

    /**
     * 恢复背景动画
     */
    resumeBackgroundAnimation: function() {
      if (App.ui._bgAnimElement) {
        App.ui._bgAnimElement.style.animationPlayState = 'running';
      }
    },

    /**
     * Toast 通知系统（限制最大显示数量）
     * @param {string} message - 消息内容
     * @param {string} type - 消息类型: 'info' | 'success' | 'error'
     */
    showToast: function(message, type = 'info') {
      let host = document.getElementById('notify-host');
      if (!host) {
        host = document.createElement('div');
        host.id = 'notify-host';
        host.style.cssText = `position: fixed; top: var(--space-6); right: var(--space-6); display: flex; flex-direction: column; gap: var(--space-2); z-index: 9999; pointer-events: none;`;
        document.body.appendChild(host);
      }

      // 限制同时显示的通知数量（最大3条）
      const maxToasts = 3;
      while (host.childElementCount >= maxToasts) {
        if (host.firstElementChild) {
          host.firstElementChild.remove();
        }
      }

      const el = document.createElement('div');
      el.className = `notification notification-${type}`;
      el.style.cssText = `
        background: var(--glass-bg);
        backdrop-filter: blur(16px);
        border: 1px solid var(--glass-border);
        border-radius: var(--radius-lg);
        padding: var(--space-4) var(--space-6);
        color: var(--neutral-900);
        font-weight: var(--font-medium);
        opacity: 0;
        transform: translateX(20px);
        transition: all var(--duration-normal) var(--timing-function);
        max-width: 360px;
        box-shadow: 0 10px 25px rgba(0,0,0,0.12);
        overflow: hidden;
        isolation: isolate;
        pointer-events: auto;
      `;

      // 根据类型设置样式
      if (type === 'success') {
        el.style.background = 'var(--theme-toast-success-bg)';
        el.style.color = '#ffffff';
        el.style.borderColor = 'transparent';
        el.style.textShadow = '0 1px 2px rgba(0,0,0,0.2)';
        el.classList.add('shadow-pulse-success');
      } else if (type === 'error') {
        el.style.background = 'var(--theme-toast-error-bg)';
        el.style.color = '#ffffff';
        el.style.borderColor = 'transparent';
        el.style.textShadow = '0 1px 2px rgba(0,0,0,0.2)';
        el.classList.add('shadow-pulse-error');
      } else if (type === 'info') {
        el.style.background = 'var(--theme-toast-info-bg)';
        el.style.color = '#ffffff';
        el.style.borderColor = 'transparent';
        el.style.textShadow = '0 1px 2px rgba(0,0,0,0.2)';
      }

      el.textContent = message;
      host.appendChild(el);

      // 动画入场
      requestAnimationFrame(() => {
        el.style.opacity = '1';
        el.style.transform = 'translateX(0)';
      });

      // 自动消失
      setTimeout(() => {
        el.style.opacity = '0';
        el.style.transform = 'translateX(20px)';
        setTimeout(() => {
          if (el.parentNode) el.parentNode.removeChild(el);
        }, 320);
      }, 3600);
    },

    /**
     * 显示确认弹窗（替代原生 confirm）
     * @param {Object} options - 配置选项
     * @param {string} options.title - 标题（默认"确认"）
     * @param {string} options.message - 提示内容
     * @param {string} options.confirmText - 确认按钮文本（默认"确定"）
     * @param {string} options.cancelText - 取消按钮文本（默认"取消"）
     * @param {string} options.type - 类型: 'danger' | 'warning' | 'info'（默认 'info'）
     * @returns {Promise<boolean>} - 用户点击确认返回 true，取消返回 false
     */
    showConfirm: function(options) {
      return new Promise((resolve) => {
        const { h } = App.ui;
        const title = options.title || '确认';
        const message = options.message || '确定要执行此操作吗？';
        const confirmText = options.confirmText || '确定';
        const cancelText = options.cancelText || '取消';
        const type = options.type || 'info';

        // 移除可能已存在的确认框
        const existing = document.getElementById('confirm-dialog-modal');
        if (existing) existing.remove();

        // 根据类型设置确认按钮样式
        let confirmBtnClass = 'btn btn-primary';
        if (type === 'danger') {
          confirmBtnClass = 'btn btn-danger';
        } else if (type === 'warning') {
          confirmBtnClass = 'btn btn-warning';
        }

        const modal = h('div', { id: 'confirm-dialog-modal', class: 'modal show' }, [
          h('div', { class: 'modal-content confirm-modal' }, [
            h('h2', { class: 'modal-title' }, title),
            h('p', { style: 'margin: 20px 0; color: var(--neutral-600);' }, message),
            h('div', { class: 'confirm-actions' }, [
              h('button', {
                class: 'btn btn-secondary',
                onclick: () => {
                  modal.remove();
                  resolve(false);
                }
              }, cancelText),
              h('button', {
                class: confirmBtnClass,
                onclick: () => {
                  modal.remove();
                  resolve(true);
                }
              }, confirmText)
            ])
          ])
        ]);

        // 点击背景取消
        modal.addEventListener('click', (e) => {
          if (e.target === modal) {
            modal.remove();
            resolve(false);
          }
        });

        // ESC 键取消
        const handleEsc = (e) => {
          if (e.key === 'Escape') {
            modal.remove();
            resolve(false);
            document.removeEventListener('keydown', handleEsc);
          }
        };
        document.addEventListener('keydown', handleEsc);

        document.body.appendChild(modal);

        // 自动聚焦取消按钮（安全默认）
        const cancelBtn = modal.querySelector('.btn-secondary');
        if (cancelBtn) cancelBtn.focus();
      });
    },

    /**
     * 显示提示弹窗（替代原生 alert）
     * @param {string|Object} options - 提示内容或配置选项
     * @param {string} options.title - 标题（默认"提示"）
     * @param {string} options.message - 提示内容
     * @param {string} options.buttonText - 按钮文本（默认"确定"）
     * @param {string} options.type - 类型: 'info' | 'warning' | 'error' | 'success'（默认 'info'）
     * @returns {Promise<void>} - 用户点击确定后 resolve
     */
    showAlert: function(options) {
      return new Promise((resolve) => {
        const { h } = App.ui;

        // 兼容直接传字符串
        if (typeof options === 'string') {
          options = { message: options };
        }

        const title = options.title || '提示';
        const message = options.message || '';
        const buttonText = options.buttonText || '确定';
        const type = options.type || 'info';

        // 移除可能已存在的提示框
        const existing = document.getElementById('alert-dialog-modal');
        if (existing) existing.remove();

        // 根据类型设置按钮样式和图标
        let btnClass = 'btn btn-primary';
        let icon = 'ℹ️';
        if (type === 'error') {
          btnClass = 'btn btn-danger';
          icon = '❌';
        } else if (type === 'warning') {
          btnClass = 'btn btn-warning';
          icon = '⚠️';
        } else if (type === 'success') {
          btnClass = 'btn btn-success';
          icon = '✅';
        }

        const modal = h('div', { id: 'alert-dialog-modal', class: 'modal show' }, [
          h('div', { class: 'modal-content confirm-modal' }, [
            h('h2', { class: 'modal-title' }, `${icon} ${title}`),
            h('p', {
              style: 'margin: 20px 0; color: var(--neutral-600); white-space: pre-wrap;'
            }, message),
            h('div', { class: 'confirm-actions', style: 'justify-content: center;' }, [
              h('button', {
                class: btnClass,
                onclick: () => {
                  modal.remove();
                  resolve();
                }
              }, buttonText)
            ])
          ])
        ]);

        // 点击背景关闭
        modal.addEventListener('click', (e) => {
          if (e.target === modal) {
            modal.remove();
            resolve();
          }
        });

        // ESC 键或 Enter 键关闭
        const handleKey = (e) => {
          if (e.key === 'Escape' || e.key === 'Enter') {
            modal.remove();
            resolve();
            document.removeEventListener('keydown', handleKey);
          }
        };
        document.addEventListener('keydown', handleKey);

        document.body.appendChild(modal);

        // 自动聚焦按钮
        const btn = modal.querySelector('button');
        if (btn) btn.focus();
      });
    },

    showUpdateToast: function(current, latest, url) {
      const { h } = App.ui;
      let host = document.getElementById('notify-host');
      if (!host) {
        host = document.createElement('div');
        host.id = 'notify-host';
        host.style.cssText = 'position: fixed; top: var(--space-6); right: var(--space-6); display: flex; flex-direction: column; gap: var(--space-2); z-index: 9999; pointer-events: none;';
        document.body.appendChild(host);
      }

      const el = h('div', {
        style: `
          background: var(--theme-toast-info-bg);
          backdrop-filter: blur(16px);
          border: 1px solid transparent;
          border-radius: var(--radius-lg);
          padding: var(--space-4) var(--space-6);
          color: #ffffff;
          font-weight: var(--font-medium);
          opacity: 0;
          transform: translateX(20px);
          transition: all var(--duration-normal) var(--timing-function);
          max-width: 360px;
          box-shadow: 0 10px 25px rgba(0,0,0,0.12);
          pointer-events: auto;
          cursor: pointer;
          text-shadow: 0 1px 2px rgba(0,0,0,0.2);
        `
      }, [
        h('div', { style: 'font-size: 13px; font-weight: 600; margin-bottom: 4px;' },
          `发现新版本 ${latest}`),
        h('div', { style: 'font-size: 11px; opacity: 0.85;' },
          `当前 ${current}，点击前往查看`)
      ]);

      if (url) {
        el.addEventListener('click', () => window.open(url, '_blank'));
      }

      host.appendChild(el);
      requestAnimationFrame(() => {
        el.style.opacity = '1';
        el.style.transform = 'translateX(0)';
      });

      setTimeout(() => {
        el.style.opacity = '0';
        el.style.transform = 'translateX(20px)';
        setTimeout(() => { if (el.parentNode) el.remove(); }, 320);
      }, 8000);
    },

    checkUpdate: async function(currentVersion) {
      const current = currentVersion.replace(/^>|<$/g, '').trim();
      if (!current || current === 'DEV') {
        showError('开发模式无法检查更新');
        return;
      }
      try {
        const data = await fetchDataWithAuth('/admin/check-update');
        const latest = data && data.latest_version;
        if (!latest) throw new Error('未获取到版本信息');

        if (latest === current) {
          showSuccess('当前已是最新版本 ' + current);
        } else {
          App.ui.showUpdateToast(current, latest, data.release_url);
        }
      } catch (e) {
        showError('检查更新失败：' + e.message);
      }
    }
  };

  // ============================================================
  // App.channel - 渠道类型管理
  // ============================================================
  App.channel = (function() {
    let channelTypesCache = null;

    // 各渠道类型的示例模型
    const modelPlaceholders = {
      'anthropic': 'claude-sonnet-4-5-20250929,claude-opus-4-5-20251101',
      'codex': 'gpt-5,gpt-5.1-codex',
      'gemini': 'gemini-3-pro,gemini-2.5-flash'
    };

    // 各渠道类型的官方 API URL
    const urlPlaceholders = {
      'anthropic': 'https://api.anthropic.com',
      'codex': 'https://api.openai.com',
      'gemini': 'https://generativelanguage.googleapis.com'
    };

    /**
     * 获取渠道类型配置（带缓存）
     */
    async function getChannelTypes() {
      if (channelTypesCache) return channelTypesCache;
      try {
        // 公开接口，使用 fetchData（无需认证）
        const data = await fetchData('/public/channel-types');
        channelTypesCache = data || [];
        return channelTypesCache;
      } catch (e) {
        console.warn('Load channel types failed:', e);
        return [];
      }
    }

    return {
      getChannelTypes,

      /**
       * 获取渠道类型对应的模型 placeholder
       */
      getModelPlaceholder: (type) => modelPlaceholders[type] || modelPlaceholders['anthropic'],

      /**
       * 获取渠道类型对应的 URL placeholder
       */
      getURLPlaceholder: (type) => urlPlaceholders[type] || urlPlaceholders['anthropic'],

      /**
       * 更新所有动态 placeholder（模型和URL）
       */
      updateAllPlaceholders: (type) => {
        const m = document.getElementById('channelModels');
        const u = document.getElementById('channelUrl');
        if (m) m.placeholder = App.channel.getModelPlaceholder(type);
        if (u) u.placeholder = App.channel.getURLPlaceholder(type);
      },

      /**
       * 渲染渠道类型单选按钮组
       */
      renderChannelTypeRadios: async (containerId, selectedValue = 'anthropic') => {
        const container = document.getElementById(containerId);
        if (!container) return;
        const types = await getChannelTypes();
        container.innerHTML = types.map(type => `
          <label style="margin-right: 15px; cursor: pointer; display: inline-flex; align-items: center;">
            <input type="radio" name="channelType" value="${App.util.escapeHtml(type.value)}"
                   ${type.value === selectedValue ? 'checked' : ''} style="margin-right: 5px;">
            <span title="${App.util.escapeHtml(type.description)}">${App.util.escapeHtml(type.display_name)}</span>
          </label>
        `).join('');
        container.querySelectorAll('input[name="channelType"]').forEach(radio => {
          radio.addEventListener('change', (e) => App.channel.updateAllPlaceholders(e.target.value));
        });
        App.channel.updateAllPlaceholders(selectedValue);
      },

      /**
       * 渲染渠道类型下拉选择框
       */
      renderChannelTypeSelect: async (selectId, selectedValue = 'anthropic') => {
        const select = document.getElementById(selectId);
        if (!select) return;
        const types = await getChannelTypes();
        select.innerHTML = types.map(type => `
          <option value="${App.util.escapeHtml(type.value)}"
                  ${type.value === selectedValue ? 'selected' : ''}
                  title="${App.util.escapeHtml(type.description)}">
            ${App.util.escapeHtml(type.display_name)}
          </option>
        `).join('');
      },

      /**
       * 渲染渠道类型过滤器下拉框
       */
      renderChannelTypeFilter: async (selectId) => {
        const select = document.getElementById(selectId);
        if (!select) return;
        const types = await getChannelTypes();
        select.innerHTML = '<option value="all">所有类型</option>' +
          types.map(type => `
            <option value="${App.util.escapeHtml(type.value)}" title="${App.util.escapeHtml(type.description)}">
              ${App.util.escapeHtml(type.display_name)}
            </option>
          `).join('');
      },

      /**
       * 渲染渠道类型Tab页
       */
      renderChannelTypeTabs: async (containerId, onTabChange, initialType = null) => {
        const container = document.getElementById(containerId);
        if (!container) return;
        const types = await getChannelTypes();
        const allTypes = [...types, { value: 'all', display_name: '全部' }];
        const activeType = initialType || (types.length > 0 ? types[0].value : 'all');
        container.innerHTML = allTypes.map((type) => `
          <button class="channel-tab ${type.value === activeType ? 'active' : ''}"
                  data-type="${App.util.escapeHtml(type.value)}">
            ${App.util.escapeHtml(type.display_name)}
          </button>
        `).join('');
        container.querySelectorAll('.channel-tab').forEach(tab => {
          tab.addEventListener('click', () => {
            container.querySelectorAll('.channel-tab').forEach(t => t.classList.remove('active'));
            tab.classList.add('active');
            if (onTabChange) onTabChange(tab.dataset.type);
          });
        });
      },

      /**
       * 获取渠道类型的显示名称
       */
      getChannelTypeDisplayName: async (value) => {
        const types = await getChannelTypes();
        const type = types.find(t => t.value === value);
        return type ? type.display_name : value;
      }
    };
  })();

})();

// ============================================================
// 向后兼容层 (Backward Compatibility)
// 保留原有的 window.xxx 全局函数，指向 App 命名空间
// ============================================================
window.fetchWithAuth = App.auth.fetch;
window.initTopbar = App.ui.initTopbar;
window.showNotification = App.ui.showToast;
window.showSuccess = (msg) => App.ui.showToast(msg, 'success');
window.showError = (msg) => App.ui.showToast(msg, 'error');
window.showWarning = (msg) => App.ui.showToast(msg, 'warning');
window.showConfirm = App.ui.showConfirm;
window.showAlert = App.ui.showAlert;
window.pauseBackgroundAnimation = App.ui.pauseBackgroundAnimation;
window.resumeBackgroundAnimation = App.ui.resumeBackgroundAnimation;
window.setButtonLoading = App.ui.setButtonLoading;
window.renderSkeletonRows = App.ui.renderSkeletonRows;
window.debounce = App.util.debounce;
window.formatCost = App.util.formatCost;
window.escapeHtml = App.util.escapeHtml;
window.copyToClipboard = App.util.copyToClipboard;

// ChannelTypeManager 保持原有对象结构，指向 App.channel
window.ChannelTypeManager = App.channel;

// ============================================================
// API响应解析（统一后端返回格式：{success,data,error,count}）
// ============================================================
(function() {
  async function parseAPIResponse(res) {
    const text = await res.text();
    if (!text) {
      throw new Error(`空响应 (HTTP ${res.status})`);
    }

    let payload;
    try {
      payload = JSON.parse(text);
    } catch (e) {
      throw new Error(`响应不是JSON (HTTP ${res.status})`);
    }

    if (!payload || typeof payload !== 'object' || typeof payload.success !== 'boolean') {
      throw new Error(`响应格式不符合APIResponse (HTTP ${res.status})`);
    }

    return payload;
  }

  async function fetchAPI(url, options = {}) {
    const res = await fetch(url, options);
    return parseAPIResponse(res);
  }

  async function fetchAPIWithAuth(url, options = {}) {
    const res = await fetchWithAuth(url, options);
    return parseAPIResponse(res);
  }

  // 需要同时读取响应头（如 X-Debug-*）的场景：返回 { res, payload }
  async function fetchAPIWithAuthRaw(url, options = {}) {
    const res = await fetchWithAuth(url, options);
    const payload = await parseAPIResponse(res);
    return { res, payload };
  }

  async function fetchData(url, options = {}) {
    const resp = await fetchAPI(url, options);
    if (!resp.success) throw new Error(resp.error || '请求失败');
    return resp.data;
  }

  async function fetchDataWithAuth(url, options = {}) {
    const resp = await fetchAPIWithAuth(url, options);
    if (!resp.success) throw new Error(resp.error || '请求失败');
    return resp.data;
  }

  window.fetchAPI = fetchAPI;
  window.fetchAPIWithAuth = fetchAPIWithAuth;
  window.fetchAPIWithAuthRaw = fetchAPIWithAuthRaw;
  window.fetchData = fetchData;
  window.fetchDataWithAuth = fetchDataWithAuth;
})();

// ============================================================
// 公共工具函数（DRY：集中定义，各页面复用）
// ============================================================
(function() {
  // 格式化数字显示（通用：K/M缩写）
  function formatNumber(num) {
    const n = Number(num);
    if (!Number.isFinite(n)) return '0';
    if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
    if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
    return n.toString();
  }

  // RPM 颜色：低流量绿色，中等橙色，高流量红色
  function getRpmColor(rpm) {
    const n = Number(rpm);
    if (!Number.isFinite(n)) return 'var(--neutral-600)';
    if (n < 10) return 'var(--success-600)';
    if (n < 100) return 'var(--warning-600)';
    return 'var(--error-600)';
  }

  // 简单显示/隐藏切换（用于日志/测试响应块等）
  function toggleResponse(elementId) {
    const el = document.getElementById(elementId);
    if (!el) return;
    el.style.display = el.style.display === 'none' ? 'block' : 'none';
  }

  // 导出到全局作用域
  window.formatNumber = formatNumber;
  window.getRpmColor = getRpmColor;
  window.toggleResponse = toggleResponse;
})();

// ============================================================
// 响应式布局优化 - 窗口大小变化监听（2025-01新增）
// 解决 F12 开启/关闭时的布局适配问题
// ============================================================
(function() {
  // 处理窗口大小变化
  function handleResize() {
    // 触发 ECharts 图表重绘（如果存在）
    if (window.myChart && typeof window.myChart.resize === 'function') {
      window.myChart.resize();
    }
    
    // 触发其他需要响应窗口变化的组件
    // 可以在这里添加更多需要响应的逻辑
    
    // 触发自定义事件，供其他模块监听
    window.dispatchEvent(new CustomEvent('app:resize', {
      detail: {
        width: window.innerWidth,
        height: window.innerHeight
      }
    }));
  }
  
  // 使用防抖优化性能（200ms）
  const debouncedResize = App.util.debounce(handleResize, 200);
  
  // 监听窗口大小变化
  window.addEventListener('resize', debouncedResize);
  
  // 导出到全局（供其他模块使用）
  window.handleAppResize = handleResize;
})();

// ============================================================
// 搜索状态管理（2025-01新增）
// 配合 content-visibility 优化，确保页面内搜索功能正常工作
// ============================================================
(function() {
  // 初始化搜索状态管理
  function initSearchStateManagement() {
    const searchInput = document.getElementById('searchInput');
    if (!searchInput) return;
    
    // 搜索框获得焦点时，标记为搜索激活状态
    searchInput.addEventListener('focus', function() {
      document.body.classList.add('search-active');
    });
    
    // 搜索框失去焦点时，如果没有搜索内容则移除激活状态
    searchInput.addEventListener('blur', function() {
      if (!searchInput.value.trim()) {
        document.body.classList.remove('search-active');
      }
    });
    
    // 搜索内容变化时，保持激活状态
    searchInput.addEventListener('input', function() {
      if (searchInput.value.trim()) {
        document.body.classList.add('search-active');
      } else {
        document.body.classList.remove('search-active');
      }
    });
    
    // 清空搜索按钮点击时，移除激活状态
    const clearSearchBtn = document.getElementById('clearSearchBtn');
    if (clearSearchBtn) {
      clearSearchBtn.addEventListener('click', function() {
        document.body.classList.remove('search-active');
      });
    }
  }
  
  // DOM 加载完成后初始化
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initSearchStateManagement);
  } else {
    initSearchStateManagement();
  }
})();

// ============================================================
// 全局 Tooltip 系统（2025-01新增）
// 解决 content-visibility 与 overflow: visible 的冲突
// 将 Tooltip 渲染在 body 根节点，不受卡片 overflow 限制
// ============================================================
(function() {
  let tooltipEl = null;
  let hideTimer = null;

  function init() {
    // 创建全局 Tooltip 元素
    tooltipEl = document.createElement('div');
    tooltipEl.className = 'global-tooltip';
    document.body.appendChild(tooltipEl);

    // 事件委托：监听所有带有 aria-label 的元素
    document.addEventListener('mouseover', handleMouseOver);
    document.addEventListener('mouseout', handleMouseOut);

    // 添加样式
    injectStyles();
  }

  function handleMouseOver(e) {
    const target = e.target.closest('[aria-label]');
    if (!target) return;

    const text = target.getAttribute('aria-label');
    if (!text) return;

    // 清除隐藏定时器
    clearTimeout(hideTimer);

    // 设置内容并显示
    tooltipEl.textContent = text;
    tooltipEl.classList.add('visible');

    // 计算位置（自动处理边界）
    updatePosition(target);
  }

  function handleMouseOut(e) {
    const target = e.target.closest('[aria-label]');
    if (!target) return;

    // 延迟隐藏，避免鼠标快速移动时闪烁
    hideTimer = setTimeout(() => {
      tooltipEl.classList.remove('visible');
    }, 100);
  }

  function updatePosition(target) {
    const rect = target.getBoundingClientRect();

    // 等待下一帧获取 Tooltip 尺寸（内容已更新）
    requestAnimationFrame(() => {
      const tooltipRect = tooltipEl.getBoundingClientRect();

      // 默认显示在上方，居中对齐
      let top = rect.top - tooltipRect.height - 8;
      let left = rect.left + (rect.width - tooltipRect.width) / 2;

      // 防止溢出屏幕左侧
      if (left < 10) left = 10;

      // 防止溢出屏幕右侧
      if (left + tooltipRect.width > window.innerWidth - 10) {
        left = window.innerWidth - tooltipRect.width - 10;
      }

      // 如果上方空间不足，显示在下方
      if (top < 10) {
        top = rect.bottom + 8;
      }

      tooltipEl.style.transform = `translate(${left}px, ${top}px)`;
    });
  }

  function injectStyles() {
    const style = document.createElement('style');
    style.textContent = `
      .global-tooltip {
        position: fixed;
        top: 0;
        left: 0;
        z-index: 99999;
        padding: 6px 10px;
        background: rgba(20, 25, 35, 0.95);
        color: white;
        font-size: 12px;
        font-weight: 500;
        border-radius: 6px;
        pointer-events: none;
        opacity: 0;
        transition: opacity 0.15s ease;
        white-space: nowrap;
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
        backdrop-filter: blur(8px);
      }

      .global-tooltip.visible {
        opacity: 1;
      }

      /* 暗色模式适配 */
      html[data-theme="light"] .global-tooltip {
        background: rgba(20, 25, 35, 0.92);
        border: 1px solid rgba(0, 0, 0, 0.1);
      }
    `;
    document.head.appendChild(style);
  }

  // DOM 加载完成后初始化
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
