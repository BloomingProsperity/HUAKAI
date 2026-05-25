# HUAKAI fingerprint-collector 三 vendor 捕获手册

目标：Owner 在本机用自己的订阅账号抓 3 套第一方客户端 TLS ClientHello 指纹。

- OpenAI Codex CLI: `openai_codex`
- Amazon Kiro CLI / IDE: `kiro_cli`
- Google Gemini Advanced / Vertex: `gemini_advanced`

边界：只抓自己机器、自己账号、自己主动触发的客户端流量。工具只解析明文
ClientHello 元数据，不解密 TLS payload。`raw-pcap-snippet.pcap` 永远不要提交。

## 1. 装依赖

1. 进入工具目录。

   ```bash
   cd tools/fingerprint-collector
   go version
   go build -o ./bin/fingerprint-collector ./cmd
   ```

2. macOS 安装依赖。

   ```bash
   xcode-select --install
   brew install libpcap
   sudo ./bin/fingerprint-collector -list-ifaces
   ```

   macOS 通常需要 `sudo` 抓包。优先选正在出网的 Wi-Fi / 有线接口，例如
   `en0`，不要盲选 VPN / tunnel 接口。

3. Windows 安装依赖。

   - 安装 Npcap: https://npcap.com/
   - 勾选 `Install Npcap in WinPcap API-compatible Mode`。
   - 用管理员 PowerShell 运行。

   ```powershell
   go build -o .\bin\fingerprint-collector.exe .\cmd
   .\bin\fingerprint-collector.exe -list-ifaces
   ```

   Windows 接口名通常是 `\Device\NPF_{...}`，复制完整字符串。

4. Linux 安装依赖。

   ```bash
   sudo apt-get update
   sudo apt-get install -y libpcap-dev
   ```

   Fedora 用 `sudo dnf install -y libpcap-devel`。Arch 用
   `sudo pacman -S --needed libpcap`。

5. Linux 运行方式二选一。

   ```bash
   sudo ./bin/fingerprint-collector -list-ifaces
   ```

   或：

   ```bash
   sudo setcap cap_net_raw,cap_net_admin=eip ./bin/fingerprint-collector
   ./bin/fingerprint-collector -list-ifaces
   ```

6. 记下接口名，下面用 `<IFACE>` 代替。

7. 清理三套输出目录。

   ```bash
   rm -rf ./output/openai_codex ./output/kiro_cli ./output/gemini_advanced
   ```

8. 旧命令仍兼容，会写旧路径 `./output/`。

   ```bash
   ./bin/fingerprint-collector -iface <IFACE> -host api.anthropic.com -min-samples 5 -out ./output/
   ```

9. 新三 vendor 抓取用 `-target-name`。未传 `-output-dir` 时默认写：

   ```text
   ./output/<target_name>/
   ```

10. 每次抓完都跑：

    ```bash
    ./verify-capture.sh ./output/<target_name>
    ```

## 2. 抓 OpenAI Codex CLI

1. 开两个终端。

   - 终端 A: fingerprint-collector。
   - 终端 B: Codex CLI。

2. 终端 B 确认 CLI 可用。

   ```bash
   codex --version
   ```

3. 终端 B 触发 OAuth 登录。

   ```bash
   codex login
   ```

   用 Owner 的 OpenAI 订阅账号完成浏览器登录。已登录也可以重跑一次，确保 token
   可用。

4. 终端 A 启动捕获。

   ```bash
   ./bin/fingerprint-collector \
     -iface <IFACE> \
     -host api.openai.com \
     -target-name openai_codex \
     -sample-count 5 \
     -duration 180 \
     -confirm-pre-flight
   ```

5. 终端 B 发一次真实请求。

   ```bash
   codex exec "Return one short sentence: HUAKAI fingerprint capture ping."
   ```

6. 样本不足时再发 4 次。

   ```bash
   codex exec "Say ping 1."
   codex exec "Say ping 2."
   codex exec "Say ping 3."
   codex exec "Say ping 4."
   ```

