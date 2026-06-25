/*
 * 我的分组与倍率(用户门户 · 用量与配额)前端类型 —— 镜像 megroupshttp 的 JSON DTO。
 * 端点(session 鉴权,身份从会话上下文派生、不读请求体,无 query):
 *   GET /v1/me/groups   列出当前用户等级可达的模型分组 + 计费倍率(仅公开者)
 * 真码:backend/internal/megroupshttp/handler.go:66(NewHandler)、backend/cmd/gateway/routes.go:208。
 * 关键:ratio 仅当运维标记该分组倍率公开(has_public_ratio=true)才返回;未公开时 ratio 字段整个省略,
 * 前端必须显示「未公开」而非默认值(后端注释明确警告勿误导)。
 */

/** 单个可达分组视图(groupView,handler.go:64)。 */
export interface MeGroupItem {
  pool_group_id: number
  name: string
  /** 计费倍率字符串(如 "1.50000000");仅当 has_public_ratio 为真时存在。 */
  ratio?: string
  has_public_ratio: boolean
}

/** 列表响应(listResponse,handler.go:58)。user_group = 调用者当前路由等级。 */
export interface MeGroupListResponse {
  object: string
  user_group: string
  items: MeGroupItem[]
}
