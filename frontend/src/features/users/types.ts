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
  display_name?: string
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
