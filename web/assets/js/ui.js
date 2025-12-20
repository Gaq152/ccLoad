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
      if (!confirm('确定要注销吗？')) return;

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
      test: () => App.ui._svg(`<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>`)
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
        const resp = await App.auth.fetch('/admin/settings/nav_visible_pages');
        if (!resp.ok) return null;
        const data = await resp.json();
        const setting = data.success ? data.data : data;
        return (setting.value || '').split(',').map(s => s.trim()).filter(Boolean);
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
          h('div', { class: 'brand-text' }, 'Claude Code & Codex Proxy')
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
        onclick: () => window.ThemeManager && ThemeManager.toggle(),
        title: '切换主题'
      });

      const right = h('div', { class: 'topbar-right' }, [
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
        const res = await fetch('/public/channel-types');
        if (!res.ok) throw new Error(`Status ${res.status}`);
        const data = await res.json();
        channelTypesCache = data.data || [];
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
