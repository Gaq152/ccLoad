/**
 * 批量用量查询管理器
 * 负责通过 SSE 异步批量查询渠道用量并渐进式更新 UI
 */
const QuotaBatchManager = {
  // 流读取器
  reader: null,

  // 状态
  isRunning: false,
  totalCount: 0,
  completedCount: 0,
  successCount: 0,
  failedCount: 0,

  // 正在处理的渠道 ID 集合
  processingSet: new Set(),

  // DOM 元素
  statusCard: null,
  progressBar: null,
  countLabel: null,
  actionLabel: null,
  cancelBtn: null,

  /**
   * 初始化
   */
  init() {
    // 创建进度悬浮窗
    this.createStatusCard();
  },

  /**
   * 创建进度悬浮窗 DOM
   */
  createStatusCard() {
    if (document.getElementById('quota-batch-status')) {
      return; // 已存在
    }

    const card = document.createElement('div');
    card.id = 'quota-batch-status';
    card.className = 'batch-status-card';
    card.innerHTML = `
      <div class="status-header">
        <span class="title">正在更新用量...</span>
        <span class="count">0/0</span>
      </div>
      <div class="progress-track">
        <div class="progress-fill" style="width: 0%"></div>
      </div>
      <div class="current-action">准备中...</div>
      <button class="cancel-btn" onclick="QuotaBatchManager.cancel()">停止</button>
    `;
    document.body.appendChild(card);

    // 缓存 DOM 元素
    this.statusCard = card;
    this.progressBar = card.querySelector('.progress-fill');
    this.countLabel = card.querySelector('.status-header .count');
    this.actionLabel = card.querySelector('.current-action');
    this.cancelBtn = card.querySelector('.cancel-btn');
  },

  /**
   * 开始批量查询
   * @param {number} concurrency - 并发数（默认从设置读取）
   */
  async start(concurrency = null) {
    if (this.isRunning) {
      showToast('批量查询正在进行中', 'warning');
      return;
    }

    // 获取并发数配置
    if (concurrency === null) {
      concurrency = await this.getConcurrencySetting();
    }

    // 重置状态
    this.isRunning = true;
    this.totalCount = 0;
    this.completedCount = 0;
    this.successCount = 0;
    this.failedCount = 0;
    this.processingSet.clear();

    // 显示进度窗
    this.statusCard.classList.add('active');
    this.updateProgress(0, 0, '正在连接...');

    // 使用 fetch + ReadableStream 替代 EventSource（支持认证）
    const url = `/admin/quota/fetch-all?concurrency=${concurrency}`;
    const token = localStorage.getItem('ccload_token');

    if (!token) {
      console.error('[QuotaBatch] 未找到认证 Token');
      this.finish(true);
      return;
    }

    try {
      const response = await fetch(url, {
        headers: {
          'Authorization': `Bearer ${token}`,
          'Accept': 'text/event-stream'
        }
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      // 保存 reader 引用以便取消
      this.reader = reader;

      // 读取流数据
      const readStream = async () => {
        try {
          while (true) {
            const { done, value } = await reader.read();

            if (done) {
              console.log('[QuotaBatch] 流结束');
              this.finish(false);
              break;
            }

            // 解码数据并添加到缓冲区
            buffer += decoder.decode(value, { stream: true });

            // 处理完整的 SSE 消息
            const lines = buffer.split('\n\n');
            buffer = lines.pop() || ''; // 保留不完整的消息

            for (const line of lines) {
              if (!line.trim()) continue;
              this.parseSSEMessage(line);
            }
          }
        } catch (error) {
          console.error('[QuotaBatch] 读取流错误:', error);
          this.finish(true);
        }
      };

      readStream();
    } catch (error) {
      console.error('[QuotaBatch] 连接失败:', error);
      this.finish(true);
    }
  },

  /**
   * 解析 SSE 消息
   * @param {string} message - SSE 消息文本
   */
  parseSSEMessage(message) {
    const lines = message.split('\n');
    let eventType = 'message';
    let data = '';

    for (const line of lines) {
      if (line.startsWith('event:')) {
        eventType = line.substring(6).trim();
      } else if (line.startsWith('data:')) {
        data = line.substring(5).trim();
      }
    }

    if (!data) return;

    try {
      const parsedData = JSON.parse(data);

      switch (eventType) {
        case 'connected':
          this.totalCount = parsedData.total || 0;
          this.updateProgress(0, this.totalCount, `准备查询 ${this.totalCount} 个渠道...`);
          console.log(`[QuotaBatch] 连接成功，总数: ${this.totalCount}`);
          break;

        case 'quota':
          this.handleResult(parsedData);
          break;

        case 'done':
          console.log(`[QuotaBatch] 批量查询完成:`, parsedData);
          this.finish(false);
          break;

        default:
          console.warn('[QuotaBatch] 未知事件类型:', eventType);
      }
    } catch (error) {
      console.error('[QuotaBatch] 解析 SSE 数据失败:', error, data);
    }
  },

  /**
   * 处理单个渠道的查询结果
   * @param {Object} result - 查询结果
   */
  handleResult(result) {
    const channelId = result.channel_id;

    // 移除处理中状态
    this.setCardLoading(channelId, false);
    this.processingSet.delete(channelId);

    // 统计
    this.completedCount++;
    if (result.success) {
      this.successCount++;
    } else {
      this.failedCount++;
    }

    // 更新进度
    const channelName = result.channel_name || `渠道 ${channelId}`;
    this.updateProgress(this.completedCount, this.totalCount, `刚刚完成: ${channelName}`);

    // 查找 DOM 元素（如果在查询期间被删除了，这里会返回 null，直接忽略即可）
    const badge = document.querySelector(`[data-channel-id="${channelId}"] .channel-quota-badge`);
    if (!badge) {
      console.warn(`[QuotaBatch] 渠道 ${channelId} 的 DOM 元素不存在，跳过更新`);
      return;
    }

    // 更新数据 & 视觉反馈
    if (result.success && result.data) {
      // 解析用量数据（复用 QuotaManager 的逻辑）
      try {
        const extractorScript = this.getExtractorScript(channelId);
        if (!extractorScript) {
          console.warn(`[QuotaBatch] 渠道 ${channelId} 未配置提取器脚本`);
          return;
        }

        // 解析 body
        let responseBody = result.data.body;
        if (typeof responseBody === 'string') {
          try {
            responseBody = JSON.parse(responseBody);
          } catch (parseErr) {
            console.warn(`[QuotaBatch] 渠道 ${channelId} body 解析失败，保持原样`);
          }
        }

        // 执行提取器
        const extractorFn = new Function('response', `return (${extractorScript})(response)`);
        const quotaData = extractorFn(responseBody);

        if (quotaData && quotaData.isValid) {
          // 更新缓存
          if (window.QuotaManager) {
            window.QuotaManager.cache[channelId] = {
              data: quotaData,
              fetchedAt: Date.now(),
              intervalSeconds: this.getIntervalSeconds(channelId)
            };
            window.QuotaManager.saveToStorage(channelId, window.QuotaManager.cache[channelId]);
          }

          // 更新 UI
          this.updateBadgeSuccess(badge, quotaData);
        } else {
          this.updateBadgeError(badge, quotaData?.error || '提取器返回无效数据');
        }
      } catch (extractError) {
        console.error(`[QuotaBatch] 渠道 ${channelId} 提取器执行失败:`, extractError);
        this.updateBadgeError(badge, extractError.message);
      }
    } else {
      // 查询失败
      this.updateBadgeError(badge, result.error || '查询失败');
    }
  },

  /**
   * 设置渠道卡片的 Loading 状态
   * @param {number} channelId - 渠道 ID
   * @param {boolean} isLoading - 是否加载中
   */
  setCardLoading(channelId, isLoading) {
    const badge = document.querySelector(`[data-channel-id="${channelId}"] .channel-quota-badge`);
    if (badge) {
      if (isLoading) {
        badge.classList.add('updating');
        this.processingSet.add(channelId);
      } else {
        badge.classList.remove('updating');
      }
    }
  },

  /**
   * 更新徽章（成功）
   * @param {HTMLElement} badge - 徽章元素
   * @param {Object} quotaData - 用量数据
   */
  updateBadgeSuccess(badge, quotaData) {
    const remaining = quotaData.remaining;
    const unit = quotaData.unit || '';

    // 颜色判断
    let colorClass = 'quota-good';
    if (typeof remaining === 'number') {
      if (remaining < 10) {
        colorClass = 'quota-danger';
      } else if (remaining < 50) {
        colorClass = 'quota-warning';
      }
    }

    // 格式化显示值
    let displayValue = '--';
    if (typeof remaining === 'number') {
      if (remaining >= 1000) {
        displayValue = remaining.toFixed(0);
      } else if (remaining >= 100) {
        displayValue = remaining.toFixed(1);
      } else {
        displayValue = remaining.toFixed(2);
      }
    } else if (remaining !== undefined && remaining !== null) {
      displayValue = String(remaining);
    }

    // XSS 防护
    const safeValue = this.escapeHtml(displayValue);
    const safeUnit = this.escapeHtml(unit || '');

    // 更新 HTML
    const unitDisplay = safeUnit ? `<span class="quota-unit">${safeUnit}</span>` : '';
    const refreshBtn = `<button type="button" class="quota-refresh-btn" onclick="QuotaManager.refresh(${badge.closest('[data-channel-id]').dataset.channelId})" title="刷新">↻</button>`;
    const countdownSpan = `<span class="quota-countdown"></span>`;
    badge.innerHTML = `<span class="quota-badge ${colorClass}" title="余额">${safeValue}${unitDisplay}</span>${refreshBtn}${countdownSpan}`;

    // 绿色闪烁动画
    badge.classList.add('updated');
    setTimeout(() => badge.classList.remove('updated'), 1000);

    // 更新倒计时
    if (window.QuotaManager) {
      const channelId = parseInt(badge.closest('[data-channel-id]').dataset.channelId);
      window.QuotaManager.updateCountdown(channelId);
    }
  },

  /**
   * 更新徽章（失败）
   * @param {HTMLElement} badge - 徽章元素
   * @param {string} error - 错误信息
   */
  updateBadgeError(badge, error) {
    const safeError = this.escapeHtml(error);
    const refreshBtn = `<button type="button" class="quota-refresh-btn" onclick="QuotaManager.refresh(${badge.closest('[data-channel-id]').dataset.channelId})" title="刷新">↻</button>`;
    badge.innerHTML = `<span class="quota-error" title="${safeError}">--</span>${refreshBtn}`;
  },

  /**
   * 更新进度显示
   * @param {number} completed - 已完成数
   * @param {number} total - 总数
   * @param {string} action - 当前操作描述
   */
  updateProgress(completed, total, action) {
    const pct = total > 0 ? (completed / total) * 100 : 0;
    this.progressBar.style.width = `${pct}%`;
    this.countLabel.textContent = `${completed}/${total}`;
    this.actionLabel.textContent = action || '';
  },

  /**
   * 完成批量查询
   * @param {boolean} hasError - 是否有错误
   */
  finish(hasError = false) {
    // 取消流读取
    if (this.reader) {
      try {
        this.reader.cancel();
      } catch (e) {
        console.warn('[QuotaBatch] 取消流读取失败:', e);
      }
      this.reader = null;
    }

    this.isRunning = false;

    // 移除所有卡片的 loading 状态（防止意外残留）
    this.processingSet.forEach(id => this.setCardLoading(id, false));
    this.processingSet.clear();

    // 更新状态
    if (hasError) {
      this.actionLabel.textContent = '更新中断';
      showToast(`批量查询中断：已完成 ${this.completedCount}/${this.totalCount}`, 'error');
    } else {
      this.actionLabel.textContent = `完成！成功: ${this.successCount}, 失败: ${this.failedCount}`;
      if (this.failedCount > 0) {
        showToast(`批量查询完成：成功 ${this.successCount}，失败 ${this.failedCount}`, 'warning');
      } else {
        showToast(`批量查询完成：全部 ${this.successCount} 个渠道成功`, 'success');
      }
    }

    // 2秒后隐藏进度条
    setTimeout(() => {
      this.statusCard.classList.remove('active');
    }, 2000);
  },

  /**
   * 取消批量查询
   */
  cancel() {
    if (!this.isRunning) return;

    if (confirm('确定要停止批量查询吗？')) {
      this.finish(true);
    }
  },

  /**
   * 获取并发数配置
   * @returns {Promise<number>} 并发数
   */
  async getConcurrencySetting() {
    try {
      const resp = await fetchDataWithAuth('/admin/settings');
      const settings = resp.settings || resp;
      if (Array.isArray(settings)) {
        const setting = settings.find(s => s.key === 'quota_batch_concurrency');
        if (setting && setting.value) {
          const val = parseInt(setting.value);
          if (val >= 1 && val <= 50) {
            return val;
          }
        }
      }
    } catch (e) {
      console.warn('[QuotaBatch] 读取并发数配置失败:', e);
    }
    return 10; // 默认值
  },

  /**
   * 获取渠道的提取器脚本
   * @param {number} channelId - 渠道 ID
   * @returns {string|null} 提取器脚本
   */
  getExtractorScript(channelId) {
    const channel = window.channels?.find(c => c.id === channelId);
    return channel?.quota_config?.extractor_script || null;
  },

  /**
   * 获取渠道的轮询间隔
   * @param {number} channelId - 渠道 ID
   * @returns {number} 轮询间隔（秒）
   */
  getIntervalSeconds(channelId) {
    const channel = window.channels?.find(c => c.id === channelId);
    return channel?.quota_config?.interval_seconds || 300;
  },

  /**
   * HTML 转义（防止 XSS）
   * @param {string} text - 要转义的文本
   * @returns {string} 转义后的文本
   */
  escapeHtml(text) {
    if (text === null || text === undefined) return '';
    return String(text)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }
};

// 页面加载时初始化
document.addEventListener('DOMContentLoaded', () => {
  QuotaBatchManager.init();
});

// 导出到全局
window.QuotaBatchManager = QuotaBatchManager;
