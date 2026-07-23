# 媒体能力门收敛:modality 统一由模型注册表派生,退休账号级 capability_flags 选号门(对齐 new-api/sub2/GCLI2API)

## 一句话

fix A(#298)只把 **chat 特性**(stream/tools/vision/json/audio-input)从账号级选号门摘掉了;媒体这半
(图片/embeddings/rerank/countTokens/audio/video 含 grok 视频)**仍在用同一套账号级 `capability_flags @>`
选号门**——同款病根原封不动。本切片把媒体也收敛到"modality 由模型注册表能力判定(handler 层,已就位)+
账号只按 `model_allow_list` 判能不能调该模型 + 失败换号兜底",与三镜一致,一次性根治。

## 一、真码诊断:媒体每条 lane 都是"模型层门(对)+ 账号层门(冗余有害)"双门

选号 SQL 两道并列门(`backend/internal/db/billing/pool_accounts.sql.go:345-349`):

```
AND (cardinality(pa.model_allow_list) = 0 OR pa.model_allow_list @> ARRAY[$1::text])  -- $1=请求模型
AND pa.capability_flags @> $5::text[]                                                  -- $5=required_capabilities
```

- `model_allow_list @> [请求模型]`:账号非空清单必须含**这个具体模型**——已精确表达"账号能不能调它"。
- `capability_flags @> [modality]`:账号能力标记必须超集——这是**多余的第二道**,且要手工填。

每条媒体 lane 的结构完全一样,**modality 校验已经在 handler 层用请求模型的注册表能力做过了**,后面又
**额外**加一道账号级门:

| lane | 模型层门(保留,`resolved.Capabilities`=注册表能力) | 账号层门(移除,追加进选号 `RequiredCapabilities`) |
|---|---|---|
| 图片 | `imageshttp/handler.go:214` `hasImageOutputCapability` | `imageshttp/handler.go:230` `requireImageOutputCapability` |
| embeddings | `embeddingshttp/handler.go:174` `hasEmbeddingsModelCapability` | `embeddingshttp/handler.go:191` `requireEmbeddingsCapability` |
| rerank | `rerankhttp/handler.go:179` `hasRerankModelCapability` | `rerankhttp/handler.go:196` `requireRerankCapability` |
| countTokens | `geminihttp/generate_content.go:189` `hasCountTokensModelCapability` | `geminihttp/generate_content.go:334` `requireCountTokensCapability` |
| audio | `audiohttp/route.go:40` `hasAudioEndpointCapability` | `audiohttp` `requireAudioEndpointCapability` |
| video(同步选号) | `videohttp/handler.go:146` `hasVideoCapability` | `videohttp/handler.go:343` `appendVideoCapability` → `:353 CapabilityFlags` |
| video(grok worker 执行) | — | `mediatask/grok_video_provider.go:178` `CapabilityFlags: []string{videoOutputCapability}` |

**病根(与 chat 同源)**:
1. **手工填 footgun**:账号默认 `capability_flags={}`,`{} @> [image_output]`=FALSE。裸建号口
   (admin / bundle import)不手工填 image_output/video/embeddings → 该账号**无法服务对应媒体请求**
   (no_capacity)。今天媒体能跑,只因 codex intake 默认填了 `[...,image_output]`
   (`accountintake/codex_defaults.go:15`)+ live 账号手工种了——和 chat "生产没爆"一模一样,
   是"每个建号路径要手工同步的脆弱真相"。
2. **跨模型误授权**:capability_flags 是账号下**多模型并集**;账号有一个图片模型就标 image_output,
   账号下别的非图片模型/别的池也被"证明能出图"。这正是 fix B(把媒体能力回填账号)被 codex 二审
   打回的根因——账号级并集必然跨模型误授权。

## 二、三镜 + 生图头部项目印证:modality 从不做账号级选号门

自读真码(clean-room:只取行为合同,不 vendoring):

- **new-api** `common/endpoint_type.go:6 GetEndpointTypesByChannelType(channelType, modelName)`:端点类型
  (=modality)是 (渠道类型 + 模型名) 的**确定性函数**——`IsImageGenerationModel(modelName)`
  (`common/model.go:38`)看模型名、channel type 看协议。**账号/渠道上没有任何 image 能力标记**。
- **sub2api** `service/image_generation_intent.go:40 IsImageGenerationIntent(endpoint, model, body)`:
  图片意图从 (端点+模型+body) 派生;`service/antigravity_gateway_streaming.go:1135 isImageGenerationModel(model)`
  从模型名判定。账号级只有一个**真 billing 门** `openai_gateway_service.go:539
  isCodexImageGenerationBridgeEnabled`——三级 override(账号→组→全局),表达"这个订阅号出图桥开没开",
  **不是**泛化的 `capability_flags @> [image_output]`。
- **GCLI2API**(账号池转生图,和我们最像):`converter/gemini_fix.py:480 prepare_image_generation_request`
  按模型名(`gemini-3.1-flash-image`)派生,`_parse_size_to_image_config` 从请求参数派生——**无账号级
  image 标记**,账号能不能出图=账号有没有这个 image 模型。
- **fal / replicate-python / InvokeAI**(单后端直连生图):`account` 只是 CLI 登录身份,单一后端无选号,
  **压根没有"账号级能力门"概念**——modality 就是 endpoint。

**结论(回答 Owner"他们不出问题怎么做到的 / 一劳永逸还是单点修")**:
- 他们不出问题,是因为 **modality 永远是 (协议+模型) 的确定性函数,存在模型/注册表侧,不存账号**;
  账号级只留**极少数真 billing 门**(codex 出图桥 / grok 出图 entitlement),且用专门字段+三级 override,
  从不用泛化 capability_flags 数组当选号 modality 过滤。所以他们的账号**零手配 modality 标记**(没这字段)。
- 他们是**一劳永逸的结构**。我们 fix A 只修了 chat 半,媒体半留着同款账号级 modality 门=**单点修**。
  本切片把媒体半也收敛到同一结构,才是"一劳永逸"。

## 三、收敛设计:退休全部泛化 modality 账号门,modality 唯一真相=模型注册表能力

**保留(已是两镜的正确形态)**:每条 lane 的 handler 层 `has<X>Capability(resolved.Capabilities)`——
用**请求模型**的注册表能力(`models.capabilities` / `model_registry_capabilities`)判 modality,
不是该 lane 的模型→400。这是 modality 的唯一真相源,对齐 new-api `IsImageGenerationModel`。

**移除(泛化 modality 账号门,7 处)**:不再把 image_output/embeddings/rerank/countTokens/audio_speech/
audio_transcription/video 追加进选号 `RequiredCapabilities`。选号只靠:
① `model_allow_list @> [请求模型]`(账号能调该模型,已存在);② 池绑定(模型绑到哪些池);
③ 失败换号(账号真不行→上游报错→exclude 换下一个,已存在)。

移除后 `capability_flags @> $5` 的 SQL 门**输入清零**(媒体项不再进 `$5`),与 chat 侧 fix A 后一致
(`@> []` 恒 true)。SQL 不改,只是这道门对 modality 彻底失效。

### 精确改动点

1. 删 `imageshttp/handler.go:230 requireImageOutputCapability(&plan)`。
2. 删 `embeddingshttp/handler.go:191 requireEmbeddingsCapability(&plan)`。
3. 删 `rerankhttp/handler.go:196 requireRerankCapability(&plan)`。
4. 删 `geminihttp/generate_content.go:334 requireCountTokensCapability(&plan)`。
5. 删 audio handler 的 `requireAudioEndpointCapability(&plan, endpoint)` 调用。
6. 删 `videohttp/handler.go:343 attempt.RequiredCapabilities = appendVideoCapability(...)`
   (改为 `CapabilityFlags: attempt.RequiredCapabilities` 直传原值,不再注入 video)。
7. 改 `mediatask/grok_video_provider.go:178 CapabilityFlags: []string{videoOutputCapability}` → 传空/去掉
   (worker 已 `PinnedAccountID` pin 到提交时账号,video 门在此纯冗余)。
8. **deadcode 清理**:各 lane 的 `require<X>Capability` / `append<X>Capability` 失去调用者→按 deadcode 门
   必删。`has<X>Capability`(模型层还在用)**保留**。
9. **注释修正**:`default_router.go:318-320` fix A 时写的"媒体账号门保留:媒体资格确实是账号级事实"
   本次推翻——媒体 modality 同样是模型属性;把这段注释改成"chat 与媒体特性均不做账号级 modality 门,
   由模型注册表能力在各自 handler 判定"。

**不动**(控制爆炸半径):
- `capability_flags` 列本身不删(admin 展示 `adminhttp/accounthealthview`、hermesops 工具仍读它做展示/管理,
  非选号门)。本切片让它**作为选号 modality 门彻底退休**,列保留;未来是否清列=独立 schema 切片。
- `codex_defaults.go:15 codexDefaultCapabilities` 保留(标记留着无害,选号不再消费);清理它要改一串测试,
  不在本切片。
- 钱路、schema、迁移一律不碰。

## 四、共用一套:接入别家视频/音频/声音的统一路径(Owner 要求"简单接入、一劳永逸")

现状痛点(真码):同类媒体 lane 各写一套、大量重复 —— `routerResolvedModel` + `bindingMaxParallelRequests`
在 imageshttp/embeddingshttp/rerankhttp/audiohttp/geminihttp **各抄一份**;modality→能力关键词散在 6 处
常量(`imageOutputCapability`/`embeddingsCapability`/`rerankCapability`/`countTokensCapability`/audio 两个/
video 内联);接新媒体供应商还要在 `videohttp/handler.go:212 videoProviderForProtocol` 硬编码 case。
未来加一种新 modality(如"声音/语音克隆")= 新建一个包 + 抄全套骨架。这正是 Owner 要根治的"接入成本"。

分两层收敛,让接入新供应商从"改一堆散落点"变成"填表 + 注册":

**层 1(本 PR,与账号门收敛同批):modality 判定统一成一套。**
- 一张**唯一** modality→注册表能力关键词表(如 `image_output`→{image_output,images};`embeddings`→
  {embeddings,embedContent,batchEmbedContents};`video`→{video,video_output};`audio_speech`/
  `audio_transcription`;`rerank`;`countTokens`),放共享处(倾向 `internal/registry`,它已是模型能力真相源)。
- 一个共享原语 `ModelSupportsModality(caps []string, modality) bool` 替换 6 处散落的 `has<X>Capability`;
  **行为逐字等价**:每个 modality 的关键词集合、以及"空集合严格(image/video/audio)vs 宽松
  (embeddings/rerank/countTokens)"策略都原样保留(空集合策略随表配置,不借机改语义)。
- 收敛后每条媒体 lane 的 handler 只需:`ResolveModel` → `ModelSupportsModality(resolved.Capabilities, 本lane
  modality)` → `Plan` → 选号(**不再加账号级门**)。骨架一致,`routerResolvedModel`/`bindingMaxParallelRequests`
  收敛成一份共享 helper。
- 效果:**新增一种 modality = 表里加一行 + 登记模型能力**,不必再抄 has/require/append/routerResolvedModel。

**层 2(后续独立切片,不塞本 money-adjacent PR):媒体 provider 注册制 —— 直接对齐 new-api 范本。**
借鉴项目怎么做的(自读真码):new-api 用**一套接口 + 一个工厂**支撑 40+ 供应商全 modality:
- 同步类一个 `channel.Adaptor` 接口(`new-api/relay/channel/adapter.go:15`),一个接口覆盖全 modality:
  `ConvertImageRequest`/`ConvertAudioRequest`/`ConvertEmbeddingRequest`/`ConvertRerankRequest`/
  `ConvertOpenAIRequest` + `DoRequest`/`DoResponse`。
- 异步媒体(视频等长任务)一个 `channel.TaskAdaptor` 接口(`adapter.go:34`),**内建计费钩子**
  `EstimateBilling`/`AdjustBillingOnSubmit`/`AdjustBillingOnComplete` + `BuildRequest*`/`FetchTask`/
  `ParseTaskResult` —— 视频按时长/张数计费天然统一在接口里。
- 一个工厂 `GetAdaptor(apiType)`(`new-api/relay/relay_adaptor.go:54`)按渠道类型查表拿 provider;
  一个 `relayHandler` 按 `RelayMode` 分派全 modality(`new-api/controller/relay.go:35`)。
- **接新供应商 = 实现这一个接口 + 工厂加一行**,handler/worker/计费不改。(sub2 相较更偏平台硬编码分支,
  如 `gateway_service.go:1152 if account.Platform == PlatformAnthropic`,统一度不如 new-api。)

我们对应改造:把 `videohttp/handler.go:212 videoProviderForProtocol` 硬编码 `case "grok_chat"` + mediatask
里 Grok/Gemini video provider 手工接线,收敛成"HUAKAI 版 TaskAdaptor 接口 + 按 (协议族,端点族) 注册的工厂",
计费钩子内建接口(对齐 new-api TaskAdaptor 的 EstimateBilling/Adjust*)。属骨架重构,单独计划 + 验证,
与本收敛解耦(本 PR 先把账号门与 modality 判定统一,层 2 让 provider 也"填表即接入")。

**接入一个新媒体供应商的 checklist(层 1 落地后)**:① 注册表登记模型 + capabilities(标 video/audio/…);
② 绑池;③（新 modality 才需)modality 表加一行；④ provider 实现(层 2 后=注册一行)；⑤ 定价基准价。
**账号侧零配置**——账号发现自动填 `model_allow_list`,选号不再要任何手工能力标记。这就是"共用一套"。

