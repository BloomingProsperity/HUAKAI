# run-go-test.ps1 — PowerShell wrapper around `go test` that survives Smart App Control hiccups.
#
# See run-go-test.sh for the full rationale; this is the PowerShell-native sibling.
# Permanent fix: disable Smart App Control. See docs/01_APPLOCKER_DEFENDER_RESOLUTION.md.

[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]] $GoTestArgs
)

$ErrorActionPreference = 'Stop'

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

# --- env (pin into Defender-excluded paths) -----------------------------------
$env:GOTMPDIR    = if ($env:GOTMPDIR)    { $env:GOTMPDIR }    else { Join-Path $RepoRoot '.tmp' }
$env:GOCACHE     = if ($env:GOCACHE)     { $env:GOCACHE }     else { Join-Path $RepoRoot '.gotmp\cache' }
$env:GOMODCACHE  = if ($env:GOMODCACHE)  { $env:GOMODCACHE }  else { Join-Path $RepoRoot '.gotmp\mod' }

foreach ($d in @($env:GOTMPDIR, $env:GOCACHE, $env:GOMODCACHE)) {
    if (-not (Test-Path $d)) { New-Item -ItemType Directory -Force -Path $d | Out-Null }
}

# --- locate go.exe ------------------------------------------------------------
$Go = $env:GO_BIN
if (-not $Go) { $Go = 'C:\Program Files\Go\bin\go.exe' }
if (-not (Test-Path $Go)) {
    $cmd = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($cmd) { $Go = $cmd.Source }
}
if (-not (Test-Path $Go)) {
    Write-Error 'run-go-test.ps1: cannot find go.exe'
    exit 127
}

# --- assemble args ------------------------------------------------------------
if (-not $GoTestArgs -or $GoTestArgs.Count -eq 0) {
    $GoTestArgs = @('-tags=integration_pg', './...')
}

$MaxAttempts = 3
$Sentinels = @(
    'An Application Control policy has blocked this file',
    'fork/exec.*\.test\.exe.*[Pp]ermission denied',
    'fork/exec.*\.test\.exe.*Application Control'
)

Set-Location $RepoRoot

for ($i = 1; $i -le $MaxAttempts; $i++) {
    Write-Host "[run-go-test] attempt ${i}/${MaxAttempts}: go test $($GoTestArgs -join ' ')"
    Write-Host "[run-go-test]   GOTMPDIR=$($env:GOTMPDIR)"
    Write-Host "[run-go-test]   GOCACHE=$($env:GOCACHE)"

    $logFile = New-TemporaryFile

    # Stream to console AND to log; capture exit code from go.exe.
    & $Go test -count=1 @GoTestArgs 2>&1 | Tee-Object -FilePath $logFile.FullName
    $rc = $LASTEXITCODE

    if ($rc -eq 0) {
        Remove-Item $logFile.FullName -Force -ErrorAction SilentlyContinue
        exit 0
    }

    $logText = Get-Content $logFile.FullName -Raw -ErrorAction SilentlyContinue
    Remove-Item $logFile.FullName -Force -ErrorAction SilentlyContinue

    $sacHit = $false
    foreach ($pat in $Sentinels) {
        if ($logText -match $pat) { $sacHit = $true; break }
    }

    if ($sacHit) {
        $sleepSec = $i * 2
        Write-Host "[run-go-test] SAC block detected (rc=$rc); retrying in ${sleepSec}s..."
        Start-Sleep -Seconds $sleepSec
        continue
    }

    Write-Host "[run-go-test] non-SAC failure (rc=$rc); not retrying."
    exit $rc
}

Write-Host "[run-go-test] exhausted $MaxAttempts attempts."
Write-Host '[run-go-test] consider disabling Smart App Control - see docs/01_APPLOCKER_DEFENDER_RESOLUTION.md'
exit 75
