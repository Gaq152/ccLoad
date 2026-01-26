/**
 * channels-render.js - 兼容层（向后兼容）
 * 将旧的全局函数映射到新的命名空间
 *
 * 重构说明：
 * - UI 渲染逻辑已移至 channels-ui.js (window.ChannelsUI)
 * - 业务逻辑已移至 channels-logic.js (window.ChannelsLogic)
 * - 本文件保留为兼容层，确保现有代码无需修改
 */

(function() {
  'use strict';

  // ===== UI 函数映射 (channels-ui.js) =====

  /**
   * 渲染冷却徽章
   * @deprecated 请使用 ChannelsUI.renderCooldownBadge()
   */
  window.inlineCooldownBadge = function(channel) {
    return ChannelsUI.renderCooldownBadge(channel);
  };

  /**
   * 渲染延迟徽章
   * @deprecated 请使用 ChannelsUI.renderLatencyBadge()
   */
  window.buildLatencyBadge = function(latencyMs, statusCode) {
    return ChannelsUI.renderLatencyBadge(latencyMs, statusCode);
  };

  /**
   * 渲染渠道类型徽章
   * @deprecated 请使用 ChannelsUI.renderChannelTypeBadge()
   */
  window.buildChannelTypeBadge = function(channelType) {
    return ChannelsUI.renderChannelTypeBadge(channelType);
  };

  /**
   * 获取渠道类型配置
   * @deprecated 请使用 ChannelsUI.getChannelTypeConfig()
   */
  window.getChannelTypeConfig = function(channelType) {
    return ChannelsUI.getChannelTypeConfig(channelType);
  };

  /**
   * 渲染渠道统计信息
   * @deprecated 请使用 ChannelsUI.renderChannelStatsInline()
   */
  window.renderChannelStatsInline = function(stats, cache, channelType) {
    return ChannelsUI.renderChannelStatsInline(stats, cache, channelType);
  };

  /**
   * 创建渠道卡片
   * @deprecated 请使用 ChannelsUI.createChannelCard()
   */
  window.createChannelCard = function(channel) {
    return ChannelsUI.createChannelCard(channel);
  };

  /**
   * 更新渠道延迟徽章
   * @deprecated 请使用 ChannelsUI.updateChannelLatencyBadge()
   */
  window.updateChannelLatencyBadge = function(channelId, endpoints) {
    return ChannelsUI.updateChannelLatencyBadge(channelId, endpoints);
  };

  // ===== 业务逻辑函数映射 (channels-logic.js) =====

  /**
   * 初始化渠道事件委托
   * @deprecated 请使用 ChannelsLogic.initChannelEventDelegation()
   */
  window.initChannelEventDelegation = function() {
    return ChannelsLogic.initChannelEventDelegation();
  };

  /**
   * 进入排序模式
   * @deprecated 请使用 ChannelsLogic.enterSortMode()
   */
  window.enterSortMode = function() {
    return ChannelsLogic.enterSortMode();
  };

  /**
   * 退出排序模式
   * @deprecated 请使用 ChannelsLogic.exitSortMode()
   */
  window.exitSortMode = function() {
    return ChannelsLogic.exitSortMode();
  };

  /**
   * 保存排序更改
   * @deprecated 请使用 ChannelsLogic.saveSortChanges()
   */
  window.saveSortChanges = function() {
    return ChannelsLogic.saveSortChanges();
  };

  /**
   * 切换渠道分组折叠状态
   * @deprecated 请使用 ChannelsLogic.toggleChannelGroup()
   */
  window.toggleChannelGroup = function(type) {
    return ChannelsLogic.toggleChannelGroup(type);
  };

  /**
   * 切换优先级泳道折叠状态
   * @deprecated 请使用 ChannelsLogic.togglePriorityLane()
   */
  window.togglePriorityLane = function(type, priority) {
    return ChannelsLogic.togglePriorityLane(type, priority);
  };

  /**
   * 渲染渠道列表
   * @deprecated 请使用 ChannelsLogic.renderChannels()
   */
  window.renderChannels = function(channelsToRender) {
    return ChannelsLogic.renderChannels(channelsToRender);
  };

  // ===== 兼容层信息 =====
  console.log('[channels-render.js] 兼容层已加载 - UI层和逻辑层已分离');
  console.log('  - UI 渲染: window.ChannelsUI');
  console.log('  - 业务逻辑: window.ChannelsLogic');
  console.log('  - 向后兼容: 全局函数已映射到新命名空间');
})();
