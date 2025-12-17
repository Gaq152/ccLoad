// ============================================================
// 内联端点管理模块（新建/编辑时直接在表单中管理端点）
// ============================================================
(function() {
  'use strict';

  // 端点数据数组
  let inlineEndpointData = [''];

  /**
   * 获取当前端点数据
   * @returns {string[]} 端点URL数组
   */
  function getInlineEndpoints() {
    return inlineEndpointData.filter(url => url && url.trim());
  }

  /**
   * 设置端点数据（编辑时使用）
   * @param {string[]} endpoints - 端点URL数组
   */
  function setInlineEndpoints(endpoints) {
    if (!endpoints || endpoints.length === 0) {
      inlineEndpointData = [''];
    } else {
      inlineEndpointData = [...endpoints];
    }
    renderInlineEndpointTable();
  }

  /**
   * 添加新端点
   */
  function addInlineEndpoint() {
    inlineEndpointData.push('');
    renderInlineEndpointTable();

    // 聚焦到新添加的输入框
    setTimeout(() => {
      const inputs = document.querySelectorAll('.inline-endpoint-input');
      if (inputs.length > 0) {
        inputs[inputs.length - 1].focus();
      }
    }, 50);
  }

  /**
   * 删除端点
   * @param {number} index - 端点索引
   */
  function deleteInlineEndpoint(index) {
    if (inlineEndpointData.length <= 1) {
      // 至少保留一个端点
      inlineEndpointData[0] = '';
      renderInlineEndpointTable();
      return;
    }
    inlineEndpointData.splice(index, 1);
    renderInlineEndpointTable();
  }

  /**
   * 更新端点URL
   * @param {number} index - 端点索引
   * @param {string} value - 新URL值
   */
  function updateInlineEndpoint(index, value) {
    if (index >= 0 && index < inlineEndpointData.length) {
      inlineEndpointData[index] = value;
      updateEndpointCount();
      // 同步到隐藏的channelUrl字段（取第一个有效URL）
      syncChannelUrl();
    }
  }

  /**
   * 同步第一个有效URL到channelUrl隐藏字段
   */
  function syncChannelUrl() {
    const validUrls = inlineEndpointData.filter(url => url && url.trim());
    const channelUrlInput = document.getElementById('channelUrl');
    if (channelUrlInput) {
      channelUrlInput.value = validUrls[0] || '';
    }
  }

  /**
   * 更新端点计数
   */
  function updateEndpointCount() {
    const countSpan = document.getElementById('inlineEndpointCount');
    if (countSpan) {
      const validCount = inlineEndpointData.filter(url => url && url.trim()).length;
      countSpan.textContent = Math.max(1, validCount);
    }
  }

  /**
   * 渲染端点表格
   */
  function renderInlineEndpointTable() {
    const tbody = document.getElementById('inlineEndpointTableBody');
    if (!tbody) return;

    // 初始化事件委托（仅一次）
    initEndpointTableEventDelegation();

    // 使用 DocumentFragment 优化批量 DOM 操作
    const fragment = document.createDocumentFragment();

    inlineEndpointData.forEach((url, index) => {
      const row = TemplateEngine.render('tpl-inline-endpoint-row', {
        index: index,
        displayIndex: index + 1,
        url: url || '',
        deleteDisplay: inlineEndpointData.length > 1 ? 'inline-flex' : 'none'
      });
      if (row) fragment.appendChild(row);
    });

    tbody.innerHTML = '';
    tbody.appendChild(fragment);

    updateEndpointCount();
    syncChannelUrl();
  }

  /**
   * 初始化端点表格事件委托
   */
  function initEndpointTableEventDelegation() {
    const tbody = document.getElementById('inlineEndpointTableBody');
    if (!tbody || tbody.dataset.endpointDelegated) return;

    tbody.dataset.endpointDelegated = 'true';

    // 处理输入框变更
    tbody.addEventListener('input', (e) => {
      const input = e.target.closest('.inline-endpoint-input');
      if (input) {
        const index = parseInt(input.dataset.index);
        updateInlineEndpoint(index, input.value);
      }
    });

    // 处理输入框焦点样式
    tbody.addEventListener('focusin', (e) => {
      const input = e.target.closest('.inline-endpoint-input');
      if (input) {
        input.style.borderColor = 'var(--primary-500)';
        input.style.boxShadow = '0 0 0 3px rgba(59,130,246,0.1)';
      }
    });

    tbody.addEventListener('focusout', (e) => {
      const input = e.target.closest('.inline-endpoint-input');
      if (input) {
        input.style.borderColor = 'var(--neutral-300)';
        input.style.boxShadow = 'none';
      }
    });

    // 处理删除按钮点击
    tbody.addEventListener('click', (e) => {
      const deleteBtn = e.target.closest('.inline-endpoint-delete-btn');
      if (deleteBtn) {
        const index = parseInt(deleteBtn.dataset.index);
        deleteInlineEndpoint(index);
      }
    });

    // 处理删除按钮悬停样式
    tbody.addEventListener('mouseover', (e) => {
      const btn = e.target.closest('.inline-endpoint-delete-btn');
      if (btn) {
        btn.style.background = 'var(--error-50)';
        btn.style.color = 'var(--error-600)';
      }
    });

    tbody.addEventListener('mouseout', (e) => {
      const btn = e.target.closest('.inline-endpoint-delete-btn');
      if (btn) {
        btn.style.background = 'transparent';
        btn.style.color = 'var(--neutral-400)';
      }
    });
  }

  /**
   * 保存端点到服务器（渠道创建/更新后调用）
   * @param {number} channelId - 渠道ID
   * @returns {Promise<boolean>} 是否成功
   */
  async function saveEndpointsToServer(channelId) {
    const validUrls = getInlineEndpoints();
    if (validUrls.length === 0) {
      // 没有端点时跳过
      return true;
    }

    try {
      const endpoints = validUrls.map((url, index) => ({
        url: url,
        is_active: index === 0 // 第一个为激活状态
      }));

      const res = await fetchWithAuth(`/admin/channels/${channelId}/endpoints`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          endpoints: endpoints,
          auto_select_endpoint: true
        })
      });

      if (!res.ok) {
        console.error('保存端点失败:', await res.text());
        return false;
      }

      return true;
    } catch (err) {
      console.error('保存端点异常:', err);
      return false;
    }
  }

  /**
   * 从服务器加载端点（编辑时使用）
   * @param {number} channelId - 渠道ID
   * @param {string} fallbackUrl - 回退URL（渠道主URL）
   */
  async function loadEndpointsFromServer(channelId, fallbackUrl) {
    try {
      const res = await fetchWithAuth(`/admin/channels/${channelId}/endpoints`);
      if (res.ok) {
        const data = await res.json();
        const endpoints = data.data || [];
        if (endpoints.length > 0) {
          // 按 is_active 排序，激活的排在前面
          endpoints.sort((a, b) => (b.is_active ? 1 : 0) - (a.is_active ? 1 : 0));
          setInlineEndpoints(endpoints.map(ep => ep.url));
          return;
        }
      }
    } catch (err) {
      console.error('加载端点失败:', err);
    }

    // 回退：使用渠道主URL
    setInlineEndpoints(fallbackUrl ? [fallbackUrl] : ['']);
  }

  /**
   * 重置端点数据（新建时使用）
   */
  function resetInlineEndpoints() {
    inlineEndpointData = [''];
    renderInlineEndpointTable();
  }

  // 导出到全局
  window.getInlineEndpoints = getInlineEndpoints;
  window.setInlineEndpoints = setInlineEndpoints;
  window.addInlineEndpoint = addInlineEndpoint;
  window.deleteInlineEndpoint = deleteInlineEndpoint;
  window.renderInlineEndpointTable = renderInlineEndpointTable;
  window.saveEndpointsToServer = saveEndpointsToServer;
  window.loadEndpointsFromServer = loadEndpointsFromServer;
  window.resetInlineEndpoints = resetInlineEndpoints;

})();
