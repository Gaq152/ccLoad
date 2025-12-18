/**
 * 渠道用量管理器
 * 负责轮询获取渠道用量数据并更新UI
 */
const QuotaManager = {
  // 存储每个渠道的轮询定时器ID
  timers: {},

  // 存储用量数据缓存（包含 data, fetchedAt, intervalSeconds）
  cache: {},

  // 倒计时更新定时器
  countdownTimer: null,

  // 是否已初始化
  initialized: false,

  // localStorage 缓存键前缀（包含版本号，升级时更改版本清除旧缓存）
  STORAGE_KEY_PREFIX: 'quota_cache_v2_',

  /**
   * 从 localStorage 加载缓存
   * @param {number} channelId - 渠道ID
   * @returns {Object|null} 缓存数据
   */
  loadFromStorage(channelId) {
    try {
      const key = this.STORAGE_KEY_PREFIX + channelId;
      const stored = localStorage.getItem(key);
      if (stored) {
        return JSON.parse(stored);
      }
    } catch (e) {
      console.warn('[QuotaManager] 读取缓存失败:', e);
    }
    return null;
  },

  /**
   * 保存缓存到 localStorage
   * @param {number} channelId - 渠道ID
   * @param {Object} cacheEntry - 缓存数据
   */
  saveToStorage(channelId, cacheEntry) {
    try {
      const key = this.STORAGE_KEY_PREFIX + channelId;
      localStorage.setItem(key, JSON.stringify(cacheEntry));
    } catch (e) {
      console.warn('[QuotaManager] 保存缓存失败:', e);
    }
  },

  /**
   * 初始化用量管理器
   * @param {Array} channels - 渠道列表
   */
  init(channels) {
    if (!channels || !Array.isArray(channels)) return;

    // 清理旧的定时器（但保留缓存）
    this.cleanupTimers();

    // 为配置了用量监控的渠道进行处理
    channels.forEach(channel => {
      if (channel.quota_config?.enabled) {
        const channelId = channel.id;
        const intervalSeconds = channel.quota_config.interval_seconds || 300;

        // 尝试从 localStorage 恢复缓存
        const storedCache = this.loadFromStorage(channelId);
        if (storedCache && storedCache.fetchedAt) {
          const elapsed = (Date.now() - storedCache.fetchedAt) / 1000;

          // 如果缓存未过期，使用缓存数据
          if (elapsed < intervalSeconds) {
            // 确保 intervalSeconds 被正确设置
            this.cache[channelId] = {
              ...storedCache,
              intervalSeconds: intervalSeconds
            };
            // 延迟更新UI，确保DOM已渲染
            setTimeout(() => {
              if (storedCache.data) {
                this.updateBadge(channelId, storedCache.data);
              }
            }, 100);
            console.log(`[QuotaManager] 渠道 ${channelId} 使用缓存数据，剩余 ${Math.round(intervalSeconds - elapsed)}s`);
          } else {
            // 缓存已过期，重新获取
            this.fetchQuota(channel);
          }
        } else {
          // 无缓存，首次获取
          this.fetchQuota(channel);
        }

        // 只有渠道启用时才启动自动轮询
        if (channel.enabled) {
          this.startPollingTimer(channel);
        }
      }
    });

    // 启动倒计时更新定时器（每秒更新一次）
    this.startCountdownTimer();

    this.initialized = true;
  },

  /**
   * 开始轮询指定渠道（包含首次获取）
   * @param {Object} channel - 渠道对象
   */
  startPolling(channel) {
    if (!channel.quota_config?.enabled) return;

    const channelId = channel.id;
    const intervalSeconds = channel.quota_config.interval_seconds || 300;

    // 初始化缓存（记录轮询间隔）
    if (!this.cache[channelId]) {
      this.cache[channelId] = {};
    }
    this.cache[channelId].intervalSeconds = intervalSeconds;

    // 首次获取（不管渠道是否启用）
    this.fetchQuota(channel);

    // 只有渠道启用时才设置定时轮询
    if (channel.enabled) {
      this.startPollingTimer(channel);
    }
  },

  /**
   * 启动轮询定时器（不含首次获取）
   * @param {Object} channel - 渠道对象
   */
  startPollingTimer(channel) {
    if (!channel.quota_config?.enabled) return;

    const channelId = channel.id;
    const intervalSeconds = channel.quota_config.interval_seconds || 300;
    const intervalMs = intervalSeconds * 1000;

    // 初始化缓存
    if (!this.cache[channelId]) {
      this.cache[channelId] = {};
    }
    this.cache[channelId].intervalSeconds = intervalSeconds;

    // 清除旧定时器
    if (this.timers[channelId]) {
      clearInterval(this.timers[channelId]);
    }

    // 设置定时轮询
    this.timers[channelId] = setInterval(() => {
      this.fetchQuota(channel);
    }, intervalMs);
  },

  /**
   * 停止指定渠道的轮询
   * @param {number} channelId - 渠道ID
   */
  stopPolling(channelId) {
    if (this.timers[channelId]) {
      clearInterval(this.timers[channelId]);
      delete this.timers[channelId];
    }
  },

  /**
   * 仅清理定时器（保留缓存）
   */
  cleanupTimers() {
    Object.keys(this.timers).forEach(id => {
      clearInterval(this.timers[id]);
    });
    this.timers = {};

    if (this.countdownTimer) {
      clearInterval(this.countdownTimer);
      this.countdownTimer = null;
    }
  },

  /**
   * 清理所有定时器和缓存
   */
  cleanup() {
    this.cleanupTimers();
    this.cache = {};
  },

  /**
   * 启动倒计时更新定时器
   */
  startCountdownTimer() {
    if (this.countdownTimer) return;

    this.countdownTimer = setInterval(() => {
      this.updateAllCountdowns();
    }, 1000);
  },

  /**
   * 更新所有渠道的倒计时显示
   */
  updateAllCountdowns() {
    Object.keys(this.cache).forEach(channelId => {
      this.updateCountdown(parseInt(channelId));
    });
  },

  /**
   * 更新单个渠道的倒计时显示
   * @param {number} channelId - 渠道ID
   */
  updateCountdown(channelId) {
    const cacheEntry = this.cache[channelId];
    if (!cacheEntry || !cacheEntry.fetchedAt) return;

    const countdown = document.querySelector(`[data-channel-id="${channelId}"] .quota-countdown`);
    if (!countdown) return;

    // 检查是否有活跃的轮询定时器（有定时器说明渠道启用且在自动轮询）
    const hasActiveTimer = !!this.timers[channelId];
    if (!hasActiveTimer) {
      countdown.textContent = '已暂停';
      countdown.title = '渠道已禁用或未启用自动轮询';
      return;
    }

    const elapsed = Math.floor((Date.now() - cacheEntry.fetchedAt) / 1000);
    const remaining = Math.max(0, (cacheEntry.intervalSeconds || 300) - elapsed);

    if (remaining > 0) {
      const mins = Math.floor(remaining / 60);
      const secs = remaining % 60;
      countdown.textContent = mins > 0 ? `${mins}m${secs}s` : `${secs}s`;
      countdown.title = '距离下次自动刷新';
    } else {
      countdown.textContent = '刷新中...';
      countdown.title = '正在自动刷新';
    }
  },

  /**
   * 获取渠道用量
   * @param {Object} channel - 渠道对象
   */
  async fetchQuota(channel) {
    const channelId = channel.id;

    try {
      const res = await fetchWithAuth(`/admin/channels/${channelId}/quota/fetch`, {
        method: 'POST'
      });

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }

      const result = await res.json();

      if (!result.success) {
        throw new Error(result.error || '请求失败');
      }

      // 执行提取器脚本
      const extractorScript = channel.quota_config.extractor_script;
      if (!extractorScript) {
        console.warn(`[QuotaManager] 渠道 ${channelId} 未配置提取器脚本`);
        return;
      }

      try {
        const extractorFn = new Function('response', `return (${extractorScript})(response)`);
        const quotaData = extractorFn(result.body);

        if (quotaData) {
          // 更新缓存
          const cacheEntry = {
            ...this.cache[channelId],
            data: quotaData,
            fetchedAt: Date.now(),
            intervalSeconds: channel.quota_config?.interval_seconds || 300
          };
          this.cache[channelId] = cacheEntry;

          // 保存到 localStorage
          this.saveToStorage(channelId, cacheEntry);

          // 更新UI
          this.updateBadge(channelId, quotaData);
        }
      } catch (extractError) {
        console.error(`[QuotaManager] 渠道 ${channelId} 提取器执行失败:`, extractError);
        this.cache[channelId] = {
          ...this.cache[channelId],
          data: { isValid: false, error: extractError.message },
          fetchedAt: Date.now()
        };
        this.updateBadge(channelId, { isValid: false, error: extractError.message });
      }

    } catch (error) {
      console.error(`[QuotaManager] 渠道 ${channelId} 用量获取失败:`, error);

      // 标记为错误状态
      this.cache[channelId] = {
        ...this.cache[channelId],
        data: { isValid: false, error: error.message },
        fetchedAt: Date.now()
      };

      this.updateBadge(channelId, { isValid: false, error: error.message });
    }
  },

  /**
   * 更新渠道卡片上的用量徽章
   * @param {number} channelId - 渠道ID
   * @param {Object} quotaData - 用量数据
   */
  updateBadge(channelId, quotaData) {
    const badge = document.querySelector(`[data-channel-id="${channelId}"] .channel-quota-badge`);
    if (!badge) return;

    // 刷新按钮和倒计时HTML（精简版）
    const refreshBtn = `<button type="button" class="quota-refresh-btn" onclick="QuotaManager.refresh(${channelId})" title="刷新">↻</button>`;
    const countdownSpan = `<span class="quota-countdown"></span>`;

    if (!quotaData || !quotaData.isValid) {
      const errorMsg = quotaData?.error ? this.escapeHtml(quotaData.error) : '未知错误';
      badge.innerHTML = `<span class="quota-error" title="${errorMsg}">--</span>${refreshBtn}`;
      badge.style.display = 'inline-flex';
      return;
    }

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

    // 格式化显示值（只显示数字，最多2位小数）
    let displayValue = '--';
    if (typeof remaining === 'number') {
      // 根据数值大小智能格式化
      if (remaining >= 1000) {
        displayValue = remaining.toFixed(0); // 大数字不显示小数
      } else if (remaining >= 100) {
        displayValue = remaining.toFixed(1); // 中等数字1位小数
      } else {
        displayValue = remaining.toFixed(2); // 小数字2位小数
      }
    } else if (remaining !== undefined && remaining !== null) {
      displayValue = String(remaining);
    }

    // XSS防护
    const safeValue = this.escapeHtml(displayValue);
    const safeUnit = this.escapeHtml(unit || '');

    // 精简显示：数字 + 单位 + 刷新按钮 + 倒计时
    const unitDisplay = safeUnit ? `<span class="quota-unit">${safeUnit}</span>` : '';
    badge.innerHTML = `<span class="quota-badge ${colorClass}" title="余额">${safeValue}${unitDisplay}</span>${refreshBtn}${countdownSpan}`;
    badge.style.display = 'inline-flex';

    // 立即更新倒计时显示
    this.updateCountdown(channelId);
  },

  /**
   * 获取缓存的用量数据
   * @param {number} channelId - 渠道ID
   * @returns {Object|null} 用量数据
   */
  getCachedQuota(channelId) {
    return this.cache[channelId]?.data || null;
  },

  /**
   * 刷新指定渠道的用量（手动触发）
   * @param {number} channelId - 渠道ID
   */
  refresh(channelId) {
    const channel = window.channels?.find(c => c.id === channelId);
    // 手动刷新：只需要用量监控启用即可，不要求渠道启用
    if (channel && channel.quota_config?.enabled) {
      // 如果渠道启用，重置轮询定时器
      if (channel.enabled && this.timers[channelId]) {
        clearInterval(this.timers[channelId]);
        const intervalMs = (channel.quota_config.interval_seconds || 300) * 1000;
        this.timers[channelId] = setInterval(() => {
          this.fetchQuota(channel);
        }, intervalMs);
      }
      // 立即获取
      this.fetchQuota(channel);
    }
  },

  /**
   * 刷新所有渠道的用量（手动触发）
   */
  refreshAll() {
    if (!window.channels) return;

    window.channels.forEach(channel => {
      // 渠道必须启用，且用量监控也必须启用
      if (channel.enabled && channel.quota_config?.enabled) {
        this.fetchQuota(channel);
      }
    });
  },

  /**
   * HTML转义（防止XSS，包括属性注入）
   * @param {string} text - 要转义的文本
   * @returns {string} 转义后的文本
   */
  escapeHtml(text) {
    // 注意：不能用 !text 判断，否则数值 0 会被当作 false
    if (text === null || text === undefined) return '';
    return String(text)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }
};

// 导出到全局
window.QuotaManager = QuotaManager;