7. 等终端 A 输出 `[done]`。

8. 验证输出。

   ```bash
   ./verify-capture.sh ./output/openai_codex
   grep '"mode_name"' ./output/openai_codex/metadata.json
   ```

9. 成功条件：`mode_name=openai_codex`，`sample_count > 0`，最好等于 5，
   `mitm_check_result=ok`。

10. 删除 raw pcap。

    ```bash
    rm -f ./output/openai_codex/raw-pcap-snippet.pcap
    ```

## 3. 抓 Amazon Kiro

1. 开终端 A 和 Kiro CLI / IDE。

2. 如果有 CLI，先确认版本。

   ```bash
   kiro --version
   ```

   如果只用 IDE，跳过该命令，确认 IDE 已登录 Owner 账号。

3. 找到 Kiro 实际模型请求 host。优先看 Kiro 日志、DNS 查询、IDE 开发者工具或
   系统防火墙日志。下面用 `<KIRO_HOST>` 代替。

4. 终端 A 启动捕获。

   ```bash
   ./bin/fingerprint-collector \
     -iface <IFACE> \
     -host <KIRO_HOST> \
     -target-name kiro_cli \
     -sample-count 5 \
     -duration 180 \
     -confirm-pre-flight
   ```

5. 用 CLI 触发真实模型请求。

   ```bash
   kiro "Return one short sentence: HUAKAI fingerprint capture ping."
   ```

6. 如果用 IDE，在聊天或 agent 面板发送同一句短请求。

7. 样本不足时重复短请求 4 次。不要切换账号、网络或代理。

8. 验证输出。

   ```bash
   ./verify-capture.sh ./output/kiro_cli
   grep '"mode_name"' ./output/kiro_cli/metadata.json
   ```

9. 成功条件：`mode_name=kiro_cli`，`sample_count > 0`，`mitm_check_result=ok`。

10. 删除 raw pcap。

    ```bash
    rm -f ./output/kiro_cli/raw-pcap-snippet.pcap
    ```

## 4. 抓 Google Gemini Advanced

1. 二选一。

   - Chrome 网页版 Gemini Advanced。
   - Vertex / gcloud / 已有最小请求脚本。

2. Chrome 路径先关闭无关 Google 标签页，减少噪音。

3. Chrome 路径初始 host 用：

   ```text
   gemini.google.com
   ```

4. 终端 A 启动 Chrome 路径捕获。

   ```bash
   ./bin/fingerprint-collector \
     -iface <IFACE> \
     -host gemini.google.com \
     -target-name gemini_advanced \
     -sample-count 5 \
     -duration 180 \
     -confirm-pre-flight
   ```

5. Chrome 打开 Gemini Advanced，发送：

   ```text
   Return one short sentence: HUAKAI fingerprint capture ping.
   ```

6. 样本不足时刷新页面或新开隐身窗口，再发 4 次短请求。

7. Vertex 路径先确认认证。

   ```bash
   gcloud auth list
   gcloud config list project
   ```

8. Vertex 路径常见 host：

   ```text
   aiplatform.googleapis.com
   ```

   如果你的脚本使用 regional endpoint，以实际 endpoint 为准。

9. Vertex 路径启动捕获。

   ```bash
   ./bin/fingerprint-collector \
     -iface <IFACE> \
     -host aiplatform.googleapis.com \
     -target-name gemini_advanced \
     -sample-count 5 \
     -duration 180 \
     -confirm-pre-flight
   ```

10. 另一个终端执行已有 Vertex 最小 Gemini 请求脚本。

11. 验证输出。

    ```bash
    ./verify-capture.sh ./output/gemini_advanced
    grep '"mode_name"' ./output/gemini_advanced/metadata.json
    ```

12. 成功条件：`mode_name=gemini_advanced`，`sample_count > 0`，
    `mitm_check_result=ok`。

