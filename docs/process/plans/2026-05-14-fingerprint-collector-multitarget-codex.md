# 2026-05-14 fingerprint-collector multi-target capture support

| Owner directive | "HUAKAI fingerprint-collector capture playbook + 工具加强 — 让 Owner 5 min 抓 3 vendor (codex / Kiro / Gemini)。" |
| Scope | In: collector CLI 参数、metadata mode_name、vendor 隔离输出、后端 collector 输出加载兼容、capture verify script、collector cmd 集成测试、中文 PLAYBOOK、防死 /tmp 汇总。Out: 真实抓包、提交/推送、参考项目源码读取、数据库/认证/计费/配额改动。 |
| Success criteria | 无参/旧参数仍能写 `./output/`；`-target-name` 默认输出到 `./output/<target_name>/`；`-output-dir` 可覆盖输出目录；`-sample-count` 默认 5 并兼容 `-min-samples`；metadata 写入 `mode_name`；后端可按空 target 读旧路径、按 target 读子目录；verify script 能列出缺失项；测试覆盖默认、target metadata、output-dir 隔离。 |
| Time estimate | 约 45-75 分钟 wall clock；单 Codex work unit。 |
| Blast radius | 主要影响本地抓包工具和 R-3 mimicry 模板加载；若参数兼容处理错误，可能影响 Phase A 旧 `output/clienthello-template.json` 路径或操作员抓包流程。 |
| Failure modes | 参数冲突导致旧命令失效；metadata JSON 字段不一致；测试触发真实 pcap 依赖；shell verify 对 JSON 解析过脆；playbook 写成泛泛说明无法让 Owner 5 分钟执行。Mitigation: 抽出参数解析/metadata 构造为可测试函数，测试不打开 pcap；verify 使用 Go 小片段解析 JSON；保留旧 `-out` 和 `-min-samples`。 |
| Decision points | 无高风险 Owner sign-off 点；不触碰 LICENSE、真实 secrets、DB schema、auth/billing/quota core、deployment scripts。 |
| Pre-execution checklist | 1. 读 `docs/RULES.md` 与任务相关实现；2. 检查工作树已有改动，不回滚用户改动；3. 先写 `/tmp/codex-capture-playbook.txt` stub；4. 增量修改文件并追加 `/tmp` 进度；5. 跑 collector tests、mimicry tests、verify script smoke；6. 生成 `/tmp/codex-capture-playbook-final.txt`。 |
| Concrete execution order | 1. 调整 collector CLI 参数解析与 metadata；2. 添加 cmd 测试辅助和测试；3. 调整 mimicry loader 可选 target；4. 添加 verify script；5. 写中文 PLAYBOOK；6. 格式化与测试；7. 写最终 `/tmp` 汇总与中文 Owner summary。 |

