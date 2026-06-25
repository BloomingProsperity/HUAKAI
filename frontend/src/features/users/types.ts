/*
 * 用户管理(运维台)前端类型 —— 镜像 adminuserhttp 的 JSON。
 * 端点:/admin/v1/users(admin token 鉴权)。
 */
export interface AdminUser {
  id: number
  email: string
  role: string
  status: string
  balance: string
  created_at: string
  // 注:列表端点 userBody 不返回 display_name(routes.go),故列表项不含该字段,避免死读。
}

export interface UserListResponse {
  items: AdminUser[]
  limit: number
  offset: number
}

export interface CreateUserRequest {
  email: string
  password: string
  display_name?: string
  role?: string
}