13. 删除 raw pcap。

    ```bash
    rm -f ./output/gemini_advanced/raw-pcap-snippet.pcap
    ```

## 5. 把 3 套 output 提交回仓

1. 回到仓库根目录。

   ```bash
   cd ../..
   git status --short
   ```

2. 确认没有 pcap。

   ```bash
   find tools/fingerprint-collector/output -name '*.pcap' -print
   ```

   有输出就删除对应 `.pcap`。

3. 验证三套 capture。

   ```bash
   tools/fingerprint-collector/verify-capture.sh tools/fingerprint-collector/output/openai_codex
   tools/fingerprint-collector/verify-capture.sh tools/fingerprint-collector/output/kiro_cli
   tools/fingerprint-collector/verify-capture.sh tools/fingerprint-collector/output/gemini_advanced
   ```

4. 添加净化输出，不添加 pcap / stdout log / stderr log。

   ```bash
   git add \
     tools/fingerprint-collector/output/openai_codex/clienthello-template.json \
     tools/fingerprint-collector/output/openai_codex/ja3-hashes.txt \
     tools/fingerprint-collector/output/openai_codex/ja4-hashes.txt \
     tools/fingerprint-collector/output/openai_codex/http2-settings.json \
     tools/fingerprint-collector/output/openai_codex/metadata.json \
     tools/fingerprint-collector/output/kiro_cli/clienthello-template.json \
     tools/fingerprint-collector/output/kiro_cli/ja3-hashes.txt \
     tools/fingerprint-collector/output/kiro_cli/ja4-hashes.txt \
     tools/fingerprint-collector/output/kiro_cli/http2-settings.json \
     tools/fingerprint-collector/output/kiro_cli/metadata.json \
     tools/fingerprint-collector/output/gemini_advanced/clienthello-template.json \
     tools/fingerprint-collector/output/gemini_advanced/ja3-hashes.txt \
     tools/fingerprint-collector/output/gemini_advanced/ja4-hashes.txt \
     tools/fingerprint-collector/output/gemini_advanced/http2-settings.json \
     tools/fingerprint-collector/output/gemini_advanced/metadata.json
   ```

5. 提交并推送。

   ```bash
   git commit -m "capture vendor clienthello fingerprints"
   git push
   ```

6. 提交前再跑一次：

   ```bash
   git status --short
   ```

## 6. Troubleshooting

1. MITM warning：关闭企业代理、HTTPS inspection、本地抓包代理、杀毒 HTTPS 扫描后重跑。

2. pcap permission denied：macOS / Linux 用 `sudo` 或 `setcap`；Windows 用管理员 PowerShell。

3. 0 samples：确认 `<IFACE>` 是真实出网接口，客户端在 capture 窗口内发了请求。

4. host 不确定：查 DNS 缓存、客户端日志、Chrome DevTools Network 或防火墙日志。

5. host 错误：只改 `-host` 重跑，不改工具代码。

6. 样本不足：加 `-duration 300`，或临时降 `-sample-count 3` 后再补抓到 5。

7. `mitm_check_result=skipped`：说明用了 `-disable-mitm-detection`，默认不能作为最终提交。

8. `ja3-hashes.txt` 数量不匹配：删除该 target 输出目录，重新抓。

9. Chrome 复用连接：完全退出 Chrome，或新开隐身窗口后先启动 collector 再发请求。

10. VPN 干扰：确认 VPN 不做 TLS inspection；必要时选择 VPN 虚拟接口。

11. IPv6 / IPv4 不一致：刷新 DNS，或临时关闭未使用地址族后重跑。

12. Windows 接口名太长：复制完整 `\Device\NPF_{...}`，用双引号包住。

13. metadata 没有 `mode_name`：确认命令使用了正确 `-target-name`。

14. 输出写到旧 `./output/`：说明没传 `-target-name`，或显式用了旧 `-out`。

