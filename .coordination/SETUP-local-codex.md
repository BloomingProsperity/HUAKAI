# local-codex worker setup (new machine)

Bring a fresh machine up as the `local-codex` worker. The worker polls the shared
ledger and, when `local-codex` has assigned/bounced tasks, invokes codex to do one
task per `.coordination/DISPATCH.md`, self-reviews, and marks it `review`.

## Prerequisites
- `git`, `python3` (3.8+), and the **codex CLI** installed.
- Shell = **bash**. On Windows use **WSL2** or **Git Bash** (worker-loop.sh is bash).
- The machine's SSH key must be added to the GitHub repo (the worker pushes `work/*` branches).

## 1. Authenticate codex  (Owner rule: gpt-5.5 + xhigh, **NO fast mode**)
```bash
codex login              # use the account/token you provide
codex --version          # expect 0.134.x
codex login status       # must show logged in
```

## 2. Clone the repo
```bash
git clone git@github.com:BloomingProsperity/HUAKAI.git
cd HUAKAI
git checkout fix/hermes-phase-1-e33d940
git pull
```

## 3. Coord credentials
```bash
mkdir -p ~/.config/huakai-coord
# Save the CA cert (content at the bottom of this file) to:
#   ~/.config/huakai-coord/coord-ca.pem
# COORD_TOKEN = the SHARED dispatch token — copy the COORD_TOKEN line from an
# existing machine's ~/.config/huakai-coord/client.env (same token server-a /
# server-b / the old local-codex use). It is NOT the owner token.
```

## 4. Environment
```bash
export COORD_URL="https://45.8.114.249:8443"
export COORD_TOKEN="<paste the shared dispatch token>"
export COORD_CACERT="$HOME/.config/huakai-coord/coord-ca.pem"
export COORD_AGENT="local-codex"
export WORKER_AI_CMD="codex exec -m gpt-5.5 -c model_reasoning_effort=xhigh -"
```

## 5. Sanity check (proves coord connectivity + TLS + token)
```bash
cd HUAKAI && bash .coordination/task.sh mine     # lists tasks for local-codex (or empty)
```

## 6. Run the worker loop
```bash
cd HUAKAI && bash .coordination/worker-loop.sh
# Auto-refreshes .coordination/ from origin each cycle; high-risk landings still
# park at needs_owner for the Owner. Stop with Ctrl-C.
```

## 7. (Optional) live terminal in /console「机器」tab
```bash
cd HUAKAI && bash .coordination/worker-loop.sh 2>&1 | python3 .coordination/push-stdout.py local-codex
# (needs COORD_URL / COORD_TOKEN / COORD_CACERT exported — same as above)
```

## CA certificate — save as `~/.config/huakai-coord/coord-ca.pem`
```
-----BEGIN CERTIFICATE-----
MIIDIDCCAgigAwIBAgIURBaQn40ukazoT/lE7PyGGWLe92gwDQYJKoZIhvcNAQEL
BQAwFzEVMBMGA1UEAwwMaHVha2FpLWNvb3JkMB4XDTI2MDUzMDE0MDU0NloXDTM2
MDUyNzE0MDU0NlowFzEVMBMGA1UEAwwMaHVha2FpLWNvb3JkMIIBIjANBgkqhkiG
9w0BAQEFAAOCAQ8AMIIBCgKCAQEAmPi1SHjtn3HChL/D5MbXqB48gFbn+aHKG/gG
aDrdSXMXBKXG8EndhkdM02U8nsXuPH5JpuBmjwA4w1Yza35vcKFHQvlUPYnAb+qq
IhoUsKDsA76jjk7T3W+6uv0rr/SKCtsh7c79wXt1hgxlfNQDO1DKJ1pMoDswkS/f
e+AdVbcLLhDjd8v56Ut66K2hVjOUTxfmfpecDdugoWCCMDFqlOBpRqxgzZckFaDu
DHvUWQWV5PBxmxxe+0H3F+zswh+uRYlXokvbOQiydU7cegiqkVe2QuYbPXyBFU7z
ibJE0TdGsGqgb+IoFJ1WCxTWF0v8ZxOte26r4sk5AR0ujpu1nQIDAQABo2QwYjAd
BgNVHQ4EFgQU0uhJ6sDa+rCMRPu7/Ki65Aex/rIwHwYDVR0jBBgwFoAU0uhJ6sDa
+rCMRPu7/Ki65Aex/rIwDwYDVR0TAQH/BAUwAwEB/zAPBgNVHREECDAGhwQtCHL5
MA0GCSqGSIb3DQEBCwUAA4IBAQB14KA1Ju/MKYZhG8muurPCdnEcNXLAeBv9O9Bd
a+6kAczmvplYh6gxTdXtRAzryJ17h9pvd3QRhsKCeJILzSInIuB/vRrE6fM+54NC
QfR7xfYOdLsmh3RrGTev5EX3kg07kKN5WnJUEkrH/8s5SQ+38oSJrTN2Xf3HYnqf
m8RkJLSpVVRBlwhALmW/T2VsGt5OM7pC/p1+d/jXoaes2MILXnRujOIU6ovAs7UO
5bFqBJa4psCz0lqsd/rhS68LXhTl3/pdMhRbegLHdEdduwnXpgj2KubHWXlgftRx
qJUoxLZmF6metAkef4IsNc3p94lM+ebhuNz2NH05w8tDCx5O
-----END CERTIFICATE-----
```

## Notes
- `COORD_AGENT` MUST be unique per machine — keep it `local-codex` so the ledger
  routes the same tasks to the replacement machine.
- The worker self-reviews each commit with codex (#8). If codex auth fails (401),
  tell the PM (server-a) — the PM compensates with manual + mutation audits.
- Stop the OLD local-codex machine's worker-loop before starting the new one, so two
  machines don't both claim `local-codex` tasks.
