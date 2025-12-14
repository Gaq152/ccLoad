// ============================================================
// 端点管理模块（多URL支持）
// ============================================================
(function() {
  'use strict';

  // 当前编辑的渠道ID
  let currentChannelId = null;
  // 端点数据
  let endpointsData = [];
  // 自动选择开关（默认开启）
  let autoSelectEnabled = true;
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
      // 默认开启自动选择，除非明确设置为 false
      autoSelectEnabled = data.auto_select_endpoint !== false;

      // 如果端点列表为空，用渠道的初始 URL 创建默认端点
      if (endpointsData.length === 0) {
        // channels 是全局变量（定义在 channels-state.js）
        const channel = window.channels?.find(c => c.id === channelId);
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
      const channel = window.channels?.find(c => c.id === channelId);
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
      autoSelectEnabled = true; // 默认开启
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

    // 按延迟排序（有延迟数据的排前面，延迟小的排前面）
    const sortedData = [...endpointsData].sort((a, b) => {
      const aLatency = a.latency_ms;
      const bLatency = b.latency_ms;
      // 无延迟数据的排后面
      if (aLatency === null || aLatency === undefined) return 1;
      if (bLatency === null || bLatency === undefined) return -1;
      // 失败的排后面
      if (aLatency < 0) return 1;
      if (bLatency < 0) return -1;
      // 按延迟升序
      return aLatency - bLatency;
    });

    // 使用 DocumentFragment 优化批量 DOM 操作
    container.innerHTML = '';
    const fragment = document.createDocumentFragment();

    sortedData.forEach((ep, sortIndex) => {
      // 找到原始索引（用于操作）
      const originalIndex = endpointsData.findIndex(e => e.url === ep.url);
      const isActive = ep.is_active;
      const latencyMs = ep.latency_ms;
      const statusCode = ep.status_code;

      // 延迟显示
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

      // 状态码显示
      let statusText = '';
      if (statusCode) {
        const statusClass = statusCode >= 200 && statusCode < 300 ? 'status-ok' : 'status-error';
        statusText = `<span class="${statusClass}">${statusCode}</span>`;
      }

      // 合并延迟和状态码
      let testResultText = '';
      if (latencyText || statusText) {
        const parts = [];
        if (latencyText) parts.push(`<span class="${latencyClass}">${latencyText}</span>`);
        if (statusText) parts.push(statusText);
        testResultText = parts.join('<br>');
      }

      // 修复：传入模板 ID 字符串，而非 DOM 元素
      const item = TemplateEngine.render('tpl-endpoint-item', {
        id: ep.id || 0,
        index: originalIndex,
        url: ep.url,
        activeClass: isActive ? 'active' : '',
        indicatorTitle: isActive ? '当前激活' : '点击选择',
        latencyText: testResultText,
        latencyClass: '' // 已在 testResultText 中包含样式
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
      // 如果端点还没保存到数据库（id=0），先保存
      const needSave = endpointsData.some(ep => !ep.id || ep.id === 0);
      if (needSave) {
        const autoSelect = document.getElementById('autoSelectEndpoint').checked;
        const saveRes = await fetchWithAuth(`/admin/channels/${currentChannelId}/endpoints`, {
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
        if (!saveRes.ok) {
          throw new Error('保存端点失败');
        }
      }

      const res = await fetchWithAuth(`/admin/channels/${currentChannelId}/endpoints/test`, {
        method: 'POST'
      });

      if (!res.ok) {
        throw new Error('测速请求失败');
      }

      const data = await res.json();

      // 更新端点数据（优先用 endpoints，包含完整信息）
      if (data.endpoints && data.endpoints.length > 0) {
        // 合并测速结果的状态码到端点数据
        const results = data.data || [];
        data.endpoints.forEach(ep => {
          const result = results.find(r => r.id === ep.id || r.url === ep.url);
          if (result) {
            ep.status_code = result.status_code;
            ep.test_count = result.test_count;
          }
        });
        endpointsData = data.endpoints;
      } else if (data.data && data.data.length > 0) {
        // 合并测速结果到现有数据
        data.data.forEach(result => {
          const ep = endpointsData.find(e => e.id === result.id || e.url === result.url);
          if (ep) {
            ep.latency_ms = result.latency_ms;
            ep.status_code = result.status_code;
            ep.test_count = result.test_count;
          }
        });
      }

      // 更新自动选择状态（可能已自动切换）
      autoSelectEnabled = document.getElementById('autoSelectEndpoint').checked;

      renderEndpointList();

      // 显示测速结果摘要
      const results = data.data || [];
      const successCount = results.filter(r => r.latency_ms >= 0).length;
      const failCount = results.filter(r => r.latency_ms < 0).length;
      const fastestResult = results.filter(r => r.latency_ms >= 0).sort((a, b) => a.latency_ms - b.latency_ms)[0];

      let msg = `测速完成（每端点测试3次取平均）：${successCount} 成功`;
      if (failCount > 0) msg += `，${failCount} 失败`;
      if (fastestResult) msg += `，最快 ${fastestResult.latency_ms}ms`;
      showSuccess(msg);

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

      // 刷新渠道列表（强制刷新，清除缓存）
      if (typeof invalidateChannelsCache === 'function') {
        invalidateChannelsCache();
      }
      if (typeof loadChannels === 'function') {
        // filters 是全局变量（定义在 channels-state.js）
        const channelType = (typeof filters !== 'undefined' && filters.channelType) || 'all';
        loadChannels(channelType, true);
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
