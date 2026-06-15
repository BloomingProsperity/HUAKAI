// 用户自助充值 & 余额：封装 /v1/users/me/payments/* 端点（session 鉴权，走 userClient）。
// 字段形状对齐后端 internal/paymenthttp/{handler.go,user_portal.go}
// （MountPaymentUserRoutes，session bearer token 鉴权）。
//
// Owner 指令：跳过真实支付 SDK 对接 —— 只做余额展示 + 充值配置/套餐展示 +
// 创建充值订单（拿到「人工支付指引」即止，不集成 Stripe/支付宝/微信 SDK）+ 我的订单列表。
// HUAKAI 后端的自助充值渠道是 manual（扫码/转账）与 taobao（淘宝/闲鱼下单），均人工确认入账，
// 因此本页不存在「跳转第三方收银台」一步，create order 直接返回该渠道的指引文案。
//
// 借鉴来源（CLEAN-ROOM，CLAUDE.md §11/§12，仅提取功能/字段/布局形态，未抄码）：
//   - sub2api(LGPL) src/api/payment.ts + src/types/payment.ts：
//     getConfig(min/max amount)、getPlans、createOrder、getMyOrders(status 过滤)、cancelOrder
//     的「充值配置 + 预设金额 + 创建订单 + 我的订单(状态)」概念形态；其 OrderStatus 大写枚举
//     (PENDING/PAID/RECHARGING/COMPLETED/EXPIRED/CANCELLED/FAILED…) 与 HUAKAI 后端小写枚举一一对应。
//   - new-api(AGPL) web/default/src/features/wallet（use-topup-info / recharge-form-card）：
//     「topup info(min/preset amounts + payment methods) → 预设金额选择 + 自定义金额输入 →
//     创建订单」的钱包充值表单形态。AGPL 仅提取功能/字段形态，绝不抄码。
//   - CLIProxyAPI：纯 relay account→API 代理，无 payment/order/billing 模块（~/refs/CLIProxyAPI
//     无 payment package），故充值形态无对照项。
// 字段名/单位以 HUAKAI handler 真码为准（金额一律为最小货币单位整数 amount_cents，currency USD）。

import { userGet, userPost } from './userClient';

// ---- 后端枚举（与 internal/payment/types.go 一致） ----

// OrderStatus（types.go）：pending → paid → recharging → completed；或 expired/cancelled/failed。
export type OrderStatus =
  | 'pending'
  | 'paid'
  | 'recharging'
  | 'completed'
  | 'expired'
  | 'cancelled'
  | 'failed'
  | (string & {});

// OrderKind（types.go）：topup(充值入余额) / subscription(购订阅)。本页只创建 topup。
export type OrderKind = 'topup' | 'subscription' | (string & {});

// ProviderKind（types.go）：门户自助充值启用 manual / taobao，均人工确认入账。
export type ProviderKind = 'manual' | 'taobao' | (string & {});

// ---- DTO（snake_case，对齐 paymenthttp orderView / balanceView / portalConfigView） ----

// balanceView（handler.go）：用户支付来源余额（payment_credits 派生 SUM），最小货币单位整数。
export interface Balance {
  tenant_id: number;
  user_id: number;
  amount_cents: number;
}

// orderView（handler.go）：面向用户的订单 DTO（仅公开字段）。
export interface PaymentOrder {
  id: number;
  out_trade_no: string;
  user_id: number;
  amount_cents: number;
  currency_code: string;
  status: OrderStatus;
  provider_kind: ProviderKind;
  order_kind: OrderKind;
  subscription_plan_id?: number | null;
  created_at: string;
  updated_at: string;
  expires_at?: string | null;
  paid_at?: string | null;
  completed_at?: string | null;
}

// portalProviderConfigView（user_portal.go）：单渠道 + 其人工支付指引文案。
export interface PortalProviderConfig {
  provider: ProviderKind;
  instruction: string;
}

