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
# rate from the intervals ending at t >= 150s (i.e. the window [120s, LegSecs]),
# excluding the ramp the whole-window figure averages in. Raw logs, legs.csv,
# per-arm medians, and `go version -m` provenance for both binaries land in the
# output directory. Analysis beyond medians (drift fit, CI) is left to the
# caller — this script only produces the data.

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

# Provenance: embedded build info for both arms, plus environment.
go version -m $BaseExe *> (Join-Path $outDir "base-goversion.txt")
go version -m $CandExe *> (Join-Path $outDir "cand-goversion.txt")
@(
    "label=$Label"
    "threads=$Threads legSecs=$LegSecs cooldownSecs=$CooldownSecs extraArgs=$ExtraArgs"
    "base=$BaseExe"
    "cand=$CandExe"
    "host=$env:COMPUTERNAME"
    "started=$(Get-Date -Format o)"
) | Set-Content (Join-Path $outDir "env.txt")

# Refuse to run alongside known bench-family strays.
$strays = Get-Process -Name stabbench, bench2_pgo, dero-v114 -ErrorAction SilentlyContinue
if ($strays) { throw "stray bench process running: $($strays.ProcessName -join ', ')" }

function Invoke-Leg([string]$exe, [string]$arm, [int]$index) {
    $log = Join-Path $outDir ("leg-{0:d2}-{1}.log" -f $index, $arm)
    $argList = @("--sustained", "--secs", $LegSecs, "-t", $Threads, "--sa", "v114", "--pin", "--high")
    if ($ExtraArgs) { $argList += ($ExtraArgs -split " ") }
    & $exe @argList *> $log
    if ($LASTEXITCODE -ne 0) { throw "leg $index ($arm) exited $LASTEXITCODE — see $log" }

    # Checkpoint lines: `120+  t=2m30s interval=   18.50 KH/s total=...`.
    # Steady state = the `120+` intervals, each covering 30s past the 120s mark.
    $steadyRates = Select-String -Path $log -Pattern '^120\+\s+t=\S+\s+interval=\s*([0-9.]+) KH/s' |
        ForEach-Object { [double]$_.Matches[0].Groups[1].Value }
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
