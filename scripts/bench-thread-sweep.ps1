# bench-thread-sweep.ps1 — same-session thread-count sweep across miners.
#
# For each thread count the arms run back-to-back in a balanced mirror order
# (A B C C B A), so slow drift loads equally on every arm at every point. Each
# leg is one cold --sustained run of -Secs seconds; the sweep reports the leg
# rate and the derived per-thread efficiency relative to the arm's own 1T and
# 4T points. A background sampler records per-logical-processor Actual
# Frequency every 5 s (no elevation needed) so a clock story can be read off
# the same timeline.
#
# Arms:
#   go-x1   <GoExe> --sustained --secs S -t T --sa v114 --pair=false --pin --high
#   go-x2   <GoExe> --sustained --secs S -t T --sa v114 --pair --pin --high
#   zig-x2  <ZigExe> T S 1            (bench2 binary: paired pipeline, pinned)
#
# Output dir: bench-results\thread-sweep\<stamp>-<Label>\ with legs.csv,
# freq.csv, env.txt, raw per-leg logs and summary.md. Raw data only; the
# write-up lives in PERF_RESEARCH.md / the vault.

param(
    [Parameter(Mandatory = $true)][string]$GoExe,
    [string]$ZigExe = "",
    [string]$Threads = "1,2,4,8,12,16,20",
    [int]$Secs = 60,
    [int]$CooldownSecs = 30,
    [int]$PreCooldownSecs = 180,
    [string]$Label = "sweep",
    [string]$OutRoot = "bench-results\thread-sweep"
)

$ErrorActionPreference = "Stop"
if (-not (Test-Path $GoExe)) { throw "binary not found: $GoExe" }
if ($ZigExe -ne "" -and -not (Test-Path $ZigExe)) { throw "binary not found: $ZigExe" }
# -Threads arrives as one string under pwsh -File; accept "1,2,4" or "1 2 4"
[int[]]$Threads = ($Threads -split "[ ,]+" | Where-Object { $_ -ne "" } | ForEach-Object { [int]$_ })
foreach ($t in $Threads) { if ($t -lt 1 -or $t -gt 20) { throw "thread count $t outside 1..20 (20 is the cap)" } }

$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$outDir = Join-Path $OutRoot "$stamp-$Label"
New-Item -ItemType Directory -Force $outDir | Out-Null

function Assert-NoStrays {
    $strays = Get-Process -Name stabbench, bench2_pgo, bench2_nopgo, dero-v114, "gm_*", "go-miner*", "dero-astrox*" -ErrorAction SilentlyContinue
    if ($strays) { throw "stray bench process running: $($strays.ProcessName -join ', ')" }
}
Assert-NoStrays

# Provenance
$goHash = (Get-FileHash -Algorithm SHA256 $GoExe).Hash
go version -m $GoExe *> (Join-Path $outDir "go-goversion.txt")
$zigHash = if ($ZigExe -ne "") { (Get-FileHash -Algorithm SHA256 $ZigExe).Hash } else { "" }
@(
    "label=$Label threads=$($Threads -join ',') secs=$Secs cooldownSecs=$CooldownSecs preCooldownSecs=$PreCooldownSecs"
    "go=$GoExe sha256=$goHash"
    "zig=$ZigExe sha256=$zigHash"
    "host=$env:COMPUTERNAME"
    "started=$(Get-Date -Format o)"
) | Set-Content (Join-Path $outDir "env.txt")

# Arms in mirror order
$arms = @("go-x1", "go-x2")
if ($ZigExe -ne "") { $arms += "zig-x2" }
$order = @($arms) + @($arms[($arms.Count - 1)..0])