// portalConfigView（user_portal.go）：门户可充配置（金额范围 + 预设金额 + 启用渠道）。
export interface PortalConfig {
  min_topup_cents: number;
  max_topup_cents: number;
  preset_amount_cents: number[];
  currency_code: string;
  providers: PortalProviderConfig[];
}

// ---- 端点信封类型 ----

// GET /balance → newUserBalanceHandler
export interface BalanceResponse {
  balance: Balance;
}

// GET /config → newPortalConfigHandler
export interface ConfigResponse {
  config: PortalConfig;
}

// GET /orders → newUserListOrdersHandler
export interface OrdersResponse {
  orders: PaymentOrder[];
}

// POST /orders → newPortalCreateTopupHandler
// 服务端生成 out_trade_no、强制 order_kind=topup、裁决金额区间；返回订单 + 该渠道人工支付指引。
export interface CreateTopupRequest {
  amount_cents: number;
  provider: ProviderKind;
}

export interface CreateTopupResponse {
  order: PaymentOrder;
  idempotent: boolean;
  payment_instruction: PortalProviderConfig;
}

// POST /orders/{id}/cancel → newUserCancelHandler（自助撤销自己的 pending 订单）
export interface CancelOrderResponse {
  order: PaymentOrder;
}

const BASE_PATH = '/v1/users/me/payments';

// 当前用户支付来源余额（payment_credits 派生 SUM）。
export function getBalance(): Promise<BalanceResponse> {
  return userGet<BalanceResponse>(`${BASE_PATH}/balance`);
}

// 门户可充配置（金额范围 + 预设金额 + 启用渠道 + 各渠道指引）。无身份依赖但仍在 session 组下。
export function getConfig(): Promise<ConfigResponse> {
  return userGet<ConfigResponse>(`${BASE_PATH}/config`);
}

// 当前用户订单（充值 + 购订阅，按创建时间倒序；limit 默认 50，上限 200）。
export function listOrders(limit?: number): Promise<OrdersResponse> {
  return userGet<OrdersResponse>(`${BASE_PATH}/orders`, limit ? { limit } : undefined);
}

// 自助创建充值订单：amount_cents 必须落在门户配置区间，provider 必须是启用渠道，否则后端 400。
// 返回订单视图 + 该渠道的人工支付指引（用户照指引线下/扫码付款，待 admin 确认入账）。
export function createTopup(req: CreateTopupRequest): Promise<CreateTopupResponse> {
  return userPost<CreateTopupResponse>(`${BASE_PATH}/orders`, req);
}

// 自助撤销自己的 pending 订单（扫码/淘宝下单前可撤）；非 pending 后端会拒。
export function cancelOrder(id: number): Promise<CancelOrderResponse> {
  return userPost<CancelOrderResponse>(`${BASE_PATH}/orders/${id}/cancel`);
}

// ---- 读侧派生辅助（纯前端计算/展示，不新增端点） ----

// 金额一律为最小货币单位整数（分）。展示为「$12.34」。
export function fmtCents(cents: number, currency = 'USD'): string {
  const amount = (cents / 100).toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
  const code = (currency || 'USD').toUpperCase();
  return code === 'USD' ? `$${amount}` : `${code} ${amount}`;
}

const STATUS_LABELS: Record<string, string> = {
  pending: '待支付',
  paid: '已支付',
  recharging: '入账中',
  completed: '已完成',
  expired: '已过期',
  cancelled: '已取消',
  failed: '已失败',
};

export function statusLabel(status: OrderStatus): string {
  return STATUS_LABELS[status] ?? status;
}

const PROVIDER_LABELS: Record<string, string> = {
  manual: '手动转账 / 扫码',
  taobao: '淘宝 / 闲鱼下单',
};

export function providerLabel(provider: ProviderKind): string {
  return PROVIDER_LABELS[provider] ?? provider;
}

// 订单是否可被用户自助撤销（仅 pending）。
export function isCancellable(status: OrderStatus): boolean {
  return status === 'pending';
}
