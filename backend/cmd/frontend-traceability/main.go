// frontend-traceability 从 OpenAPI 生成前后端双向追踪矩阵。
//
// 映射规则只保存稳定的前端信息架构归属，接口方法、路径、operationId、
// 标签和安全声明始终从 OpenAPI 读取，避免人工复制后静默漂移。
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v2"
)

const (
	defaultSpecPath = "docs/openapi/openapi.yaml"
	defaultDocPath  = "docs/前后端双向追踪矩阵.md"
)

type openAPIDocument struct {
	Paths map[string]pathItem    `yaml:"paths"`
	Raw   map[string]interface{} `yaml:",inline"`
}

type pathItem struct {
	Get     *operation             `yaml:"get"`
	Put     *operation             `yaml:"put"`
	Post    *operation             `yaml:"post"`
	Delete  *operation             `yaml:"delete"`
	Options *operation             `yaml:"options"`
	Head    *operation             `yaml:"head"`
	Patch   *operation             `yaml:"patch"`
	Trace   *operation             `yaml:"trace"`
	Raw     map[string]interface{} `yaml:",inline"`
}

type operation struct {
	Method       string
	Path         string
	OperationID  string                 `yaml:"operationId"`
	Tags         []string               `yaml:"tags"`
	Summary      string                 `yaml:"summary"`
	Security     []map[string][]string  `yaml:"security"`
	RequiredRole string                 `yaml:"x-huakai-required-role"`
	Sources      []string               `yaml:"x-huakai-spec-source"`
	Raw          map[string]interface{} `yaml:",inline"`
}

type tagSpec struct {
	Module  string
	Package string
	Page    string
	Scene   string
}

type row struct {
	OperationID string
	Method      string
	Path        string
	Tags        string
	Module      string
	Package     string
	Actor       string
	Page        string
	Menu        string
	Scene       string
	Action      string
	Status      string
	Remark      string
}

type gap struct {
	ID       string
	Priority string
	Page     string
	Module   string
	Need     string
	Current  string
}

var pages = map[string]string{
	"FE-PG-001": "首装与认证",
	"FE-PG-002": "总览",
	"FE-PG-003": "上游与模型",
	"FE-PG-004": "用户与 Key",
	"FE-PG-005": "资金与商品",
	"FE-PG-006": "调用与媒体",
	"FE-PG-007": "日志与恢复",
	"FE-PG-008": "系统与助手",
	"SYS-INT":   "系统集成（无前端页面）",
}

