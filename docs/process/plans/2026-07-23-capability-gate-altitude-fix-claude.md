# 能力门粒度收敛:停止对 chat 特性做账号级门控(对齐 sub2/new-api)

## 背景与诊断(四角印证:2×codex clean-room + 自读真码 + 实测 + live 库)

排查"图片账号 no_capacity"时,发现更根本的选号能力门粒度问题。诊断结论:

- **钱:无逻辑问题,和两镜逐字对齐**。用户按请求模型价×池倍率扣余额、账号侧只记归属
  (provider_account_id),幂等 claim;sub2 的 `ActualCost`(用户)vs `TotalCost×AccountRateMultiplier`
  (账号)同构,new-api 的 log user/channel 维度同构。E2E 扣费实测分文不差。
- **账号/DB 建模:对齐 sub2,非烂根**。`model_allow_list`(账号级模型清单)= sub2 `model_mapping`;
  账号级**媒体**能力门(image/embedding)= sub2 media eligibility。
- **唯一真问题:chat 特性(stream/tools/vision/json/audio)做了账号级能力门**。
  `default_router.requiredCapabilities(req.Features)` 把这些请求特性映射成 RequiredCapabilities,
  选号 SQL `pa.capability_flags @> required` 过滤账号。**两镜都不做这个**(codex 双证:sub2/new-api
  的 stream/tools/vision 不参与账号×模型能力过滤,由 handler/上游处理;仅媒体走账号级门)。

后果(实测复现):账号默认 `capability_flags={}`,`{} @> [stream]` = FALSE → **裸建号口建的账号无法
服务流式请求(no_capacity)**;流式是默认模式。生产未爆是因 codex intake 默认填
`[stream,tools,json,vision,image_output]`、live 账号也手工带了标记——即"能力标记成了每个建号路径要手工
同步的脆弱真相,裸建口漏填即坏"。同一错层还导致跨模型误授权(账号并集能力→在不支持某特性的模型上放行)。

## 目标

账号级能力门**只保留媒体 lane**(image_output/images/embeddings/audio_speech/audio_transcription/
video/rerank——这些两镜也做账号级门);**chat 特性(stream/tools/vision/json/audio-input)不再做账号级
门控**,放行交上游,消除流式 footgun + 跨模型误授权。不动钱路、不动 schema。

## 改动(A 核心 + B 支撑,均对齐两镜)

**A. 停止 chat 特性账号级门控(核心)**
- `backend/internal/router/default_router.go` 的 `requiredCapabilities(f RequestFeatures)`:不再输出
  stream/tools/vision/json/audio 作为**账号池选号**的 RequiredCapabilities(返回空或仅保留确需账号级
  区分的项)。媒体 lane 各自的账号门(`imageshttp/route.go` requireImageOutputCapability、rerank/video/
  embedding 的 route planner)**不变**——它们独立于 requiredCapabilities,继续账号级门控媒体。
- 爆炸半径:唯一消费者是选号 SQL 的 `capability_flags @> required`;去掉 chat 项 → 已带标记账号无害
  (门不再要求)、空标记账号可服务流式(修 bug)、媒体不受影响(独立门)。既有 vision/tools 账号标记
  保留不动,仅不再作为**拒绝**条件。

**B. 账号发现能力回填收窄到媒体(已撤,defer)**
- 曾尝试:Sync 回填媒体能力(image_output/embeddings/…)到账号 capability_flags,并集不覆盖。
- **codex 二审否决、已撤销**:embeddings/rerank 的 handler 把"空模型能力"当支持,账号级媒体并集会把
  跨模型误授权从 chat 挪到媒体 lane(账号有 A=embeddings→写账号标记→模型 B 空能力也被放行);且白名单
  漏 Gemini countTokens 门。媒体账号能力零手配要安全,必须先有 **per-(账号×模型) 能力事实**(见下"后续")。
- **本 PR 只落 A**。媒体账号仍如现状手配 capability_flags(admin/codex intake 已默认填),无回归。

## 后续(独立切片,Owner-gated)
- 媒体账号能力零手配 + 彻底根治跨模型误授权 = 引入 **账号×模型能力表**(或按请求模型严格校验模型能力),
  选号按 `(账号, 模型, 该模型能力)` 判。中型重构,单独计划+双验证,不并入本 PR。

## 爆炸半径小结
- 选号:A 放宽 chat 选号(更少拒绝),媒体门不变;无新增漏配面。
- 钱:不碰成本/归属/claim;路由更宽不影响"按请求模型价扣用户"。
- schema:不动。多写者字段改为并集合并(B),不再互相抹。
- 回归:chat happy path、流式、工具、vision、媒体(图片/embedding/rerank/video)全需回归。

## 测试
1. 判别性单测:requiredCapabilities 不再含 chat 特性(变异加回 stream→选号被误滤,红);媒体聚合白名单
   排除 chat 特性(变异放进 tools→红)。
2. 真 PG:空标记账号服务流式 200(修 bug);账号有 image 模型→发现回填得 image_output(不含 tools);
   双模型账号(A 有 tools、B 无)请求"B+tools"不被账号级误授权(chat 门已去)。
3. E2E(运行实例):裸建号(空标记)流式直接 200;图片账号零手配出图;既有 chat/媒体全模型回归。
4. 四道静态门 + 触及包全测 + 全仓单测。

## 待批/风险
- 属选号 + money-adjacent + 默认行为变更,Owner-gated;已获 Owner "去吧开始处理" 授权本切片。
- clean-room:仅 paraphrase 两镜行为(账号级只门媒体、chat 上游处理),无 vendoring。
- 单 PR 接 part-2 系列;合前 codex 交叉审 + 三门绿 + 变异刀。
