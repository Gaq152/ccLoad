// ============================================================
// 端点管理模块（多URL支持）
// ============================================================
(function() {
  'use strict';

  // 当前编辑的渠道ID
  let currentChannelId = null;
  // 端点数据
  let endpointsData = [];
  // 自动选择开关
  let autoSelectEnabled = false;
  // 测速中状态
  let isTesting = false;

  // ============================================================
  // 弹窗控制
  // ============================================================

  /**
   * 打开端点管理弹窗
   * @param {number} channelId - 渠道ID
   */
  async function openEndpointModal(channelId) {
    currentChannelId = channelId;

    // 获取端点数据
    try {
      const res = await fetchWithAuth(`/admin/channels/${channelId}/endpoints`);
      if (!res.ok) {
        throw new Error('获取端点列表失败');
      }
      const data = await res.json();
      endpointsData = data.data || [];
      autoSelectEnabled = data.auto_select_endpoint || false;

      // 如果端点列表为空，用渠道的初始 URL 创建默认端点
      if (endpointsData.length === 0) {
        const channel = window.channelsCache?.find(c => c.id === channelId);
        if (channel && channel.url) {
          endpointsData = [{
            id: 0,
            url: channel.url,
            is_active: true,
            latency_ms: null
          }];
        }
      }
    } catch (err) {
      console.error('获取端点失败:', err);
      // API 请求失败时，从渠道 URL 创建默认端点
      const channel = window.channelsCache?.find(c => c.id === channelId);
      if (channel && channel.url) {
        endpointsData = [{
          id: 0,
          url: channel.url,
          is_active: true,
          latency_ms: null
        }];
      } else {
        endpointsData = [];
      }
      autoSelectEnabled = false;
    }

    // 更新UI
    document.getElementById('autoSelectEndpoint').checked = autoSelectEnabled;
    renderEndpointList();
    updateEndpointCount();

    // 显示弹窗（CSS 使用 .show 类）
    document.getElementById('endpointModal').classList.add('show');
  }

  /**
   * 关闭端点管理弹窗
   */
  function closeEndpointModal() {
    document.getElementById('endpointModal').classList.remove('show');
    currentChannelId = null;
    endpointsData = [];
  }

  // ============================================================
  // 渲染
  // ============================================================

  /**
   * 渲染端点列表
   */
  function renderEndpointList() {
    const container = document.getElementById('endpointList');

    if (endpointsData.length === 0) {
      container.innerHTML = `
        <div style="padding: 40px; text-align: center; color: var(--neutral-500);">
          暂无端点，请添加
        </div>
      `;
      return;
    }

    // 使用 DocumentFragment 优化批量 DOM 操作
    container.innerHTML = '';
    const fragment = document.createDocumentFragment();

    endpointsData.forEach((ep, index) => {
      const isActive = ep.is_active;
      const latencyMs = ep.latency_ms;

      let latencyText = '';
      let latencyClass = '';
      if (latencyMs === null || latencyMs === undefined) {
        latencyText = '';
      } else if (latencyMs < 0) {
        latencyText = '超时';
        latencyClass = 'latency-error';
      } else {
        latencyText = `${latencyMs}ms`;
        if (latencyMs < 500) {
          latencyClass = 'latency-good';
        } else if (latencyMs < 1000) {
          latencyClass = 'latency-medium';
        } else {
          latencyClass = 'latency-slow';
        }
      }

      // 修复：传入模板 ID 字符串，而非 DOM 元素
      const item = TemplateEngine.render('tpl-endpoint-item', {
        id: ep.id || 0,
        index: index,
        url: ep.url,
        activeClass: isActive ? 'active' : '',
        indicatorTitle: isActive ? '当前激活' : '点击选择',
        latencyText: latencyText,
        latencyClass: latencyClass
      });
      if (item) fragment.appendChild(item);
    });

    container.appendChild(fragment);

    // 绑定点击事件
    bindEndpointEvents();
  }

  /**
   * 绑定端点列表事件
   */
  function bindEndpointEvents() {
    const container = document.getElementById('endpointList');

    // 点击端点选择
    container.querySelectorAll('.endpoint-item').forEach(item => {
      item.addEventListener('click', (e) => {
        // 排除删除按钮点击
        if (e.target.classList.contains('endpoint-delete-btn')) return;

        const index = parseInt(item.dataset.endpointIndex);
        selectEndpoint(index);
      });
    });

    // 删除按钮
    container.querySelectorAll('.endpoint-delete-btn').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const index = parseInt(btn.dataset.index);
        deleteEndpoint(index);
      });
    });
  }

  /**
   * 更新端点计数
   */
  function updateEndpointCount() {
    document.getElementById('endpointCount').textContent = endpointsData.length;
  }

  // ============================================================
  // 端点操作
  // ============================================================

  /**
   * 添加端点
   */
  function addEndpoint() {
    const input = document.getElementById('newEndpointUrl');
    const url = input.value.trim();

    if (!url) {
      showError('请输入端点URL');
      return;
    }

    // 简单URL验证
    try {
      new URL(url);
    } catch {
      showError('请输入有效的URL');
      return;
    }

    // 检查重复
    if (endpointsData.some(ep => ep.url === url)) {
      showError('该端点已存在');
      return;
    }

    // 添加端点
    const isFirst = endpointsData.length === 0;
    endpointsData.push({
      id: 0,
      url: url,
      is_active: isFirst, // 第一个端点默认激活
      latency_ms: null
    });

    input.value = '';
    renderEndpointList();
    updateEndpointCount();
  }

  /**
   * 删除端点
   * @param {number} index - 端点索引
   */
  function deleteEndpoint(index) {
    const wasActive = endpointsData[index].is_active;
    endpointsData.splice(index, 1);

    // 如果删除的是激活端点，激活第一个
    if (wasActive && endpointsData.length > 0) {
      endpointsData[0].is_active = true;
    }

    renderEndpointList();
    updateEndpointCount();
  }

  /**
   * 选择端点（设为激活）
   * @param {number} index - 端点索引
   */
  function selectEndpoint(index) {
    endpointsData.forEach((ep, i) => {
      ep.is_active = (i === index);
    });
    renderEndpointList();
  }

  // ============================================================
  // 测速
  // ============================================================

  /**
   * 测速所有端点
   */
  async function testEndpoints() {
    if (isTesting || !currentChannelId || endpointsData.length === 0) return;

    isTesting = true;
    const btn = document.getElementById('testEndpointsBtn');
    const btnText = document.getElementById('testEndpointsBtnText');
    btn.disabled = true;
    btnText.innerHTML = '<span class="spinner-small"></span> 测速中';

    try {
      const res = await fetchWithAuth(`/admin/channels/${currentChannelId}/endpoints/test`, {
        method: 'POST'
      });

      if (!res.ok) {
        throw new Error('测速请求失败');
      }

      const data = await res.json();

      // 更新端点数据
      if (data.endpoints) {
        endpointsData = data.endpoints;
      } else if (data.data) {
        // 合并测速结果到现有数据
        data.data.forEach(result => {
          const ep = endpointsData.find(e => e.id === result.id || e.url === result.url);
          if (ep) {
            ep.latency_ms = result.latency_ms;
          }
        });
      }

      // 更新自动选择状态（可能已自动切换）
      autoSelectEnabled = document.getElementById('autoSelectEndpoint').checked;

      renderEndpointList();
      showSuccess('测速完成');

    } catch (err) {
      console.error('测速失败:', err);
      showError('测速失败: ' + err.message);
    } finally {
      isTesting = false;
      btn.disabled = false;
      btnText.textContent = '测速';
    }
  }

  // ============================================================
  // 保存
  // ============================================================

  /**
   * 保存端点配置
   */
  async function saveEndpoints() {
    if (!currentChannelId) return;

    const autoSelect = document.getElementById('autoSelectEndpoint').checked;

    try {
      const res = await fetchWithAuth(`/admin/channels/${currentChannelId}/endpoints`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          endpoints: endpointsData.map(ep => ({
            url: ep.url,
            is_active: ep.is_active
          })),
          auto_select_endpoint: autoSelect
        })
      });

      if (!res.ok) {
        throw new Error('保存失败');
      }

      showSuccess('端点配置已保存');

      // 如果编辑弹窗打开且正在编辑同一渠道，更新 URL 输入框
      const activeEndpoint = endpointsData.find(ep => ep.is_active);
      if (activeEndpoint && typeof editingChannelId !== 'undefined' && editingChannelId === currentChannelId) {
        const urlInput = document.getElementById('channelUrl');
        if (urlInput) {
          urlInput.value = activeEndpoint.url;
        }
      }

      closeEndpointModal();

      // 刷新渠道列表
      if (typeof loadChannels === 'function') {
        loadChannels();
      }

    } catch (err) {
      console.error('保存端点失败:', err);
      showError('保存失败: ' + err.message);
    }
  }

  // ============================================================
  // 事件绑定
  // ============================================================

  function initEndpointEvents() {
    // 添加端点按钮
    document.getElementById('addEndpointBtn')?.addEventListener('click', addEndpoint);

    // 回车添加
    document.getElementById('newEndpointUrl')?.addEventListener('keypress', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        addEndpoint();
      }
    });

    // 测速按钮
    document.getElementById('testEndpointsBtn')?.addEventListener('click', testEndpoints);

    // 关闭按钮
    document.getElementById('closeEndpointModalBtn')?.addEventListener('click', closeEndpointModal);

    // 保存按钮
    document.getElementById('saveEndpointsBtn')?.addEventListener('click', saveEndpoints);

    // 点击遮罩关闭
    document.getElementById('endpointModal')?.addEventListener('click', (e) => {
      if (e.target.id === 'endpointModal') {
        closeEndpointModal();
      }
    });

    // ESC 关闭
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && document.getElementById('endpointModal').classList.contains('show')) {
        closeEndpointModal();
      }
    });
  }

  // 页面加载后初始化
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initEndpointEvents);
  } else {
    initEndpointEvents();
  }

  // 导出到全局
  window.openEndpointModal = openEndpointModal;
  window.closeEndpointModal = closeEndpointModal;

})();
