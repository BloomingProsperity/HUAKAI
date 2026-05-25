# 01 — AppLocker / Defender / Smart App Control: Test Binary Block Resolution

**Status:** Diagnosed (2026-04-30). Action required by Owner (one PowerShell click).
**Repo:** `C:\HUAKAI\repo\backend`
**Symptom:** Intermittent `fork/exec ... .test.exe: An Application Control policy has blocked this file.` and `Permission denied` while running `go test ./...`.

---

## 1. Diagnostic — what is actually blocking the binaries

### 1.1 Eliminated suspects

| Suspect | Evidence | Verdict |
|---|---|---|
| **AppLocker** | `Get-AppLockerPolicy -Effective -Xml` returns `<AppLockerPolicy Version="1" />` (no rules). `Microsoft-Windows-AppLocker/EXE and DLL` log shows only event 8001 ("policy applied") with no 8003/8004 deny events. | **Not the cause.** No policy exists. |
| **Microsoft Defender Antivirus** | `Get-MpComputerStatus` confirms RTP is on, but Defender exclusions for `C:\HUAKAI`, `%LOCALAPPDATA%\go-build`, `%TEMP%`, `%USERPROFILE%\go`, `C:\Program Files\Go` are already in place. The error message `An Application Control policy has blocked this file` is **not** an AV detection string; AV detections produce events 1116/1117. None observed for our test binaries. | **Not the cause.** Defender is correctly excluded. |
| **SmartScreen (file MOTW)** | Freshly built `repro.test.exe` has no `Zone.Identifier` ADS (`Get-Item -Stream *` shows only `:$DATA`). No mark-of-the-web on Go-produced files. | **Not the cause.** Go test binaries are not downloaded files. |
| **Enterprise WDAC (custom signing policy)** | The blocking Policy ID is `{0283ac0f-fff1-49ae-ada1-8a933130cad6}`, which is the **public Microsoft "Smart App Control" policy GUID**, not a custom corporate WDAC policy. `Get-CimInstance -Namespace root/Microsoft/Windows/DeviceGuard Win32_DeviceGuard` shows `UsermodeCodeIntegrityPolicyEnforcementStatus = 2` but only because SAC itself is a UMCI-mode policy. | **Not a custom corporate policy** — it's the consumer-tier policy below. |

### 1.2 The actual blocker: **Smart App Control (SAC)**

```
Get-MpComputerStatus | Select SmartAppControlState, SmartAppControlExpiration
  SmartAppControlState      : On
  SmartAppControlExpiration :

Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy'
  VerifiedAndReputablePolicyState : 1   # 1 = Enforced
  SAC_EnforcementReason           : 1
  SAC_PreviousState               : 4294967295   # 0xFFFFFFFF = "On since clean install"

Microsoft-Windows-CodeIntegrity/Operational, recent events:
  3033 / 3077  Code Integrity determined that a process (...go.exe / bash.exe) attempted to
               load ...proto.test.exe / obs.test.exe that did not meet the Enterprise
               signing level requirements or violated code integrity policy
               (Policy ID:{0283ac0f-fff1-49ae-ada1-8a933130cad6}).
  3118         Smart App Control Block Details
```

**Root cause confirmed:** Smart App Control is **On** on this machine. SAC is a Windows 11 consumer feature (different from corporate WDAC). It enforces a Microsoft-signed list of trusted publishers + a cloud reputation service. **Any unsigned executable** without enough cloud reputation gets blocked.

### 1.3 Why the failures are intermittent