## 五、爆炸半径

- **空 `model_allow_list` 账号(=无限制)行为变化**:此前空标记账号做媒体请求被 `capability_flags` 门挡
  (no_capacity);收敛后 `model_allow_list` 门 bypass(空=无限制)→ **被选中**。若上游真支持→修 bug
  (零手配即服务);若不支持→dispatch 失败→exclude 换号(已有 failover),属**性能回归(多一轮尝试),
  非正确性回归**。缓解:媒体模型绑媒体池(池隔离是第一道)+ 账号发现填充 allow_list(实践中媒体账号
  allow_list 非空)+ failover 兜底。此变化与 chat 侧 fix A 完全同构,已实测无害。
- **grok/codex 真 billing entitlement(出图/出视频订阅)**:两镜用专门账号字段+三级 override 表达;我们
  当前是**借** capability_flags 表达(粗粒度)。收敛后,能出视频的 grok 账号靠 `model_allow_list` 含
  grok-video 模型(账号发现时填充)精确表达 + failover 兜底,功能不缩水;失去的只是"手工标记某账号
  不许出视频"——那本就是 footgun。**若 Owner 要精确的 entitlement 关闭开关**(对齐 sub2 codex bridge
  override),是独立增强切片,不在本次。
- **embeddings/rerank/countTokens 模型层 `has<X>` 空集合返回 true(宽松)**:移除账号门后这是唯一 modality
  门。语义是"注册表尚未探到该模型能力时放行,兼容既有人工模型"。对**已回填 capabilities 的模型**,非
  embeddings 模型返回 false→正确拦截;只有 capabilities 完全空的模型被放行(再由上游报错兜底)。**本切片
  默认保留此宽松**(收紧=空集合→false 需先核存量媒体模型 capabilities 已回填,否则回归),列为可选项交
  codex/Owner 定,不与账号门收敛耦合。