var tagSpecs = map[string]tagSpec{
	"health":                         {"运行健康", "internal/healthhttp", "SYS-INT", "进程与依赖探针"},
	"gateway":                        {"模型网关", "internal/gatewayhttp 及协议处理包", "FE-PG-006", "模型调用测试台"},
	"hermes":                         {"Hermes 运维助手", "internal/hermeshttp", "FE-PG-008", "Hermes 配置、对话与工具"},
	"auth":                           {"用户认证", "internal/gatewayhttp、internal/userauth", "FE-PG-001", "注册、登录与身份绑定"},
	"user-passkeys":                  {"Passkey", "internal/passkeyhttp", "FE-PG-001", "Passkey 管理"},
	"sessions":                       {"用户会话", "internal/controlhttp", "FE-PG-001", "会话刷新与撤销"},
	"invitations":                    {"邀请与推荐", "internal/invitationhttp、internal/referralhttp", "FE-PG-004", "邀请与推荐关系"},
	"user-vouchers":                  {"用户兑换码", "internal/voucherhttp", "FE-PG-005", "兑换与兑换记录"},
	"user-recharges":                 {"用户充值", "internal/paymenthttp", "FE-PG-005", "充值订单"},
	"user-checkin":                   {"签到奖励", "internal/checkinhttp", "FE-PG-005", "签到与奖励"},
	"user-notifications":             {"用户通知", "internal/usernoticehttp、internal/controlhttp", "FE-PG-008", "通知与偏好"},
	"announcements":                  {"公告", "internal/announcementhttp", "FE-PG-002", "公告栏"},
	"payment-webhooks":               {"支付回调", "internal/paymenthttp", "SYS-INT", "支付服务端回调"},
	"user-audit":                     {"用户用量与凭证", "internal/meusagehttp、internal/userauditloghttp", "FE-PG-007", "调用记录、收据与导出"},
	"user-quota":                     {"用户配额", "internal/mequotahttp、internal/megroupshttp", "FE-PG-004", "配额与可选分组"},
	"user-api-key-controls":          {"用户 Key 策略", "internal/userkeycontrolshttp", "FE-PG-004", "Key 配额、分组与 IP 策略"},
	"pricing":                        {"公开费率", "internal/pricingpublichttp", "FE-PG-005", "费率与历史快照"},
	"audit":                          {"日志证明", "internal/auditverifyhttp、internal/auditexporthttp", "FE-PG-007", "证明、验签与导出"},
	"trust":                          {"信任发现", "internal/trusthttp", "FE-PG-007", "签名材料与离线验签"},
	"user-api-keys":                  {"用户 Key", "internal/userkeyhttp", "FE-PG-004", "Key 创建与管理"},
	"user-payments":                  {"用户订单", "internal/paymenthttp、internal/invoicehttp", "FE-PG-005", "订单、支付、退款申请与收据"},
	"user-subscriptions":             {"用户订阅", "internal/subscriptionhttp", "FE-PG-005", "订阅计划、进度与兑换"},
	"media-tasks":                    {"异步媒体任务", "internal/mediataskhttp", "FE-PG-006", "异步媒体任务"},
	"video":                          {"视频协议", "internal/videohttp、internal/videoclient", "FE-PG-006", "视频生成、编辑与任务"},
	"midjourney":                     {"Midjourney 协议", "internal/mjclient", "FE-PG-006", "图像任务"},
	"suno":                           {"Suno 协议", "internal/sunoclient", "FE-PG-006", "音频任务"},
	"public":                         {"公共站点", "internal/setuphttp、internal/sitepublichttp", "FE-PG-001", "首装与公共配置"},
	"payments":                       {"支付与导出", "internal/paymenthttp", "FE-PG-005", "支付回调与资金导出"},
	"admin":                          {"运营分析", "internal/usageanalyticshttp", "FE-PG-002", "运营指标与排行"},
	"observability":                  {"系统观测", "internal/systemhealthhttp、internal/modulehttp", "FE-PG-008", "节点、模块与系统健康"},
	"admin-risk":                     {"风险总览", "internal/riskoverviewhttp", "FE-PG-002", "风险总览"},
	"admin-account-modes":            {"账号模式目录", "internal/adminhttp", "FE-PG-003", "账号导入模式"},
	"admin-accounts":                 {"上游账号", "internal/adminhttp", "FE-PG-003", "账号列表、详情、测试与恢复"},
	"admin-channel-health":           {"渠道健康", "internal/channelhealthhttp", "FE-PG-003", "健康状态与人工恢复"},
	"admin-credential-acquisition":   {"凭据导入", "internal/gatewayhttp/credentialacqhttp、internal/gatewayhttp/accountintakehttp", "FE-PG-003", "账号导入、迁移与续期"},
	"admin-channel-catalog":          {"渠道目录", "internal/adminhttp", "FE-PG-003", "渠道与测试模板"},
	"admin-provider-catalog":         {"厂商目录", "internal/adminhttp", "FE-PG-003", "厂商目录"},
	"admin-pools":                    {"账号池", "internal/adminpoolhttp", "FE-PG-003", "池管理"},
	"admin-routes":                   {"路由规则", "internal/controlhttp", "FE-PG-003", "路由规则"},
	"admin-proxies":                  {"代理池", "internal/proxyadminhttp", "FE-PG-003", "代理、默认代理与探测"},
	"admin-tls-fingerprint-profiles": {"TLS 指纹档案", "internal/tlsfphttp", "FE-PG-003", "指纹档案"},
	"admin-models":                   {"模型目录", "internal/modeladminhttp", "FE-PG-003", "模型与能力"},
	"admin-model-sync":               {"模型同步", "internal/adminhttp", "FE-PG-003", "模型同步"},
	"admin-model-discovery":          {"模型发现", "internal/modeldiscoveryhttp", "FE-PG-003", "模型发现与上架"},
	"admin-model-bindings":           {"模型池绑定", "internal/modelbindingadminhttp", "FE-PG-003", "模型与池绑定"},
	"admin-model-routing-overrides":  {"模型路由覆盖", "internal/modelroutingadminhttp", "FE-PG-003", "模型路由覆盖"},
	"admin-tenants":                  {"租户管理", "internal/tenantadminhttp", "FE-PG-004", "租户生命周期"},
	"admin-users":                    {"用户管理", "internal/adminuserhttp", "FE-PG-004", "用户、状态与安全"},
	"admin-api-keys":                 {"管理端用户 Key", "internal/adminhttp", "FE-PG-004", "用户 Key 代管"},
	"admin-tokens":                   {"管理员令牌", "internal/adminhttp", "FE-PG-004", "管理员令牌"},
	"admin-quota-policy":             {"配额策略", "internal/adminquotahttp", "FE-PG-004", "配额策略"},
	"admin-vouchers":                 {"兑换码运营", "internal/voucherhttp", "FE-PG-005", "兑换码批次"},
	"admin-payments":                 {"支付运营", "internal/paymenthttp", "FE-PG-005", "订单、退款、渠道与价格覆盖"},
	"admin-subscriptions":            {"订阅运营", "internal/subscriptionhttp", "FE-PG-005", "订阅计划与分配"},
	"admin-billing":                  {"结算管理", "internal/billingadminhttp、internal/billingreconhttp", "FE-PG-005", "结算、余额与重定价"},
	"admin-pricing":                  {"租户费率", "internal/pricingcataloghttp", "FE-PG-005", "池组倍率与验证"},
	"admin-usage":                    {"用量查询", "internal/adminobservabilityhttp", "FE-PG-007", "租户用量与导出"},
	"admin-audit":                    {"管理日志与争议", "internal/controlhttp、internal/runtimeloghttp", "FE-PG-007", "日志、争议与处置"},
	"admin-alerting":                 {"告警", "internal/alertinghttp", "FE-PG-007", "规则、事件与静默"},
	"admin-dlq":                      {"失败队列", "internal/dlqhttp、internal/obsdlqhttp", "FE-PG-007", "DLQ 查询与重放"},
	"admin-media-tasks":              {"媒体恢复", "internal/orphanreconcilehttp", "FE-PG-007", "媒体孤儿与人工恢复"},
	"admin-cache":                    {"响应缓存", "internal/cacheadminhttp", "FE-PG-007", "缓存状态与清除"},
	"admin-platform-settings":        {"平台设置", "internal/controlhttp", "FE-PG-008", "平台设置"},
	"admin-email":                    {"邮件设置", "internal/emailsettingshttp", "FE-PG-008", "邮件配置、测试与模板"},
	"admin-notifications":            {"通知运营", "internal/usernoticehttp、internal/controlhttp", "FE-PG-008", "通知广播与 Worker"},
	"admin-announcements":            {"公告运营", "internal/announcementhttp", "FE-PG-008", "公告管理"},
	"admin-moderation":               {"内容安全", "internal/moderationhttp", "FE-PG-008", "规则、违规、封禁与解封"},
	"admin-ops":                      {"运行配置", "internal/runtimeloghttp", "FE-PG-008", "运行日志级别"},
	"admin-version":                  {"版本信息", "internal/adminhttp", "FE-PG-008", "运行版本"},
}

