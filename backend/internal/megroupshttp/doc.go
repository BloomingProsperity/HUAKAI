// Package megroupshttp 暴露已认证用户可见的 pool group 及其公开的计费倍率。
//
// 该端点把一个成熟的账号中心通常拆成两次读取的两件事 ——「我能用哪些 group」
// 和「适用什么倍率」—— 合并为单次只读投影,且严格限定在会话身份范围内。
// tenant 和 user 只取自已校验的会话;任何 query 或 header 参数都无法放宽
// 此次读取(CMB-5)。只有当某个 group 行带有运营者控制的 public 标志时,
// 其倍率才会被披露;非公开的内部成本倍率绝不会被序列化。
package megroupshttp