- **钱**:不碰成本/归属/claim;路由更宽不改"按请求模型价扣用户"。媒体计费门(imageshttp per-image/
  token-image settle、`imagepricing`)独立于选号能力门,不受影响。
- **schema**:不动。`capability_flags` 列保留,仅退出选号消费。
- **与 fix B 的关系**:方向相反。fix B 是**增加**账号级能力(把模型能力并集回填账号)→ 跨模型误授权,
  被打回;本切片是**移除**账号级 modality 门,把判定收回模型注册表→根除跨模型误授权,不重蹈 fix B。

## 五、测试(判别性 + 真 PG + E2E,§30)

1. **判别性单测**:
   - 各 lane:变异——把 `require<X>Capability(&plan)` 加回 → 断言选号 `RequiredCapabilities` 不含媒体
     modality 的测试转红(证明确实摘掉了账号门)。
   - 模型层门不动:请求非图片模型走 /images 仍 400 `model_not_image_capable`(变异删
     `hasImageOutputCapability` 校验→红),证明 modality 判定仍在模型层守。
2. **真 PG 集成**(`pool_accounts_eligibility_integration_test.go` 同款夹具):
   - 空 `capability_flags` + `model_allow_list=[dall-e-3]` 账号 → 图片请求 dall-e-3 **被选中**(修 footgun);
   - 空 `capability_flags` + 空 `model_allow_list` 账号 → 也被选中(无限制);
   - `model_allow_list=[gpt-4]`(不含图片模型)账号 → 图片请求**不被选中**(model_allow_list 门仍挡);
   - video/embeddings/rerank 同构各一例。