var gaps = []gap{
	{"FE-GAP-001", "P0", "FE-PG-006", "音频协议", "部分交付时的计费、补偿和人工恢复合同", "当前缺少完整闭环，不得只凭成功响应标记已结算"},
	{"FE-GAP-002", "P1", "FE-PG-002", "运营聚合", "管理员与租户作用域的 Token、缓存、成本趋势、账号批量指标、池健康和链路图投影", "现有原子接口不足以支撑一屏聚合，禁止浏览器跨权限拼装"},
	{"FE-GAP-003", "P1", "FE-PG-003", "账号批量运维", "按选中账号执行批量动作与即时凭据刷新", "现有按标签批量能力不能替代逐项选择和部分成功合同"},
	{"FE-GAP-004", "P1", "FE-PG-008", "内容安全", "审核规则覆盖适用的全部兄弟协议入口", "现有入口覆盖仍需按协议矩阵补齐"},
	{"FE-GAP-005", "P1", "FE-PG-008", "Worker 运维", "模型同步和订阅 Worker 的节点、领导者、最后 tick 与集群口径", "现有状态不足以判断多副本是否真实工作"},
	{"FE-GAP-006", "P1", "FE-PG-003", "代理运维", "持久质量检测、多目标质量、批量管理和备用代理", "不得以单次连通或静默直连冒充完整代理恢复"},
	{"FE-GAP-007", "P1", "FE-PG-002/FE-PG-007", "租户运营", "租户管理员的本租户经营总览和运行日志投影", "必须服务端按租户聚合并脱敏"},
	{"FE-GAP-008", "P2", "FE-PG-003", "账号与池增强", "定时账号测试、加密代理迁移包、账号复制安全子集、关系级池内排序", "没有正式 operation，前端不得用本地状态伪造"},
	{"FE-GAP-009", "路线", "FE-PG-006", "协议生态", "公共 MCP 与 Realtime", "后端路线未闭环"},
	{"FE-GAP-010", "封存", "FE-PG-003", "封存厂商", "Cursor、Copilot、Windsurf 正式上线链路", "按 Owner 指令留到项目核心链路上线后处理"},
}