function Invoke-Leg([string]$arm, [int]$t, [int]$rep) {
    $log = Join-Path $outDir ("t{0:d2}-{1}-r{2}.log" -f $t, $arm, $rep)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    switch ($arm) {
        "go-x1"  { $psi.FileName = (Resolve-Path $GoExe);  $psi.Arguments = "--sustained --secs $Secs -t $t --sa v114 --pair=false --pin --high" }
        "go-x2"  { $psi.FileName = (Resolve-Path $GoExe);  $psi.Arguments = "--sustained --secs $Secs -t $t --sa v114 --pair --pin --high" }
        "zig-x2" { $psi.FileName = (Resolve-Path $ZigExe); $psi.Arguments = "$t $Secs 1"; $psi.WorkingDirectory = (Split-Path (Resolve-Path $ZigExe)) }
    }
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $start = Get-Date
    $p = [System.Diagnostics.Process]::Start($psi)
    $out = $p.StandardOutput.ReadToEnd() + $p.StandardError.ReadToEnd()
    $p.WaitForExit()
    $end = Get-Date
    Set-Content $log $out
    $khs = $null
    switch ($arm) {
        { $_ -like "go-*" } { if ($out -match '(?m)^\d+ hashes in [^=]+= ([0-9.]+) KH/s') { $khs = [double]$Matches[1] } }
        "zig-x2"            { if ($out -match '-> ([0-9.]+) KH/s total') { $khs = [double]$Matches[1] } }
    }
    if ($p.ExitCode -ne 0 -or $null -eq $khs) { throw "leg $arm t=$t rep=$rep failed: exit $($p.ExitCode), rate unparsed (see $log)" }
    [pscustomobject]@{ threads = $t; arm = $arm; rep = $rep; khs = $khs; perThread = [math]::Round($khs * 1000 / $t, 1); start = $start.ToString("o"); end = $end.ToString("o") }
}

# Background frequency sampler (Actual Frequency per logical processor, 5 s)
$freqCsv = Join-Path $outDir "freq.csv"
$sampler = Start-Job -ScriptBlock {
    param($csv)
    "time,instance,mhz" | Set-Content $csv
    while ($true) {
        try {
            $s = Get-Counter '\Processor Information(*)\Actual Frequency' -ErrorAction Stop
            $ts = (Get-Date).ToString("o")
            foreach ($c in $s.CounterSamples) { if ($c.InstanceName -ne "_Total" -and $c.InstanceName -notmatch '_Total') { Add-Content $csv "$ts,$($c.InstanceName),$([math]::Round($c.CookedValue))" } }
        } catch { }
        Start-Sleep 5
    }
} -ArgumentList $freqCsv

$legs = @()
try {
    Write-Host "pre-cooldown $PreCooldownSecs s"
    Start-Sleep $PreCooldownSecs
    foreach ($t in $Threads) {
        $rep = 0
        foreach ($arm in $order) {
            $rep++
            Assert-NoStrays
            Write-Host ("t={0,2} {1,-7} rep {2} ..." -f $t, $arm, $rep) -NoNewline
            $leg = Invoke-Leg $arm $t $rep
            $legs += $leg
            Write-Host (" {0,8:N3} KH/s ({1,7:N1} H/s/thr)" -f $leg.khs, $leg.perThread)
            $legs | Export-Csv (Join-Path $outDir "legs.csv") -NoTypeInformation
            Start-Sleep $CooldownSecs
        }
    }
} finally {
    Stop-Job $sampler -ErrorAction SilentlyContinue | Out-Null
    Remove-Job $sampler -Force -ErrorAction SilentlyContinue | Out-Null
}

# Summary: median of the two reps per (arm, t); efficiency vs the arm's own 1T and 4T per-thread rates
$summary = @("# thread sweep $Label", "", "| arm | threads | KH/s (median of 2) | H/s/thread | eff vs 1T | eff vs 4T |", "|---|---:|---:|---:|---:|---:|")
foreach ($arm in $arms) {
    $rows = $legs | Where-Object arm -eq $arm
    $med = @{}
    foreach ($t in $Threads) {
        $v = ($rows | Where-Object threads -eq $t | ForEach-Object khs | Sort-Object)
        if ($v.Count -gt 0) { $med[$t] = ($v[[math]::Floor(($v.Count - 1) / 2)] + $v[[math]::Ceiling(($v.Count - 1) / 2)]) / 2 }
    }
    $pt1 = if ($med.ContainsKey(1)) { $med[1] } else { $null }
    $pt4 = if ($med.ContainsKey(4)) { $med[4] / 4 } else { $null }
    foreach ($t in $Threads) {
        if (-not $med.ContainsKey($t)) { continue }
        $pt = $med[$t] / $t
        $e1 = if ($pt1) { "{0:N1}%" -f (100 * $pt / $pt1) } else { "-" }
        $e4 = if ($pt4) { "{0:N1}%" -f (100 * $pt / $pt4) } else { "-" }
        $summary += ("| {0} | {1} | {2:N3} | {3:N1} | {4} | {5} |" -f $arm, $t, $med[$t], ($pt * 1000), $e1, $e4)
    }
}
$summary += ""
$summary += "Legs: $($legs.Count); order per thread count: $($order -join ' ')"
$summary | Set-Content (Join-Path $outDir "summary.md")
Write-Host ""
Get-Content (Join-Path $outDir "summary.md")
Write-Host "results: $outDir"