- Every `go test -c` produces a binary with a **new SHA-256** (rebuild changes embedded path/timestamps).
- SAC submits unknown-hash binaries to Microsoft's cloud reputation service. While the lookup is in flight, the launch is **denied** — that is the `An Application Control policy has blocked this file` message.
- Once the cloud returns "no opinion → reputable enough," the cached decision allows subsequent launches of the same hash to succeed.
- The "Permission denied" follow-up reflects a brief ACL/handle-lock window after the kernel kill — hence rebuilding "fixes" it.
- Direct `go test ./pkg ...` (no `-c`) works more often because Go writes the test binary into `GOTMPDIR\go-buildXXXXX\bNNN\` with a content-hashed name and the in-process exec handle is shorter-lived; effectively, fewer distinct exec attempts per second hit the SAC cloud.

### 1.4 Why this is NOT corporate AppLocker / WDAC

- `Get-AppLockerPolicy -Effective` returns the empty XML stub; corporate AppLocker would have rule sets.
- The Policy ID `{0283ac0f-fff1-49ae-ada1-8a933130cad6}` is the public SAC policy GUID. A corporate WDAC policy would have a different, IT-managed GUID.
- `SAC_PreviousState = 0xFFFFFFFF` means SAC has been On since first boot of this Windows 11 install (consumer default for clean installs). It is a personal-machine setting, not domain-pushed.

---

## 2. Prescriptive permanent fix — Owner runs once

Smart App Control is a **per-user / per-machine setting**. Disabling it does not require domain admin, but it does require **local Administrator** and a **reboot**. Once turned Off, **SAC cannot be turned back On** (by Microsoft design); the only way to re-enable it is a full Windows reinstall. This is acceptable for a developer workstation.

### 2.1 Option A (recommended) — Disable Smart App Control via Settings UI

1. Open Start → search **"Smart App Control"** → click *App & browser control → Smart App Control*.
2. Set the radio button to **Off**.
3. Confirm the warning dialog ("This setting cannot be turned back on without reinstalling Windows").
4. Reboot.

### 2.2 Option B — Disable via PowerShell (admin)

Run in an **elevated** PowerShell (Right-click PowerShell → *Run as administrator*):

```powershell
# 1. Sanity-check current SAC state
Get-MpComputerStatus | Select-Object SmartAppControlState, SmartAppControlExpiration
Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' |
    Select-Object VerifiedAndReputablePolicyState, SAC_PreviousState

# 2. Turn SAC Off  (0 = Off, 1 = On (Enforced), 2 = Evaluation)
Set-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' `
    -Name 'VerifiedAndReputablePolicyState' -Value 0 -Type DWord

# 3. Reboot for the kernel CI policy to unload
Restart-Computer
```

### 2.3 Verification (after reboot, non-admin OK)

```powershell
# Should report Off
Get-MpComputerStatus | Select-Object -ExpandProperty SmartAppControlState