3. **E2E(运行实例,真号,最便宜模型控额度)**:
   - 裸建号(空 capability_flags)绑图片模型 → 直接出图 200 + 精确扣费,零手配;
   - grok 视频账号 → 出视频 200;既有 chat / embeddings / rerank / audio 全模型回归 200;
   - 非媒体模型走媒体端点仍 400。
4. 四道静态门(build/vet/staticcheck/openapi)+ deadcode 门(删 require/append helper 后无残留)+
   触及包全测 + 全仓单测。

## 六、待批 / 风险

- 属**选号 + money-adjacent + 默认行为变更**,Owner-gated;Owner 已连续指示"开始处理""video/audio grok
  视频也要参考进去,最好一劳永逸",授权本切片全量收敛。
- clean-room:仅 paraphrase 两镜/生图项目行为(modality 从模型派生、账号只留真 billing 门),无 vendoring,
  代码注释不出现镜像标识符。
- 单 PR 接 part-2 / fix A 系列;合前 codex 交叉审(重点:跨模型误授权是否真根除、失败换号是否覆盖空
  allow_list 媒体账号、grok pin worker 去门后仍选回 pin 账号)+ 三门绿 + 变异刀。

---

## 八、codex 交叉审结论与方案升级(采纳「三类事实分离」)

