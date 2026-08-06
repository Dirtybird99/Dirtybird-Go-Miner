# bench-thue-morse.ps1 — paired sustained A/B runner.
#
# Runs one discarded warm-up leg (base) followed by the 8-leg Thue-Morse order
# B-C-C-B-C-B-B-C. The order is balanced through quadratic drift: base occupies
# leg positions {1,4,6,7} and candidate {2,3,5,8}, whose sums (18/18) and sums
# of squares (102/102) are equal, so neither linear nor quadratic thermal drift
# can load onto the arm contrast. A 4-leg ABBA block cannot make that claim —
# its treatment indicator is exactly representable as a quadratic in leg
# position.
#
# Per leg the script records every checkpoint line and derives a steady-state
# rate from the intervals ENDING strictly after t=120s (the window
# [120s, LegSecs]) — the t=120s checkpoint itself is excluded because its
# interval covers [90s, 120s], which is still ramp. Raw logs, legs.csv,
# per-arm medians, and provenance (SHA256 of both binaries + `go version -m`)
# land in the output directory. Analysis beyond medians (drift fit, CI) is
# left to the caller — this script only produces the data.

param(
    [Parameter(Mandatory = $true)][string]$BaseExe,
    [Parameter(Mandatory = $true)][string]$CandExe,
    [int]$Threads = 20,
    [int]$LegSecs = 240,
    [int]$CooldownSecs = 20,
    [string]$ExtraArgs = "",
    [string]$Label = "run",
    [string]$OutRoot = "bench-results\thue-morse"
)

$ErrorActionPreference = "Stop"

foreach ($exe in @($BaseExe, $CandExe)) {
    if (-not (Test-Path $exe)) { throw "binary not found: $exe" }
}
if ($LegSecs -lt 180) {
    throw "LegSecs must be >= 180 so at least one steady-state interval exists past 120s"
}

$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$outDir = Join-Path $OutRoot "$stamp-$Label"
New-Item -ItemType Directory -Force $outDir | Out-Null

# Provenance: embedded build info plus content hashes for both arms —
# `go version -m` alone cannot distinguish two `go test -c` binaries (it
# stamps neither -pgo nor a revision for them), so the hashes are the only
# proof the arms differ.
go version -m $BaseExe *> (Join-Path $outDir "base-goversion.txt")
go version -m $CandExe *> (Join-Path $outDir "cand-goversion.txt")
$baseHash = (Get-FileHash -Algorithm SHA256 $BaseExe).Hash
$candHash = (Get-FileHash -Algorithm SHA256 $CandExe).Hash
@(
    "label=$Label"
    "threads=$Threads legSecs=$LegSecs cooldownSecs=$CooldownSecs extraArgs=$ExtraArgs"
    "base=$BaseExe sha256=$baseHash"
    "cand=$CandExe sha256=$candHash"
    "armsIdentical=$($baseHash -eq $candHash)"
    "host=$env:COMPUTERNAME"
    "started=$(Get-Date -Format o)"
) | Set-Content (Join-Path $outDir "env.txt")

# Refuse to run alongside bench-family strays — checked before the warm-up
# AND between every leg (a process started mid-block poisons the later legs;
# this workspace has retracted results over exactly that). The campaign's own
# binaries are safe to include because legs run synchronously.
function Assert-NoStrays {
    $strays = Get-Process -Name stabbench, bench2_pgo, dero-v114, "gm_*", "go-miner*" -ErrorAction SilentlyContinue
    if ($strays) { throw "stray bench process running: $($strays.ProcessName -join ', ')" }
}
Assert-NoStrays

function Invoke-Leg([string]$exe, [string]$arm, [int]$index) {
    $log = Join-Path $outDir ("leg-{0:d2}-{1}.log" -f $index, $arm)
    $argList = @("--sustained", "--secs", $LegSecs, "-t", $Threads, "--sa", "v114", "--pin", "--high")
    if ($ExtraArgs) { $argList += ($ExtraArgs -split " ") }
    & $exe @argList *> $log
    if ($LASTEXITCODE -ne 0) { throw "leg $index ($arm) exited $LASTEXITCODE — see $log" }

    # Checkpoint lines: `120+  t=2m30s interval=   18.50 KH/s total=...`.
    # Steady state = intervals ENDING strictly after 120s. The t=120s line is
    # ramp (its interval covers [90s, 120s]), so parse the t= duration and
    # keep > 120s rather than trusting the `120+` label alone.
    $steadyRates = Select-String -Path $log -Pattern '^120\+\s+t=(?:(\d+)m)?(\d+(?:\.\d+)?)s\s+interval=\s*([0-9.]+) KH/s' |
        ForEach-Object {
            $m = $_.Matches[0]
            $endSecs = [double]$m.Groups[2].Value
            if ($m.Groups[1].Success) { $endSecs += 60 * [int]$m.Groups[1].Value }
            if ($endSecs -gt 120) { [double]$m.Groups[3].Value }
        }
    if (-not $steadyRates) { throw "no steady-state checkpoints parsed from $log" }
    $steady = ($steadyRates | Measure-Object -Average).Average

    $wholeLine = Select-String -Path $log -Pattern '= ([0-9.]+) KH/s' | Select-Object -Last 1
    $whole = if ($wholeLine) { [double]$wholeLine.Matches[0].Groups[1].Value } else { 0 }
    [pscustomobject]@{ leg = $index; arm = $arm; steadyKHs = [math]::Round($steady, 4); wholeKHs = $whole }
}

Write-Host "warm-up leg (discarded)..."
Invoke-Leg $BaseExe "warmup" 0 | Out-Null
Start-Sleep $CooldownSecs

$order = @("B", "C", "C", "B", "C", "B", "B", "C")
$rows = @()
for ($i = 0; $i -lt $order.Count; $i++) {
    $arm = $order[$i]
    $exe = if ($arm -eq "B") { $BaseExe } else { $CandExe }
    Write-Host ("leg {0}/8: {1}" -f ($i + 1), $arm)
    Assert-NoStrays
    $rows += Invoke-Leg $exe $arm ($i + 1)
    if ($i -lt $order.Count - 1) { Start-Sleep $CooldownSecs }
}

$rows | Export-Csv (Join-Path $outDir "legs.csv") -NoTypeInformation

$b = ($rows | Where-Object arm -eq "B" | ForEach-Object steadyKHs | Sort-Object)
$c = ($rows | Where-Object arm -eq "C" | ForEach-Object steadyKHs | Sort-Object)
$medB = ($b[1] + $b[2]) / 2
$medC = ($c[1] + $c[2]) / 2
$delta = 100 * ($medC / $medB - 1)
$summary = @(
    "base   steady legs: $($b -join ', ')  median $([math]::Round($medB,4)) KH/s"
    "cand   steady legs: $($c -join ', ')  median $([math]::Round($medC,4)) KH/s"
    "median delta: $([math]::Round($delta,3))%"
    ""
    "NOTE: medians only — run the drift-adjusted fit + CI before any retention decision."
)
$summary | Tee-Object (Join-Path $outDir "summary.txt")
