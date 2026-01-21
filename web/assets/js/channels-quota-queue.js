/**
 * 用量查询请求队列管理器
 * 负责控制用量查询的并发数，避免大量并发请求导致 UI 卡顿
 */
const QuotaRequestQueue = {
  // 队列配置
  concurrency: 10, // 默认并发数
  queue: [], // 待处理的任务队列
  running: 0, // 当前正在执行的任务数

  // 统计信息
  totalProcessed: 0,
  totalFailed: 0,

  /**
   * 初始化队列
   */
  init() {
    // 从 localStorage 读取并发数配置
    try {
      const stored = localStorage.getItem('quota_request_concurrency');
      if (stored) {
        const val = parseInt(stored);
        if (val >= 1 && val <= 50) {
          this.concurrency = val;
        }
      }
    } catch (e) {
      console.warn('[QuotaQueue] 读取并发数配置失败:', e);
    }

    console.log(`[QuotaQueue] 初始化完成，并发数: ${this.concurrency}`);
  },

  /**
   * 添加任务到队列
   * @param {Object} channel - 渠道对象
   * @param {boolean} isManualRefresh - 是否为手动刷新
   * @returns {Promise} 任务 Promise
   */
  add(channel, isManualRefresh = false) {
    return new Promise((resolve, reject) => {
      const task = {
        channel,
        isManualRefresh,
        resolve,
        reject,
        addedAt: Date.now()
      };

      this.queue.push(task);
      this.process();
    });
  },

  /**
   * 处理队列中的任务
   */
  async process() {
    // 如果已达到并发上限，或队列为空，则返回
    if (this.running >= this.concurrency || this.queue.length === 0) {
      return;
    }

    // 取出队列头部的任务
    const task = this.queue.shift();
    if (!task) return;

    this.running++;

    try {
      // 执行实际的用量查询
      await this.executeTask(task);
      task.resolve();
      this.totalProcessed++;
    } catch (error) {
      console.error(`[QuotaQueue] 任务执行失败 (channel=${task.channel.id}):`, error);
      task.reject(error);
      this.totalFailed++;
    } finally {
      this.running--;

      // 继续处理队列中的下一个任务
      this.process();
    }
  },

  /**
   * 执行单个任务
   * @param {Object} task - 任务对象
   */
  async executeTask(task) {
    const { channel, isManualRefresh } = task;
    const channelId = channel.id;

    try {
      const result = await fetchAPIWithAuth(`/admin/channels/${channelId}/quota/fetch`, {
        method: 'POST'
      });

      // 统一错误处理
      if (!result.success) {
        throw new Error(result.error || '请求失败');
      }

      // 从 result.data 中提取上游响应
      const upstreamData = result.data || {};

      // 检查上游 HTTP 状态码
      const upstreamStatus = upstreamData.status_code || 200;
      if (upstreamStatus < 200 || upstreamStatus >= 300) {
        // 检查是否为 Cloudflare 拦截
        const bodyPreview = (upstreamData.body || '').substring(0, 500);
        if (bodyPreview.includes('Just a moment') || bodyPreview.includes('cf-challenge')) {
          throw new Error(`被 Cloudflare 拦截 (HTTP ${upstreamStatus})，请检查 IP 或稍后重试`);
        }
        throw new Error(`上游返回 HTTP ${upstreamStatus}`);
      }

      // 执行提取器脚本
      const extractorScript = channel.quota_config.extractor_script;
      if (!extractorScript) {
        console.warn(`[QuotaQueue] 渠道 ${channelId} 未配置提取器脚本`);
        return;
      }

      try {
        // 解析 body
        let responseBody = upstreamData.body;
        if (typeof responseBody === 'string') {
          const trimmed = responseBody.trim();
          if (trimmed.startsWith('<') && (trimmed.includes('<!DOCTYPE') || trimmed.includes('<html'))) {
            if (trimmed.includes('Just a moment') || trimmed.includes('cf-challenge')) {
              throw new Error('被 Cloudflare 拦截，请检查 IP 或稍后重试');
            }
            throw new Error('响应不是 JSON 格式（返回了 HTML 页面）');
          }
          try {
            responseBody = JSON.parse(responseBody);
          } catch (parseErr) {
            console.warn(`[QuotaQueue] 渠道 ${channelId} body 解析失败，保持原样`);
          }
        }

        const extractorFn = new Function('response', `return (${extractorScript})(response)`);
        const quotaData = extractorFn(responseBody);

        if (quotaData) {
          // 更新缓存
          if (window.QuotaManager) {
            const cacheEntry = {
              ...window.QuotaManager.cache[channelId],
              data: quotaData,
              fetchedAt: Date.now(),
              intervalSeconds: channel.quota_config?.interval_seconds || 300
            };
            window.QuotaManager.cache[channelId] = cacheEntry;

            // 保存到 localStorage
            window.QuotaManager.saveToStorage(channelId, cacheEntry);

            // 更新UI
            window.QuotaManager.updateBadge(channelId, quotaData);

            // 手动刷新时显示成功 toast
            if (isManualRefresh && quotaData.isValid) {
              const remaining = quotaData.remaining;
              const unit = quotaData.unit || '';
              const displayValue = typeof remaining === 'number' ? remaining.toFixed(2) : remaining;
              showToast(`✅ 用量刷新成功：剩余 ${displayValue}${unit}`, 'success');
            }
          }
        }
      } catch (extractError) {
        console.error(`[QuotaQueue] 渠道 ${channelId} 提取器执行失败:`, extractError);

        if (window.QuotaManager) {
          window.QuotaManager.cache[channelId] = {
            ...window.QuotaManager.cache[channelId],
            data: { isValid: false, error: extractError.message },
            fetchedAt: Date.now()
          };
          window.QuotaManager.updateBadge(channelId, { isValid: false, error: extractError.message });
        }

        if (isManualRefresh) {
          showToast(`❌ 用量刷新失败：${extractError.message}`, 'error');
        }

        throw extractError;
      }

    } catch (error) {
      console.error(`[QuotaQueue] 渠道 ${channelId} 用量获取失败:`, error);

      // 标记为错误状态
      if (window.QuotaManager) {
        window.QuotaManager.cache[channelId] = {
          ...window.QuotaManager.cache[channelId],
          data: { isValid: false, error: error.message },
          fetchedAt: Date.now()
        };
        window.QuotaManager.updateBadge(channelId, { isValid: false, error: error.message });
      }

      // 手动刷新时显示网络错误 toast
      if (isManualRefresh) {
        showToast(`❌ 用量刷新失败：${error.message}`, 'error');
      }

      throw error;
    }
  },

  /**
   * 获取队列状态
   * @returns {Object} 队列状态
   */
  getStatus() {
    return {
      concurrency: this.concurrency,
      queueLength: this.queue.length,
      running: this.running,
      totalProcessed: this.totalProcessed,
      totalFailed: this.totalFailed
    };
  },

  /**
   * 清空队列
   */
  clear() {
    this.queue = [];
    console.log('[QuotaQueue] 队列已清空');
  }
};

// 页面加载时初始化
document.addEventListener('DOMContentLoaded', () => {
  QuotaRequestQueue.init();
});

// 导出到全局
window.QuotaRequestQueue = QuotaRequestQueue;