codex 对上面 v1 方案给 **S1 整体反对**,判为"跨 lane 批量删门的单点修,非一劳永逸的结构对齐"。
自读真码逐条核实,**codex 的核心缺陷主张全部成立(未看错层)**,采纳并把方案升级为 v2。

### 8.1 已核实的真码缺陷(v1 的错误假设)

1. **「删门→failover 兜底」不成立(基石缺陷)**:普通"不支持该模型/端点"的 400/422 是**终态,不换号**。
   `bindingfallback/executor/classifier.go:93-107 SignalFromUpstream` 只在 body 含机器 token
   (context_length_exceeded / content_policy)时给可重试信号,否则 → SignalRequestMalformed(终态)。
   图片还有 `imageshttp/attempt.go:100` keepalive 已写字节即停止换号、异步付费 family 副作用未确认时禁重试。
   → 空 allow_list 账号被误选后返回 400,**直接把终态错误返给用户**,不会换到能出图的账号。v1 的"性能回归
   非正确性回归"判断**错误**。
2. **空集合宽松不能保留**:embeddings/rerank/countTokens 的 `has<X>` 空集合返回 true。删账号门后这是唯一
   modality 门,空集合模型 + 400 不换号 = 把"注册表缺能力数据"投影成上游 502/客户端错误,违反 fail-closed。
3. **video worker pin 后不能换号**:`mediatask/store_money.go` 创建任务即 reserve 钱/quota 并绑定账号;
   错误账号被选中=已预留却无法提交的任务 + worker 对同一 pin 反复重试 + 人工恢复。不是"多一轮性能成本"。
4. **钱路非「完全独立」**:计费公式不读 capability_flags 属实,但扩大账号候选会改变 settle 平台、reserve/abort
   次数、DLQ 压力、账号健康信号。v1"钱路不受影响"低估。

