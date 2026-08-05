# bench-micro-couples.ps1 — paired pinned microbenchmark screen.
#
# Runs N alternating (base, candidate) couples of a single benchmark, each
# invocation pinned to one core via process affinity with high priority, and
# reports the per-couple log-ratios. Pairing makes slow environmental drift
# common-mode: the estimate comes from within-couple contrasts seconds apart,
# not from two pooled arm means minutes apart, which is what an autocorrelated
# 8-10% CoV defeats.
#
# Expects PRE-BUILT test binaries (go test -c) so affinity applies to the
# process that actually hashes and no compile runs between legs:
#   $env:GOAMD64='v3'; go test -c "-pgo=default.pgo" -o gm_bench_base.exe ./internal/astrobwt
#
# Affinity 0x1 is the first P-core sibling on this box's P-core-first layout;
# 0x10000 is the first E-core (the mask the LUT experiment established).

param(
    [Parameter(Mandatory = $true)][string]$BaseExe,
    [Parameter(Mandatory = $true)][string]$CandExe,
    [int]$Couples = 20,
    [string]$Bench = "^BenchmarkHashV114$",
    [string]$BenchTime = "600x",
    [long]$AffinityMask = 0x1,
    [string]$Label = "micro",
    [string]$OutRoot = "bench-results\micro-couples"
)

$ErrorActionPreference = "Stop"
foreach ($exe in @($BaseExe, $CandExe)) {
    if (-not (Test-Path $exe)) { throw "binary not found: $exe" }
}

$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$outDir = Join-Path $OutRoot "$stamp-$Label"
New-Item -ItemType Directory -Force $outDir | Out-Null
go version -m $BaseExe *> (Join-Path $outDir "base-goversion.txt")
go version -m $CandExe *> (Join-Path $outDir "cand-goversion.txt")
"label=$Label couples=$Couples bench=$Bench benchtime=$BenchTime affinity=0x$($AffinityMask.ToString('x')) started=$(Get-Date -Format o)" |
    Set-Content (Join-Path $outDir "env.txt")

function Invoke-Arm([string]$exe, [string]$name, [int]$couple) {
    $log = Join-Path $outDir ("c{0:d2}-{1}.log" -f $couple, $name)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = (Resolve-Path $exe)
    $psi.Arguments = "-test.run=^$ -test.bench=$Bench -test.benchtime=$BenchTime -test.count=1 -test.benchmem"
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $p = [System.Diagnostics.Process]::Start($psi)
    $p.ProcessorAffinity = [IntPtr]$AffinityMask
    $p.PriorityClass = "High"
    $out = $p.StandardOutput.ReadToEnd() + $p.StandardError.ReadToEnd()
    $p.WaitForExit()
    Set-Content $log $out
    if ($p.ExitCode -ne 0) { throw "arm $name couple $couple exited $($p.ExitCode) — see $log" }
    $m = [regex]::Match($out, '(?m)^Benchmark\S+\s+\d+\s+([0-9.]+) ns/op')
    if (-not $m.Success) { throw "no ns/op parsed from $log" }
    [double]$m.Groups[1].Value
}

$rows = @()
for ($i = 1; $i -le $Couples; $i++) {
    $b = Invoke-Arm $BaseExe "base" $i
    $c = Invoke-Arm $CandExe "cand" $i
    $ratio = [math]::Log($b / $c)   # >0 means candidate is faster (fewer ns/op)
    $rows += [pscustomobject]@{
        couple = $i; baseNs = $b; candNs = $c
        logRatio = [math]::Round($ratio, 6)
        pct = [math]::Round(100 * ($b / $c - 1), 3)
    }
    Write-Host ("couple {0:d2}: base {1:n0} cand {2:n0} -> {3:+0.00;-0.00}%" -f $i, $b, $c, $rows[-1].pct)
}
$rows | Export-Csv (Join-Path $outDir "couples.csv") -NoTypeInformation

$lr = $rows | ForEach-Object logRatio
$mean = ($lr | Measure-Object -Average).Average
$sd = [math]::Sqrt((($lr | ForEach-Object { [math]::Pow($_ - $mean, 2) }) | Measure-Object -Sum).Sum / ($lr.Count - 1))
$se = $sd / [math]::Sqrt($lr.Count)
# t crit for df=19: 2.093 two-sided 95%, 1.729 one-sided 95%.
$t2 = 2.093; $t1 = 1.729
$pct = { param($v) 100 * ([math]::Exp($v) - 1) }
$summary = @(
    "couples=$($lr.Count) affinity=0x$($AffinityMask.ToString('x'))"
    ("mean effect (cand vs base): {0:+0.000;-0.000}%" -f (& $pct $mean))
    ("95% CI: [{0:+0.000;-0.000}%, {1:+0.000;-0.000}%]" -f (& $pct ($mean - $t2 * $se)), (& $pct ($mean + $t2 * $se)))
    ("one-sided 95% lower bound: {0:+0.000;-0.000}%" -f (& $pct ($mean - $t1 * $se)))
    "NOTE: micro is a screen, not the retention gate; 20T sustained is primary."
)
$summary | Tee-Object (Join-Path $outDir "summary.txt")
