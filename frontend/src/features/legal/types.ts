/*
 * 法律条款页类型 —— 镜像 sitepublichttp.NewHandler 的 JSON 投影。
 *
 * 端点:GET /v1/site/config(匿名,无鉴权)
 *   - handler:backend/internal/sitepublichttp/handler.go:79(NewHandler)
 *   - 挂载:backend/cmd/gateway/routes_siteconfig.go:19(r.Get("/v1/site/config", ...))
 *
 * 该端点是「公开 key 白名单投影」,只读品牌/站点公开字段。后端并无独立的
 * terms/privacy 文本 key(已 grep platformsettings/types.go 确认),故本页仅取
 * 站点品牌信息(名称/页脚/联系方式/文档链接)用于填充条款正文里的占位,正文主体
 * 走静态占位文案(见 legal.ts)。这里只声明本页真正读取的字段,其余公开字段不死读。
 */
export interface SiteConfig {
  /** 站点名称(site_name),默认 "HUAKAI"。用于条款正文中的主体名占位替换。 */
  site_name: string
  /** 页脚文本(site_footer),可能含运营主体/备案等信息;空则不展示。 */
  site_footer: string
  /** 公开联系方式(site_contact_info),邮箱/IM 等;空则不展示。 */
  site_contact_info: string
  /** 公开文档链接(site_doc_url);空则不展示。 */
  site_doc_url: string
}
