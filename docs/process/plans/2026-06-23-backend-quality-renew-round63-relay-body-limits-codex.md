# 2026-06-23 backend-quality-renew round63 relay body limits

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `completionshttp`、`embeddingshttp`、`imageshttp`、`rerankhttp`、`geminihttp`、`audiohttp` 等 sibling relay handler 的请求体读取、body limit、`MaxBytesReader`/`io.ReadAll` 重复样板、multipart 双缓冲与 env 配置漂移。 |
| Out of scope | 不展开跨租户/密钥安全专项；不修改生产代码；不触碰另一个目标；不调整 body limit 策略本身。 |
| Success criteria | 输出中文 findings，逐条带绝对路径行号、具体函数/常量、问题触发条件和可执行修法。 |
| Time estimate | 约 25-40 分钟；一次 Codex renew 审查切面。 |
| Blast radius | 默认只读源码和写本计划文件；若误改 handler，可能影响多个 relay 入口，因此本轮不做生产代码修改。 |
| Failure modes | 只看搜索结果不读上下文；把“测试存在”误判成已运行；把 multipart 内存放大误写成安全结论。缓解：逐文件读关键函数，记录环境缺口。 |
| Decision points | 若确认需要抽公共 body reader/helper，后续由 Owner 决定先抽独立 `internal/httpreq` 还是落到每个 `*http` 包的共享小包。 |
| Pre-execution checklist | 1. 读取 goal objective；2. 检索目标包的 body limit 常量和 `MaxBytesReader`/`io.ReadAll`；3. 读取 audio/images multipart 路径；4. 检索 `HUAKAI_MAX_REQUEST_BODY_MB` 实际接线；5. 尝试运行相关测试并记录环境限制。 |
| Concrete execution order | 先用 `rg` 建证据索引，再并行读取目标 handler 片段，最后输出 findings，不写额外报告。 |
