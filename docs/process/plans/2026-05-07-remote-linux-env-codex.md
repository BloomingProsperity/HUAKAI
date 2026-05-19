# 2026-05-07 Remote Linux Env Codex Plan

| Owner directive | "远程登录新 Linux 服务器，搭好 HUAKAI 项目运行环境。" |
| Scope | In: SSH baseline inspection, system packages, Go 1.25.0, PostgreSQL 16, dev database/user, clone `claude/phase-1`, migrations 0001-0012, backend build/test, gateway smoke. Out: production deployment, secrets rotation, persistent service setup, firewall/domain/TLS setup. |
| Success criteria | Remote server reports expected OS/tools; Go reports 1.25.0; PostgreSQL 16 is installed; `huakai_dev` exists; repo HEAD starts with `9f733d2`; all 12 migrations apply; `go build ./...` succeeds; `go test ./...` completes with tail captured; gateway smoke returns HTTP response from `localhost:8080/admin/v1/usage`. |
| Time estimate | 30-60 minutes wall clock depending on package downloads and Go tests; Codex active time about 20 minutes. |
| Blast radius | Remote development server only. Local repo mutation limited to this plan artifact. No production secrets, deployment scripts, or local business implementation changes are intended. |
| Failure modes | SSH host key or sudo prompt blocks execution; apt repository/key installation fails; Go tarball unavailable; PostgreSQL role/database already exists; migration conflict from prior partial setup; build/test failure due missing env or flaky test; gateway port already occupied. Mitigation: stop at first failing requested step and report raw error plus last verification output. |
| Decision points | Owner confirmation required before changing production credentials, editing remote firewall, creating persistent systemd services, deleting remote repo data, changing database schema beyond requested migrations, or changing HUAKAI source code. |
| Pre-execution checklist | 1. Confirm project rules and start gate. 2. Use SSH key path provided by Owner. 3. Capture baseline OS/tool status. 4. Install only requested base packages if needed. 5. Install requested Go version. 6. Install PostgreSQL 16 from PGDG. 7. Create requested dev-only database resources. 8. Clone and verify branch/SHA. 9. Apply migrations in explicit order. 10. Build/test backend. 11. Run gateway smoke and stop it. |
| Concrete execution order | Follow the Owner-provided numbered steps exactly, adding `pwd && hostname && whoami` style verification after each step. |

Notes:
- This is the Codex independent plan for the requested work. A separate Claude plan is not available in this session; the Owner supplied the execution order directly.
- Dev credential values are intentionally not recorded in this plan artifact.