func main() {
	var (
		specPath = flag.String("spec", defaultSpecPath, "OpenAPI 文件路径")
		docPath  = flag.String("doc", defaultDocPath, "生成文档路径")
		write    = flag.Bool("write", false, "写入生成文档")
		check    = flag.Bool("check", false, "检查生成结果是否与文档一致")
	)
	flag.Parse()
	if *write == *check {
		exitf("必须且只能指定 -write 或 -check")
	}

	root, err := findRepoRoot()
	if err != nil {
		exitf("%v", err)
	}
	spec := filepath.Join(root, filepath.FromSlash(*specPath))
	doc := filepath.Join(root, filepath.FromSlash(*docPath))
	rendered, err := renderFile(spec)
	if err != nil {
		exitf("生成矩阵失败：%v", err)
	}
	if *write {
		if err := os.WriteFile(doc, rendered, 0o644); err != nil {
			exitf("写入 %s：%v", doc, err)
		}
		fmt.Printf("已生成 %s\n", doc)
		return
	}
	current, err := os.ReadFile(doc)
	if err != nil {
		exitf("读取 %s：%v", doc, err)
	}
	if !bytes.Equal(current, rendered) {
		exitf("%s 已过期；运行 go run ./cmd/frontend-traceability -write 更新", doc)
	}
	fmt.Printf("%s 与 OpenAPI 一致\n", doc)
}

func renderFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc openAPIDocument
	if err := yaml.UnmarshalStrict(raw, &doc); err != nil {
		return nil, fmt.Errorf("解析 OpenAPI：%w", err)
	}
	ops := flatten(doc)
	rows, err := buildRows(ops)
	if err != nil {
		return nil, err
	}
	return render(rows), nil
}