# Reg state
(Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy').VerifiedAndReputablePolicyState
# Expect: 0

# Functional test — must succeed every time
cd C:\HUAKAI\repo\backend
$env:GOTMPDIR='C:\HUAKAI\repo\backend\.tmp'
& 'C:\Program Files\Go\bin\go.exe' test -count=1 -c -o C:\HUAKAI\repo\backend\.tmp\verify.test.exe ./internal/auth
& C:\HUAKAI\repo\backend\.tmp\verify.test.exe -test.run ZZZNeverMatch  # exit 0 expected

# Confirm CodeIntegrity log no longer records new 3033/3077 events for our binaries:
Get-WinEvent -LogName 'Microsoft-Windows-CodeIntegrity/Operational' -MaxEvents 20 |
    Where-Object { $_.Id -in 3033,3077,3118 -and $_.TimeCreated -gt (Get-Date).AddMinutes(-5) }
# Expect: no rows
```

### 2.4 Defender exclusions to keep (already added by Owner)

These remain useful (real-time AV scan exemption, separate from SAC):

```text
C:\HUAKAI
%LOCALAPPDATA%\go-build
%TEMP%
%USERPROFILE%\go
C:\Program Files\Go
```

No changes needed there.

---

## 3. Workaround — what to use **today**, no admin required

If the Owner is unavailable to disable SAC, the developer can mitigate (not eliminate) the failure rate with the following **non-admin** measures. None of these *defeat* SAC; they reduce the rate of SAC cloud-reputation churn that triggers the block.

### 3.1 Pin GOTMPDIR + GOCACHE inside the repo (Defender-excluded path)

Already-excluded `C:\HUAKAI` is the safest base. Set in user env (run once, persists across shells):

```powershell
# User-level, no admin
[Environment]::SetEnvironmentVariable('GOTMPDIR', 'C:\HUAKAI\repo\backend\.tmp',  'User')
[Environment]::SetEnvironmentVariable('GOCACHE',  'C:\HUAKAI\repo\backend\.gotmp\cache', 'User')
[Environment]::SetEnvironmentVariable('GOMODCACHE','C:\HUAKAI\repo\backend\.gotmp\mod',  'User')
[Environment]::SetEnvironmentVariable('GOTESTFLAGS', '-count=1', 'User')
```

Open a fresh shell to pick them up.

### 3.2 Prefer `go test ./...` over `go test -c && ./xxx.test.exe`

The `-c` workflow produces a long-lived `.test.exe` next to the repo root. SAC fingerprints and re-validates that file on every launch. The non-`-c` path keeps the binary inside `GOTMPDIR\go-buildNNN\bNNN\`, where Go reuses cached binaries by content hash, so SAC's per-hash cache hits more often.

### 3.3 Use the wrapper script with retry

`scripts/run-go-test.sh` (and `.ps1` companion) wraps `go test` with the right env and a 3-attempt retry that re-runs only on the SAC-block exit signature.

### 3.4 Last-resort: pre-warm SAC reputation by signing test binaries with a self-signed cert (NOT recommended)

Self-signed certs are NOT in SAC's trusted root and do **not** improve reputation; this only helps with corporate WDAC, which is not our scenario. Skip.

### 3.5 Fastest no-admin escape hatch (per-shell)

If a single command keeps tripping SAC during a hot loop, set:

```powershell
$env:GOTMPDIR  = 'C:\HUAKAI\repo\backend\.tmp'
$env:GOCACHE   = 'C:\HUAKAI\repo\backend\.gotmp\cache'
$env:GOMODCACHE= 'C:\HUAKAI\repo\backend\.gotmp\mod'
$env:GOFLAGS   = '-count=1'   # prevents stale-cache reuse confusion
```

…then run `go test -tags=integration_pg ./...` directly. This is what is already known to work most of the time.

---

## 4. Test-runner script

Two equivalent entry points are added; both wrap `go test -tags=integration_pg ./...` with retry-on-SAC-block:

- **`C:\HUAKAI\repo\backend\scripts\run-go-test.sh`** — Git Bash / MSYS2.
- **`C:\HUAKAI\repo\backend\scripts\run-go-test.ps1`** — PowerShell.

Both:

1. Force `GOTMPDIR`, `GOCACHE`, `GOMODCACHE` into the repo (Defender-excluded path).
2. Disable `-c` style; run `go test ./...` directly so Go owns the build artifact lifecycle.
3. Retry **only** on the two known SAC-block signatures (max 3 attempts):
   - `An Application Control policy has blocked this file`
   - `fork/exec` + `Permission denied` on a `.test.exe` path
4. Forward additional args, e.g.: `./scripts/run-go-test.sh -run TestFoo ./internal/auth/...`

### Usage

```bash
# default: full suite with integration_pg
./scripts/run-go-test.sh

# focused
./scripts/run-go-test.sh -run TestSettler ./internal/billing/...

# from PowerShell
./scripts/run-go-test.ps1 -run TestSettler ./internal/billing/...
```

---

## 5. Decision log

| Decision | Rationale |
|---|---|
| Recommend turning SAC **Off** | This is the only deterministic fix. SAC is Microsoft's consumer reputation gate; it cannot be configured to allowlist a path or a publisher. There is no AppLocker rule or Defender exclusion that affects it. |
| Document the one-way nature of SAC | Once Off, can't be turned back On without reinstall. Acceptable trade-off for a dev workstation; should NOT be done on a production/end-user machine. |
| Keep Defender exclusions | They cover a different layer (AV real-time scan) and prevent a separate class of intermittent slowdowns and quarantines. |
| No source/test changes | Per task spec: no code edits; only env, script, and machine settings. |

---

## 6. Quick-reference command summary

```powershell
# DIAGNOSE  (any user)
Get-MpComputerStatus | Select SmartAppControlState                              # On / Off / Eval
Get-AppLockerPolicy -Effective -Xml                                              # empty if no AppLocker
Get-WinEvent -LogName 'Microsoft-Windows-CodeIntegrity/Operational' -MaxEvents 20 |
  Where-Object { $_.Id -in 3033,3077,3118 } | Format-List TimeCreated,Id,Message

# FIX       (admin, one-time, reboot required)
Set-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' `
  -Name VerifiedAndReputablePolicyState -Value 0 -Type DWord
Restart-Computer

# WORKAROUND (any user, today)
[Environment]::SetEnvironmentVariable('GOTMPDIR','C:\HUAKAI\repo\backend\.tmp','User')
.\scripts\run-go-test.ps1
```
