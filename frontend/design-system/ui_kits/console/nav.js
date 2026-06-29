// HUAKAI internal navigation IA — two portals, grouped.
// id scheme: top-level leaf = "<portal>:<group>"; sub-item = "<portal>:<group>:<item>".
window.HK_NAV = {
  ops: {
    label: "运营台",
    home: "运营总览",
    groups: [
      { label: "运营总览", icon: "layout-dashboard" },
      { label: "账号池", icon: "database", items: ["上游账号", "账号健康", "出口代理", "TLS 指纹"] },
      { label: "路由与分组", icon: "route", items: ["路由规则", "分组", "池绑定", "路由测试"] },
      { label: "用户与租户", icon: "users", items: ["用户列表", "用户详情", "余额记录", "安全状态", "社交绑定"] },
      { label: "模型与定价", icon: "boxes", items: ["模型列表", "模型注册", "上游模型同步", "定价规则", "缓存价格覆盖"] },
      { label: "计费运营", icon: "receipt", items: ["订单", "订阅", "兑换码", "分销", "支付争议", "账单导出"] },
      { label: "用量分析", icon: "bar-chart-3", items: ["请求明细", "排行榜", "性能指标", "健康评分", "成本分析"] },
      { label: "内容运营", icon: "megaphone", items: ["公告", "站内信", "内容审核"] },
      { label: "风控", icon: "shield-alert", items: ["风控总览", "风险事件", "拦截规则"] },
      { label: "监控告警", icon: "bell", items: ["系统健康", "运维面板", "告警规则", "告警事件", "静默规则", "死信队列"] },
      { label: "审计", icon: "file-check-2", items: ["审计事件", "用户活动", "凭据审计"] },
      { label: "系统维护", icon: "settings", items: ["平台设置", "备份", "版本", "日志诊断"] },
    ],
  },
  user: {
    label: "用户门户",
    home: "概览",
    groups: [
      { label: "概览", icon: "layout-dashboard" },
      { label: "API Key", icon: "key-round", items: ["Key 列表", "Playground"] },
      { label: "用量", icon: "bar-chart-3", items: ["用量概览", "请求明细", "媒体任务"] },
      { label: "模型与渠道", icon: "layers", items: ["可用渠道", "我的分组"] },
      { label: "钱包与订单", icon: "wallet", items: ["钱包", "订单", "订阅", "兑换", "签到"] },
      { label: "邀请返利", icon: "gift", items: ["邀请概览", "返利记录"] },
      { label: "账户", icon: "user-round", items: ["个人资料", "通知", "活动日志"] },
    ],
  },
};

// Resolve an id to { portalLabel, groupLabel, groupIcon, itemLabel }.
window.HK_NAV_FIND = function (id) {
  const [portal, groupLabel, itemLabel] = id.split(":");
  const p = window.HK_NAV[portal];
  const g = p && p.groups.find((x) => x.label === groupLabel);
  return {
    portal,
    portalLabel: p ? p.label : portal,
    groupLabel,
    groupIcon: g ? g.icon : "circle",
    itemLabel, // undefined for top-level leaves
  };
};