### 8.2 capability shape inventory(codex S1#1 要求,已完成)

逐一核实 capability_flags 的全部写入点:`accountintake/codex_defaults.go`(无差别默认全填)、
`accountbundle/import.go`(导入)、`adminpoolhttp/handler.go` + `db/admin/*`(admin 手工)。
**没有任何一处从上游探测账号真实媒体订阅**;`accountmodeldiscovery` 只写 `model_allow_list`,完全不碰
capability_flags。结论:

- **6 类媒体 flag(image_output/embeddings/rerank/countTokens/audio_speech/audio_transcription/video)
  全部是"建号时无差别/手工写入的 modality 标签误用",无真 entitlement 语义**。删除其选号消费不丢任何 entitlement。
- 我们真正的"账号能不能调某媒体模型"事实载体 = **`model_allow_list`**(account 发现覆盖填充,`store.go:76`)。
  → 与 codex"三类分离"里的「账号层专门资格事实」对应者,在 HUAKAI 已存在=model_allow_list,**无需新建
  entitlement 字段/schema**。风险仅存于 **空 model_allow_list 账号**(裸建号未发现 / 导入未给清单)。

### 8.3 v2 升级设计:三类事实分离(对齐 codex 框架 + 聚焦 HUAKAI 实际)

| 层级 | 权威事实(HUAKAI 载体) | 失败策略 |
|---|---|---|
| 模型层 | 模型支持哪些 modality（`resolved.Capabilities`=模型注册表能力，handler 层 has<X> 已就位） | 空/未知 **fail-closed** |
| 账号层 | 账号能否调该模型（`model_allow_list`，account 发现填充；**不再用 capability_flags**） | 选号前过滤，媒体 lane **不放行空 allow_list** |
| 运行时 | 凭据/限流/健康/上游临时失败 | 可判别分类，**不得把普通 4xx 当 failover** |

具体动作(相对 v1 的增量):
1. **模型层 fail-closed**:embeddings/rerank/countTokens 的 `has<X>` 空集合 `true→false`。
   **前置(Owner-gated 数据)**:先核实并回填存量 embeddings/rerank 模型的注册表 capabilities
   (`model_sync_writer.go:95 syncVendorCapabilities` 是回填通道),确认无老模型被 fail-closed 误伤后再翻。
2. **账号层前置过滤**:删账号级泛化 modality 门(v1 的 7 处),**同时**让媒体 lane 选号**要求
   `model_allow_list` 非空且含请求模型**(去掉 `cardinality(model_allow_list)=0` 空 bypass,仅媒体 lane;
   对齐 new-api "请求模型必须在账号模型清单内")。空 allow_list 账号在媒体 lane fail-closed 不被选,不靠 failover。
   → 选号 SQL 需加"媒体 lane 强制 allow_list 命中"的参数(money-adjacent 选号变更,Owner-gated)。
3. **不靠 failover 兜底**:媒体错误账号在选号前被前两层挡住,不 dispatch 到错误上游 → 无 400 终态投影、
   无 video 空预留、无 reserve/abort 放大。
4. **video 提交前即保证 entitlement**:提交阶段选号已经过前两层(model_allow_list 含视频模型 + 模型层 video 门),
   worker pin 不再承担 entitlement 判定。
