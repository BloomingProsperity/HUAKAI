# HCSF 默认翻转取证(rank4)— 证据 + 给 Owner 的决策

状态:✅ 取证切片已完成(纯测试 + 本文档,零生产默认改动)
日期:2026-06-26
作者:Claude(Owner 全权 + 按 scoping 排期推进)

## 这一切片做了什么
HCSF(hot-config-safety-fence,热路径配置安全围栏)的「默认翻转」是 Owner-gated 默认行为变更,
**本切片不翻任何默认**,只产出翻转所需的**安全证据**,让 Owner 能拍「翻哪个、敢不敢翻」:
1. 新增 `backend/internal/headerfirewall/override_invariant_test.go`(表驱动 + nil fail-closed),
   把"运营者 AllowOverride 掀不动内置 deny"的不变量从现有的**仅 Set-Cookie 一例**扩到**全部 16 条
   内置 deny 规则**;并补 `settings=nil → Policy{} → 只剩内置 deny` 的 fail-closed 断言。
2. 本文档:固化内置 deny 清单 + 缓存语义 + 三个翻转候选的对照与推荐。

## 安全前提(已被新测试证实)
`FilterResponseHeaders`(firewall.go:68)对内置 deny 头在 allowOverride **之前**无条件剥离
(:71-73),只有运营者自配的 extraDeny 才能被 allowOverride 掀(:74)。因此**翻转响应头围栏默认
不会让下列 16 类敏感上游头泄露**——它们永远被剥,运营配置掀不动:

内置 deny(13 exact + 3 prefix,firewall.go:25-42):
`Set-Cookie`、`Set-Cookie2`、`Authorization`、`Proxy-Authenticate`、`Proxy-Authorization`、
`WWW-Authenticate`、`X-Real-IP`、`X-Forwarded-For/Host/Proto/Port`、`X-Cloud-Trace-Context`、
`Server`、前缀 `CF-`、`X-Amz-`、`X-Amzn-`。

变异验证:把 :71 改成 `denyBuiltIn(name) && !matchesDynamic(name, allowOverride)`(让 override 能掀),
新测试全部样本转红——证明断言有判别性,不是假绿。

## 缓存语义(影响"翻转多快生效")
`platformsettings.Service.Get`(service.go:53)带 **30s 缓存 TTL**(defaultCacheTTL,:11)+ lastKnown
回退。⇒ 经 DB 设置翻默认有最长 ~30s 传播延迟;经 env/代码缺省翻转则即时。安全围栏类默认若走 DB,
需接受 30s 窗口;若要即时,改代码缺省。

## 三个翻转候选(Owner 拍)+ 推荐
| 候选 | 现状 | 翻转代价 | 风险 |
|---|---|---|---|
| ① 主中继响应头围栏接通真实流量 | 主中继自建干净头,响应围栏对真实上游流量是死的 | **不是改默认,是补能力**——需新增"上游响应头→客户端的受控复制点"再设默认 | 中:需确保 16 类内置 deny 兜底(已证),但要逐一核哪些上游头会新流到客户端 |
| ② warmup_intercept_enabled false→true | 默认 false(types.go:142) | 改 1 处代码缺省 + 测试 | 低:单值开关,影响面小 |
| ③ pool selector default→shadow/canary | — | 改选号默认 | 中高:碰 §6 选号/碰撞包,且影响真实路由 |

**推荐**:① 是真正有价值但**工作量最大**(补受控复制能力,非翻开关),且需一份"翻转后哪些上游头新
流到客户端"的逐头清单才敢上;②是最小、最安全的即时收益(单开关),可作为先行小切片;③碰 §6 冻结
包,当前不碰。**建议 Owner 先拍 ②(低风险即时)还是 ①(高价值但要先补能力 + 逐头核白名单)**;
本切片已把 ① 的安全前提(内置 deny 兜底)证牢,为后续 ① 切片扫清第一道顾虑。

## 验证
- 新测试 `go test ./internal/headerfirewall/` 绿;变异(让 override 掀内置 deny)→ 全样本转红。
- 纯新增测试 + 文档,**零生产代码/默认改动**;baseline 零新增。
