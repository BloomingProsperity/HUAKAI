// Package userkeycontrolshttp 暴露按 session 限定的 API key 控制端点。
//
// 这些路由必须挂在 auth.SessionMiddleware 内部。即便如此,当 session 身份缺失时
// handler 仍会 fail closed(失败即拒),这样意外的公开挂载也无法用零值的
// tenant/user 触达 service。
package userkeycontrolshttp