func flatten(doc openAPIDocument) []operation {
	var ops []operation
	for path, item := range doc.Paths {
		byMethod := map[string]*operation{
			"get": item.Get, "put": item.Put, "post": item.Post, "delete": item.Delete,
			"options": item.Options, "head": item.Head, "patch": item.Patch, "trace": item.Trace,
		}
		for method, candidate := range byMethod {
			if candidate == nil {
				continue
			}
			op := *candidate
			op.Method = strings.ToUpper(method)
			op.Path = path
			ops = append(ops, op)
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops
}

func buildRows(ops []operation) ([]row, error) {
	if len(ops) == 0 {
		return nil, errors.New("OpenAPI 未解析出 operation")
	}
	seenID := make(map[string]struct{}, len(ops))
	seenRoute := make(map[string]struct{}, len(ops))
	rows := make([]row, 0, len(ops))
	for _, op := range ops {
		if strings.TrimSpace(op.OperationID) == "" {
			return nil, fmt.Errorf("%s %s 缺少 operationId", op.Method, op.Path)
		}
		if _, exists := seenID[op.OperationID]; exists {
			return nil, fmt.Errorf("operationId 重复：%s", op.OperationID)
		}
		seenID[op.OperationID] = struct{}{}
		routeKey := op.Method + " " + op.Path
		if _, exists := seenRoute[routeKey]; exists {
			return nil, fmt.Errorf("method+path 重复：%s", routeKey)
		}
		seenRoute[routeKey] = struct{}{}
		if isAdminOperation(op) && op.RequiredRole == "" && !isExplicitlyPublic(op) {
			return nil, fmt.Errorf("%s 缺少 x-huakai-required-role，前端不得猜测管理权限", op.OperationID)
		}
		primary, spec, err := primarySpec(op)
		if err != nil {
			return nil, err
		}
		spec = effectiveSpec(op, spec)
		page, scene, integration := classify(op, spec)
		menu, ok := pages[page]
		if !ok {
			return nil, fmt.Errorf("%s 使用未知页面编号 %s", op.OperationID, page)
		}
		pkg := sourcePackage(op.Sources)
		if pkg == "" {
			pkg = spec.Package
		}
		actorName, err := actor(op, primary)
		if err != nil {
			return nil, err
		}
		status := "后端已挂载；前端待接线"
		remark := "OpenAPI 与生产路由一致性门覆盖"
		if integration {
			status = "后端已挂载；无需页面直调"
			remark = "服务端集成、回调或部署探针"
		}
		rows = append(rows, row{
			OperationID: op.OperationID,
			Method:      op.Method,
			Path:        op.Path,
			Tags:        strings.Join(op.Tags, "、"),
			Module:      spec.Module,
			Package:     pkg,
			Actor:       actorName,
			Page:        page,
			Menu:        menu,
			Scene:       scene,
			Action:      action(op.Method, scene),
			Status:      status,
			Remark:      remark,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Page != rows[j].Page {
			return rows[i].Page < rows[j].Page
		}
		if rows[i].Scene != rows[j].Scene {
			return rows[i].Scene < rows[j].Scene
		}
		if rows[i].Path != rows[j].Path {
			return rows[i].Path < rows[j].Path
		}
		return rows[i].Method < rows[j].Method
	})
	return rows, nil
}

func isAdminOperation(op operation) bool {
	for _, tag := range op.Tags {
		if tag == "admin" || tag == "observability" || strings.HasPrefix(tag, "admin-") {
			return true
		}
	}
	return false
}

func isExplicitlyPublic(op operation) bool {
	return op.Security != nil && len(op.Security) == 0
}

func effectiveSpec(op operation, base tagSpec) tagSpec {
	path := op.Path
	switch {
	case strings.Contains(path, "/system/health") || strings.Contains(path, "/system/nodes"):
		return tagSpec{"系统与节点监测", "internal/systemhealthhttp、internal/servermonitorhttp", "FE-PG-008", "节点与系统健康"}
	case strings.Contains(path, "/admin/v1/modules") || strings.Contains(path, "/v1/admin/modules"):
		return tagSpec{"模块注册表", "internal/modulehttp", "FE-PG-008", "模块状态"}
	case strings.Contains(path, "/backup/"):
		return tagSpec{"备份状态", "internal/backuphttp", "FE-PG-008", "备份清单"}
	case strings.Contains(path, "/runtime-logs"):
		return tagSpec{"运行日志", "internal/runtimeloghttp", "FE-PG-007", "分类日志与清理"}
	case strings.Contains(path, "/admin/referrals"):
		return tagSpec{"推荐运营", "internal/referralhttp", "FE-PG-004", "推荐关系与奖励"}
	case path == "/v1/public/rankings":
		return tagSpec{"公共排行", "internal/publicrankinghttp", "FE-PG-002", "公共排行"}
	case strings.Contains(path, "/orders/export") || strings.Contains(path, "/refunds/export"):
		return tagSpec{"资金数据导出", "internal/paymenthttp", "FE-PG-005", "订单与退款导出"}
	default:
		return base
	}
}

func primarySpec(op operation) (string, tagSpec, error) {
	if len(op.Tags) == 0 {
		return "", tagSpec{}, fmt.Errorf("%s 缺少 tag", op.OperationID)
	}
	primary := op.Tags[0]
	for _, tag := range op.Tags {
		if tag != "admin" && tag != "observability" && tag != "payments" {
			primary = tag
			break
		}
	}
	spec, ok := tagSpecs[primary]
	if !ok {
		return "", tagSpec{}, fmt.Errorf("%s 使用未归类 tag %q", op.OperationID, primary)
	}
	return primary, spec, nil
}

func classify(op operation, base tagSpec) (page, scene string, integration bool) {
	path := op.Path
	switch {
	case path == "/healthz" || path == "/readyz":
		return "SYS-INT", "部署健康探针", true
	case strings.Contains(path, "/webhooks/") || strings.HasSuffix(path, "/identity-changed"):
		return "SYS-INT", "服务端回调", true
	case path == "/.well-known/huakai-pubkey.json":
		return "SYS-INT", "公开信任发现", true
	case strings.Contains(path, "/admin/system/") || strings.Contains(path, "/admin/v1/system/") ||
		strings.Contains(path, "/v1/admin/modules") ||
		strings.Contains(path, "/admin/v1/modules") || strings.Contains(path, "/backup/"):
		return "FE-PG-008", "节点、模块与备份", false
	case strings.Contains(path, "/runtime-logs"):
		return "FE-PG-007", "分类日志与清理", false
	case strings.Contains(path, "/loglevel") || strings.Contains(path, "/platform-settings") ||
		strings.Contains(path, "/admin/version"):
		return "FE-PG-008", "运行配置与版本", false
	case strings.Contains(path, "/usage/overview") || strings.Contains(path, "/usage/time-series") ||
		strings.Contains(path, "/usage/performance") || strings.Contains(path, "/usage/leaderboard") ||
		strings.Contains(path, "/risk/overview"):
		return "FE-PG-002", "运营服务区", false
	case path == "/v1/public/rankings":
		return "FE-PG-002", "公共排行", false
	case strings.Contains(path, "/admin/referrals"):
		return "FE-PG-004", "推荐关系与奖励", false
	case strings.Contains(path, "/orders/export") || strings.Contains(path, "/refunds/export"):
		return "FE-PG-005", "资金数据导出", false
	case strings.Contains(path, "/receipts/") || strings.Contains(path, "/disputes"):
		return "FE-PG-005", "收据与争议", false
	case strings.Contains(path, "/audit") || strings.Contains(path, "/dlq") ||
		strings.Contains(path, "/usage-records"):
		return "FE-PG-007", "日志、证明与恢复", false
	default:
		return base.Page, base.Scene, base.Page == "SYS-INT"
	}
}

func actor(op operation, primaryTag string) (string, error) {
	switch op.RequiredRole {
	case "platform_admin":
		return "部署者（platform_admin）", nil
	case "tenant_operator":
		return "租户管理员（tenant_operator）", nil
	case "platform_admin_or_tenant_operator":
		return "部署者或租户管理员", nil
	case "platform_admin_or_granted_tenant_operator":
		return "部署者或已获能力授权的租户管理员", nil
	case "tenant_scoped_admin":
		return "部署者或租户管理员（限工作租户）", nil
	case "session":
		return "最终用户会话", nil
	case "":
		// 继续按 security 与稳定功能域归类。
	default:
		return "", fmt.Errorf("%s 使用未知 x-huakai-required-role：%s", op.OperationID, op.RequiredRole)
	}

	if op.Security != nil {
		if len(op.Security) == 0 {
			return "匿名/公开调用方", nil
		}
		for _, scheme := range op.Security {
			if _, ok := scheme["sessionBearerAuth"]; ok {
				return "最终用户会话", nil
			}
			if _, ok := scheme["adminBearerAuth"]; ok {
				return "部署者或租户管理员（按处理器作用域）", nil
			}
			if _, ok := scheme["bearerAuth"]; ok {
				return "有效用户 Key", nil
			}
		}
	}
	if strings.HasPrefix(primaryTag, "admin-") || primaryTag == "admin" || primaryTag == "observability" {
		return "部署者或租户管理员（按处理器作用域）", nil
	}
	if strings.HasPrefix(primaryTag, "user-") || primaryTag == "invitations" || primaryTag == "sessions" {
		return "最终用户会话", nil
	}
	if primaryTag == "hermes" {
		return "部署者或租户管理员", nil
	}
	if primaryTag == "gateway" {
		return "有效用户 Key", nil
	}
	return "按 OpenAPI 全局 bearerAuth", nil
}

func action(method, scene string) string {
	verb := map[string]string{
		"GET": "查询", "HEAD": "探测", "POST": "创建或执行", "PUT": "整体更新",
		"PATCH": "局部更新", "DELETE": "删除或撤销", "OPTIONS": "协商", "TRACE": "追踪",
	}[method]
	return verb + "：" + scene
}

func sourcePackage(sources []string) string {
	for _, source := range sources {
		const prefix = "backend/internal/"
		if !strings.HasPrefix(source, prefix) {
			continue
		}
		rest := strings.TrimPrefix(source, "backend/")
		parts := strings.Split(rest, "/")
		if len(parts) >= 2 {
			return strings.Join(parts[:2], "/")
		}
	}
	return ""
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, defaultSpecPath)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("未找到 HUAKAI 仓库根目录")
		}
		dir = parent
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
