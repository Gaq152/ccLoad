// 初始化引导页：凭容器日志中的 Setup Token 设置管理密码
(function() {
    const form = document.getElementById('setup-form');
    const errorMessage = document.getElementById('error-message');
    const errorText = document.getElementById('error-text');
    const setupButton = document.getElementById('setup-button');
    const tokenInput = document.getElementById('setup-token');
    const passwordInput = document.getElementById('setup-password');
    const confirmInput = document.getElementById('setup-password-confirm');
    const strengthHint = document.getElementById('password-strength');

    function showError(message) {
      errorText.textContent = message;
      errorMessage.style.display = 'flex';
      errorMessage.style.animation = 'none';
      errorMessage.offsetHeight;
      errorMessage.style.animation = 'slideInUp 0.3s ease-out';
    }

    function hideError() {
      errorMessage.style.display = 'none';
    }

    function setLoading(loading) {
      setupButton.classList.toggle('loading', loading);
      setupButton.disabled = loading;
      tokenInput.disabled = loading;
      passwordInput.disabled = loading;
      confirmInput.disabled = loading;
    }

    // 密码强度即时提示
    passwordInput.addEventListener('input', () => {
      const pwd = passwordInput.value;
      if (!pwd) {
        strengthHint.textContent = '';
        return;
      }
      if (pwd.length < 8) {
        strengthHint.textContent = `还差 ${8 - pwd.length} 位（至少 8 位）`;
        strengthHint.style.color = 'var(--danger-500, #ef4444)';
        return;
      }
      // 简单强度评估：长度 + 字符种类
      let kinds = 0;
      if (/[a-z]/.test(pwd)) kinds++;
      if (/[A-Z]/.test(pwd)) kinds++;
      if (/[0-9]/.test(pwd)) kinds++;
      if (/[^a-zA-Z0-9]/.test(pwd)) kinds++;
      if (pwd.length >= 16 || (pwd.length >= 12 && kinds >= 3)) {
        strengthHint.textContent = '强度：高 ✓';
        strengthHint.style.color = 'var(--success-600, #16a34a)';
      } else if (pwd.length >= 10 && kinds >= 2) {
        strengthHint.textContent = '强度：中（建议加长或混合字符种类）';
        strengthHint.style.color = 'var(--warning-600, #d97706)';
      } else {
        strengthHint.textContent = '强度：低（满足最低要求，建议加强）';
        strengthHint.style.color = 'var(--warning-600, #d97706)';
      }
    });

    [tokenInput, passwordInput, confirmInput].forEach(el => el.addEventListener('focus', hideError));

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      hideError();

      const token = tokenInput.value.trim();
      const password = passwordInput.value;
      const confirm = confirmInput.value;

      if (password.length < 8) {
        showError('密码长度至少 8 位');
        return;
      }
      if (password !== confirm) {
        showError('两次输入的密码不一致');
        return;
      }

      setLoading(true);
      try {
        const res = await fetch('/setup', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ setup_token: token, password }),
        });
        const resp = await res.json();

        if (resp.success) {
          const data = resp.data || {};
          // 设置成功即登录：服务端已签发会话
          localStorage.setItem('ccload_token', data.token);
          localStorage.setItem('ccload_token_expiry', Date.now() + data.expiresIn * 1000);

          setupButton.style.background = 'linear-gradient(135deg, var(--success-500), var(--success-600))';
          setTimeout(() => { window.location.href = '/web/index.html'; }, 600);
        } else {
          showError(resp.error || '初始化失败，请重试');
          tokenInput.style.animation = 'none';
          tokenInput.offsetHeight;
          tokenInput.style.animation = 'shake 0.5s ease-in-out';
          setTimeout(() => { tokenInput.style.animation = ''; }, 500);
        }
      } catch (err) {
        console.error('Setup error:', err);
        showError('网络连接错误，请检查网络后重试');
      } finally {
        setLoading(false);
      }
    });

    // 摇晃动画关键帧（与登录页一致）
    document.addEventListener('DOMContentLoaded', function() {
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