5. **capability_flags 降级**:退出媒体选号消费,保留列供展示;运营页标注"历史/观察用途,改它不再控制媒体选号"
   (codex S2#10),避免运营误改。
6. **共用一套(修正 codex S2#9)**:第四节 new-api 表述改为"同步 `Adaptor` + 异步 `TaskAdaptor` 两类适配边界
   及各自工厂",不再声称"单接口单工厂全 modality 零外围改动";补 CLIProxyAPI 媒体接入核查、给三镜 HEAD/许可证/
   Source Coverage Proof 后再作为已证实范本。共享 helper 分层避免 `registry↔router` 环(`registry/cache.go:12`
   已 registry→router;modality 纯判断放中性叶子包,DTO 转换放独立 adapter,不把账号 entitlement 混入 modality 表)。

### 8.4 Owner-gated 待批子项(v2 新增,§14)

- **B1 存量 capabilities 回填 + 空集合 fail-closed**:数据回填(可能迁移/运维)+ 默认行为翻转。
- **B2 媒体 lane 选号强制 allow_list 命中**:选号逻辑变更 + money-adjacent(改候选集→影响 settle/reserve)。
- 二者是 v2 "不靠 failover" 的支柱,必须先批再实现;未批则媒体收敛只能停在 v1(有回归风险)不予实现。

### 8.5 采纳的守卫/验收清单(codex 守卫,判别性)

1. 空 capabilities 统一 fail-closed(回填后翻严格;判别:embeddings 模型 capabilities 缺失→请求被拒而非 502)。
2. 不把普通上游 4xx 当既有 failover:为每供应商核实"不支持模型/端点"稳定错误码;仅证明副作用为零且确属账号
   资格失败才允许 exclude 换号。
3. video 提交前验证账号 entitlement(model_allow_list 命中),不等 reserve 后 pin worker 才发现。
4. 全 lane 真实失败验收:错误空 allow_list 账号被选中→上游 400/404/422→是否换号→claim 终态→settle 次数→
   健康信号→客户端状态,逐项断言。
5. 媒体副作用测试:图片异步 family、视频提交超时/结果不明,证明不跨账号重复创建付费任务。
6. 钱路验收:图片 per-image/token-image、embeddings/rerank reserve-abort、audio streaming、video durable claim
   各断言"只有一次最终资金效果"。
7. 三镜证据门:补 CLIProxyAPI + HEAD/许可证/Source Coverage Proof。

> v2 结论:删账号级泛化 modality 门只是必要的第一刀;**真正一劳永逸=模型层 fail-closed + 账号层用
> model_allow_list 前置过滤(媒体不放行空清单)+ 运行时不误当 failover**。因 HUAKAI 的账号资格事实已由
> model_allow_list 承载(无真 entitlement 散在 capability_flags),无需新建 schema,但 B1/B2 属 Owner-gated,
> 先批后动。

---

## 九、三类失败严格区分(回应 Owner「限流了/额度没了怎么办」)

Owner 追问验证了 v2 的运行时层。三类失败**处理相反**,v2 必须把它们分清(codex 三类框架的落地):

| 失败类型 | 例子 | 性质 | 处理 | 真码 |
|---|---|---|---|---|
| 静态不匹配 | 账号根本不支持该模型/modality | 静态 | **选号前**用 model_allow_list + 模型层能力挡住,不选(换号无用——换到同类号一样不支持) | v2 前置过滤 |
| 动态没货 | 限流 429 / 额度耗尽 / 5xx / 超时 | 动态 | **换号** + 账号冷却(健康 FSM),下次选号跳过 | classifier.go:19/29 |
| 请求错误 | 400 malformed / 内容策略 | 终态 | 返回用户,不换号 | classifier.go:97-107 |

- **限流**:`ErrorClassRateLimited → SignalUpstreamRateLimit`(ClassQuota 降级换号,`decision.go:62` requiresRetryPermission);
  binding/key/upstream rate limit 各有专门信号(`failure.go:37-44`)。
- **额度耗尽**:`ErrorClassCreditExhausted → SignalUpstreamAuthFailure`(换号 + 冷却,`classifier.go:29`);
  选号 SQL LATERAL 查 `provider_account_quota_facts`,exhausted 状态降级(`pool_accounts.sql.go:293-297`)。
- **冷却跳过**:选号 SQL `health_state IN (throttled/cooldown) AND until>NOW()` 排除(`pool_accounts.sql.go:337-344`)。
- **正交性(关键)**:`model_allow_list`(能调该模型,静态)⊥ 健康/额度状态(现在可用,动态)。限流账号被健康门
  跳过 → 换到**别的能调该模型**的号。**只有一个池里能出图的号全限流,才返回"暂时无可用账号"(503,等冷却
  恢复)——这是对的,货真没了只能等**。
- v2 **不碰运行时层**,反而因静态判断前置,运行时层只处理真动态失败,更清爽。三类各归其位=真正一劳永逸。
