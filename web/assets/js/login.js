(function() {
    const form = document.getElementById('login-form');
    const errorMessage = document.getElementById('error-message');
    const errorText = document.getElementById('error-text');
    const loginButton = document.getElementById('login-button');
    const passwordInput = document.getElementById('password');
    const turnstileContainer = document.getElementById('turnstile-container');

    // Turnstile 人机验证状态（enabled 由 /public/login-config 决定）
    let turnstileEnabled = false;
    let turnstileWidgetId = null;

    // 初始化 Turnstile：配置开启时动态加载 CF 脚本并渲染验证组件
    async function initTurnstile() {
      try {
        const resp = await fetchAPI('/public/login-config');
        const cfg = (resp && resp.data) || {};
        if (!cfg.turnstile_enabled || !cfg.turnstile_site_key) return;

        turnstileEnabled = true;
        turnstileContainer.style.display = 'flex';

        const script = document.createElement('script');
        script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
        script.async = true;
        script.onload = () => {
          // 嵌入式小组件（渲染在登录表单内部，非整页挑战）；flexible 宽度自适应登录窗口
          turnstileWidgetId = window.turnstile.render('#turnstile-container', {
            sitekey: cfg.turnstile_site_key,
            theme: 'auto',
            size: 'flexible',
          });
        };
        script.onerror = () => {
          showError('人机验证组件加载失败，请检查网络后刷新页面');
        };
        document.head.appendChild(script);
      } catch (e) {
        // 配置接口异常时不阻塞登录（服务端会兜底校验）
        console.error('加载登录配置失败:', e);
      }
    }
    initTurnstile();

    // 重置 Turnstile（登录失败后令牌已消耗，需重新验证）
    function resetTurnstile() {
      if (turnstileEnabled && turnstileWidgetId !== null && window.turnstile) {
        window.turnstile.reset(turnstileWidgetId);
      }
    }

    function showError(message) {
      if (window.showError) try { window.showError(message); } catch (_) {}
      errorText.textContent = message;
      errorMessage.style.display = 'flex';

      // 添加摇晃动画
      errorMessage.style.animation = 'none';
      errorMessage.offsetHeight; // 触发重绘
      errorMessage.style.animation = 'slideInUp 0.3s ease-out';
    }

    function hideError() {
      errorMessage.style.display = 'none';
    }

    function setLoading(loading) {
      if (loading) {
        loginButton.classList.add('loading');
        loginButton.disabled = true;
        passwordInput.disabled = true;
      } else {
        loginButton.classList.remove('loading');
        loginButton.disabled = false;
        passwordInput.disabled = false;
      }
    }

    // 表单提交处理
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      hideError();
      setLoading(true);

      const password = passwordInput.value;

      // 开启人机验证时必须先完成验证
      const body = { password };
      if (turnstileEnabled) {
        const token = (window.turnstile && turnstileWidgetId !== null)
          ? window.turnstile.getResponse(turnstileWidgetId)
          : '';
        if (!token) {
          showError('请先完成人机验证');
          setLoading(false);
          return;
        }
        body.turnstile_token = token;
      }

      try {
        const resp = await fetchAPI('/login', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify(body),
        });

        if (resp.success) {
          const data = resp.data || {};

          // 存储Token到localStorage
          localStorage.setItem('ccload_token', data.token);
          localStorage.setItem('ccload_token_expiry', Date.now() + data.expiresIn * 1000);

          // 登录成功，添加成功动画
          loginButton.style.background = 'linear-gradient(135deg, var(--success-500), var(--success-600))';

          setTimeout(() => {
            // 优先回到登录前的 returnUrl（来自 HTML 鉴权中间件或 ui.js），兼容旧 redirect 参数
            const urlParams = new URLSearchParams(window.location.search);
            const redirect = urlParams.get('returnUrl') || urlParams.get('redirect') || '/web/index.html';
            window.location.href = redirect;
          }, 500);
        } else {
          showError(resp.error || '密码错误，请重试');
          resetTurnstile(); // 验证令牌一次性，失败后需重新验证

          // 添加输入框摇晃动画
          passwordInput.style.animation = 'none';
          passwordInput.offsetHeight;
          passwordInput.style.animation = 'shake 0.5s ease-in-out';

          setTimeout(() => {
            passwordInput.style.animation = '';
          }, 500);
        }
      } catch (error) {
        console.error('Login error:', error);
        showError('网络连接错误，请检查网络后重试');
        resetTurnstile();
      } finally {
        setLoading(false);
      }
    });

    // 输入框焦点处理
    passwordInput.addEventListener('focus', hideError);

    // 键盘快捷键
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        hideError();
      }
    });

    // 检查URL参数中的错误信息
    const urlParams = new URLSearchParams(window.location.search);
    const errorParam = urlParams.get('error');
    if (errorParam) {
      showError(decodeURIComponent(errorParam));
    }

    // 页面加载完成后的初始化
    document.addEventListener('DOMContentLoaded', function() {
      // 聚焦到密码输入框
      setTimeout(() => {
        passwordInput.focus();
      }, 500);

      // 添加输入框摇晃动画关键帧
      const style = document.createElement('style');
      style.textContent = `
        @keyframes shake {
          0%, 100% { transform: translateX(0); }
          10%, 30%, 50%, 70%, 90% { transform: translateX(-8px); }
          20%, 40%, 60%, 80% { transform: translateX(8px); }
        }
      `;
      document.head.appendChild(style);
    });
})();
