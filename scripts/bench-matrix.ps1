param(
    [int]$Secs = 30,
    [int]$Repeat = 1,
    [string[]]$Threads = @(),
    [string]$OutDir = "bench-results",
    [string]$Candidate = "baseline",
    [string]$RegexBenchPath = "",
    [string]$RegexBenchmarkPath = "",
    [string]$CoregexPath = "",
    [string]$UawkPath = "",
    [string]$UawkBenchPath = "",
    [string]$RaceDetectorPath = "",
    [switch]$IncludeOvercommit
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

if ($Secs -le 0) {
    throw "-Secs must be greater than zero"
}
if ($Repeat -le 0) {
    throw "-Repeat must be greater than zero"
}
if ($Candidate.Trim() -eq "") {
    throw "-Candidate must not be empty"
}

$outRoot = if ([System.IO.Path]::IsPathRooted($OutDir)) {
    $OutDir
} else {
    Join-Path $root $OutDir
}
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$runDir = Join-Path $outRoot $stamp
New-Item -ItemType Directory -Force $runDir | Out-Null

$rawLog = Join-Path $runDir "raw.log"
$csvPath = Join-Path $runDir "results.csv"
$aggregatePath = Join-Path $runDir "aggregate.csv"
$summaryPath = Join-Path $runDir "summary.md"
$envPath = Join-Path $runDir "env.json"
$binPath = Join-Path $runDir "go-miner-bench.exe"

function Write-LogLine {
    param([string]$Line)
    Write-Host $Line
    Add-Content -LiteralPath $rawLog -Value $Line
}

function Invoke-Native {
    param(
        [string]$FilePath,
        [string[]]$ArgumentList
    )

    $quotedArgs = $ArgumentList | ForEach-Object {
        if ($_ -match "\s") { '"' + $_ + '"' } else { $_ }
    }
    Write-LogLine ("== " + $FilePath + " " + ($quotedArgs -join " "))

    $output = & $FilePath @ArgumentList 2>&1
    $exitCode = $LASTEXITCODE
    foreach ($line in @($output)) {
        Write-LogLine "$line"
    }
    if ($exitCode -ne 0) {
        throw "command failed with exit code ${exitCode}: $FilePath $($ArgumentList -join ' ')"
    }

    [pscustomobject]@{
        ExitCode = $exitCode
        Output   = @($output | ForEach-Object { "$_" })
    }
}

function Get-GitText {
    param([string[]]$ArgumentList)
    try {
        $text = & git @ArgumentList 2>$null
        if ($LASTEXITCODE -ne 0) {
            return ""
        }
        return ($text -join "`n").Trim()
    } catch {
        return ""
    }
}

function Get-SourceRepo {
    param(
        [string]$Name,
        [string]$Url,
        [string]$Path
    )

    $commit = ""
    $dirty = ""
    if ($Path -ne "" -and (Test-Path -LiteralPath $Path)) {
        $commit = Get-GitText @("-C", $Path, "rev-parse", "HEAD")
        $dirty = Get-GitText @("-C", $Path, "status", "--short")
    }
    [pscustomobject]@{
        name   = $Name
        url    = $Url
        path   = $Path
        commit = $commit
        dirty  = $dirty
    }
}

function Get-CPUInfo {
    try {
        $processors = @(Get-CimInstance Win32_Processor)
        if ($processors.Count -gt 0) {
            $logical = ($processors | Measure-Object -Property NumberOfLogicalProcessors -Sum).Sum
            return [pscustomobject]@{
                Model       = $processors[0].Name
                LogicalCPUs = [int]$logical
            }
        }
    } catch {
    }
    [pscustomobject]@{
        Model       = "unknown"
        LogicalCPUs = [Environment]::ProcessorCount
    }
}

function Convert-ThreadList {
    param([string[]]$Values)

    $parsed = [System.Collections.Generic.List[int]]::new()
    foreach ($value in $Values) {
        foreach ($part in ($value -split ",")) {
            $trimmed = $part.Trim()
            if ($trimmed -eq "") {
                continue
            }
            $n = 0
            if (-not [int]::TryParse(
                $trimmed,
                [System.Globalization.NumberStyles]::Integer,
                [System.Globalization.CultureInfo]::InvariantCulture,
                [ref]$n)) {
                throw "invalid thread count: $trimmed"
            }
            $parsed.Add($n)
        }
    }
    @($parsed)
}

function Add-Run {
    param(
        [System.Collections.Generic.List[object]]$Runs,
        [string]$Name,
        [int]$ThreadCount,
        [string]$Backend,
        [bool]$Pin,
        [bool]$High,
        [bool]$Pair
    )

    $key = "$Name|$ThreadCount|$Backend|$Pin|$High|$Pair"
    foreach ($run in $Runs) {
        if ($run.Key -eq $key) {
            return
        }
    }
    $Runs.Add([pscustomobject]@{
        Key     = $key
        Name    = $Name
        Threads = $ThreadCount
        Backend = $Backend
        Pin     = $Pin
        High    = $High
        Pair    = $Pair
    })
}

function Get-Mean {
    param([double[]]$Values)
    if ($Values.Count -eq 0) {
        return 0.0
    }
    $sum = 0.0
    foreach ($value in $Values) {
        $sum += $value
    }
    $sum / $Values.Count
}

function Get-Median {
    param([double[]]$Values)
    if ($Values.Count -eq 0) {
        return 0.0
    }
    $sorted = @($Values | Sort-Object)
    $mid = [int]($sorted.Count / 2)
    if (($sorted.Count % 2) -eq 1) {
        return [double]$sorted[$mid]
    }
    ([double]$sorted[$mid - 1] + [double]$sorted[$mid]) / 2.0
}

function Get-StdDev {
    param([double[]]$Values)
    if ($Values.Count -le 1) {
        return 0.0
    }
    $mean = Get-Mean $Values
    $sumSquares = 0.0
    foreach ($value in $Values) {
        $delta = $value - $mean
        $sumSquares += $delta * $delta
    }
    [Math]::Sqrt($sumSquares / $Values.Count)
}

function Format-Flags {
    param($Run)
    $flags = [System.Collections.Generic.List[string]]::new()
    if ($Run.pin) { $flags.Add("--pin") }
    if ($Run.high) { $flags.Add("--high") }
    if ($Run.pair) { $flags.Add("--pair") }
    $flags.Add("--sa $($Run.backend)")
    $flags -join " "
}

$cpu = Get-CPUInfo
$logicalCPUs = [int]$cpu.LogicalCPUs
if ($logicalCPUs -lt 1) {
    $logicalCPUs = [Environment]::ProcessorCount
}

$requestedThreads = @(Convert-ThreadList $Threads)
if ($requestedThreads.Count -eq 0) {
    $threadList = @(20)
} else {
    $threadList = $requestedThreads
}
$threadList = @($threadList | Where-Object { $_ -ge 1 -and $_ -le $logicalCPUs } | Sort-Object -Unique)
if ($threadList.Count -eq 0) {
    throw "no valid thread counts selected"
}

$runs = [System.Collections.Generic.List[object]]::new()
foreach ($t in $threadList) {
    Add-Run $runs "v114" $t "v114" $false $false $false
    Add-Run $runs "v114-pin-high" $t "v114" $true $true $false
}
$pairCandidates = @(20)
foreach ($t in $pairCandidates) {
    if ($t -ge 1 -and $t -le $logicalCPUs -and $threadList -contains $t) {
        Add-Run $runs "v114-pair-pin-high" $t "v114" $true $true $true
    }
}
Add-Run $runs "sais-baseline" 1 "sais" $false $false $false

if ($IncludeOvercommit) {
    foreach ($t in @($logicalCPUs + 2, $logicalCPUs + 4)) {
        if ($t -le 255) {
            Add-Run $runs "v114-overcommit-pin-high" $t "v114" $true $true $false
        }
    }
}

$sourceRepos = @(
    Get-SourceRepo "regex-bench" "https://github.com/kolkov/regex-bench" $RegexBenchPath
    Get-SourceRepo "regex-benchmark" "https://github.com/kolkov/regex-benchmark" $RegexBenchmarkPath
    Get-SourceRepo "coregex" "https://github.com/coregx/coregex" $CoregexPath
    Get-SourceRepo "uawk" "https://github.com/kolkov/uawk" $UawkPath
    Get-SourceRepo "uawk-bench" "https://github.com/kolkov/uawk-bench" $UawkBenchPath
    Get-SourceRepo "racedetector" "https://github.com/kolkov/racedetector" $RaceDetectorPath
)

$minerCommit = Get-GitText @("rev-parse", "HEAD")
$minerDirty = Get-GitText @("status", "--short")
$regexBenchRepo = $sourceRepos | Where-Object { $_.name -eq "regex-bench" } | Select-Object -First 1
$regexBenchCommit = if ($null -ne $regexBenchRepo) { $regexBenchRepo.commit } else { "" }
$goVersion = (& go version)
$pgoArg = if (Test-Path -LiteralPath (Join-Path $root "default.pgo")) { "-pgo=default.pgo" } else { "-pgo=off" }

$buildArgs = @("build", $pgoArg, "-trimpath", "-ldflags", "-s -w", "-o", $binPath, ".")
$env:GOAMD64 = "v3"
Invoke-Native "go" $buildArgs | Out-Null

$envInfo = [ordered]@{
    started          = (Get-Date).ToString("o")
    root             = $root
    candidate        = $Candidate
    minerCommit      = $minerCommit
    minerDirty       = $minerDirty
    regexBenchPath   = $RegexBenchPath
    regexBenchCommit = $regexBenchCommit
    sourceRepos      = $sourceRepos
    goVersion        = $goVersion
    cpuModel         = $cpu.Model
    logicalCPUs      = $logicalCPUs
    secs             = $Secs
    repeat           = $Repeat
    pgo              = $pgoArg
    goamd64          = "v3"
    binary           = $binPath
    runCount         = $runs.Count * $Repeat
}
$envInfo | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $envPath

$rows = [System.Collections.Generic.List[object]]::new()
$resultPattern = "(?m)(?<hashes>\d+) hashes in (?<window>\S+) = (?<khs>[0-9.]+) KH/s \((?<per>[0-9.]+) H/s/thread\)"

for ($rep = 1; $rep -le $Repeat; $rep++) {
    foreach ($run in $runs) {
        $args = @("--sustained", "--secs", "$Secs", "-t", "$($run.Threads)", "--sa", $run.Backend)
        if ($run.Pin) {
            $args += "--pin"
        }
        if ($run.High) {
            $args += "--high"
        }
        if ($run.Pair) {
            $args += "--pair"
        }

        $result = Invoke-Native $binPath $args
        $text = $result.Output -join "`n"
        $match = [regex]::Match($text, $resultPattern)
        if (-not $match.Success) {
            throw "could not parse benchmark output for $($run.Name) at $($run.Threads) threads"
        }

        $rows.Add([pscustomobject]@{
            timestamp   = (Get-Date).ToString("o")
            candidate   = $Candidate
            repeat      = $rep
            variant     = $run.Name
            threads     = $run.Threads
            backend     = $run.Backend
            pin         = $run.Pin
            high        = $run.High
            pair        = $run.Pair
            seconds     = $Secs
            hashes      = [uint64]$match.Groups["hashes"].Value
            khs         = [double]$match.Groups["khs"].Value
            perThreadHs = [double]$match.Groups["per"].Value
            command     = $binPath + " " + ($args -join " ")
        })
    }
}

$rows | Export-Csv -LiteralPath $csvPath -NoTypeInformation

$aggregateRaw = foreach ($group in ($rows | Group-Object -Property candidate, variant, threads, backend, pin, high, pair, seconds)) {
    $first = $group.Group[0]
    $khsValues = @($group.Group | ForEach-Object { [double]$_.khs })
    $perValues = @($group.Group | ForEach-Object { [double]$_.perThreadHs })
    [pscustomobject]@{
        candidate       = $first.candidate
        variant         = $first.variant
        threads         = $first.threads
        backend         = $first.backend
        pin             = $first.pin
        high            = $first.high
        pair            = $first.pair
        seconds         = $first.seconds
        runs            = $group.Count
        minKhs          = [double]($khsValues | Measure-Object -Minimum).Minimum
        maxKhs          = [double]($khsValues | Measure-Object -Maximum).Maximum
        meanKhs         = Get-Mean $khsValues
        medianKhs       = Get-Median $khsValues
        stddevKhs       = Get-StdDev $khsValues
        meanPerThreadHs = Get-Mean $perValues
        command         = $first.command
    }
}

$aggregateRows = [System.Collections.Generic.List[object]]::new()
$rank = 1
foreach ($row in @($aggregateRaw | Sort-Object -Property @{ Expression = { $_.medianKhs }; Descending = $true })) {
    $aggregateRows.Add([pscustomobject]@{
        rank            = $rank
        candidate       = $row.candidate
        variant         = $row.variant
        threads         = $row.threads
        backend         = $row.backend
        pin             = $row.pin
        high            = $row.high
        pair            = $row.pair
        seconds         = $row.seconds
        runs            = $row.runs
        minKhs          = $row.minKhs
        maxKhs          = $row.maxKhs
        meanKhs         = $row.meanKhs
        medianKhs       = $row.medianKhs
        stddevKhs       = $row.stddevKhs
        meanPerThreadHs = $row.meanPerThreadHs
        command         = $row.command
    })
    $rank++
}
$aggregateRows | Export-Csv -LiteralPath $aggregatePath -NoTypeInformation

$culture = [System.Globalization.CultureInfo]::InvariantCulture
$best = $aggregateRows[0]

$summary = [System.Collections.Generic.List[string]]::new()
$summary.Add("# Miner Benchmark Matrix $stamp")
$summary.Add("")
$summary.Add("- Candidate: $Candidate")
$summary.Add("- Miner commit: $minerCommit")
$summary.Add("- Go: $goVersion")
$summary.Add("- CPU: $($cpu.Model) ($logicalCPUs logical CPUs)")
$summary.Add("- Window: ${Secs}s, repeat: $Repeat")
$summary.Add("- PGO: $pgoArg, GOAMD64=v3")
$summary.Add("")
$summary.Add("## Source Repos")
$summary.Add("")
$summary.Add("| Repo | Commit | Path |")
$summary.Add("|---|---|---|")
foreach ($repo in $sourceRepos) {
    $commit = if ($repo.commit -ne "") { $repo.commit } else { "(not checked out)" }
    $summary.Add(('| [{0}]({1}) | `{2}` | `{3}` |' -f $repo.name, $repo.url, $commit, $repo.path))
}
$summary.Add("")
$summary.Add("## Best")
$summary.Add("")
$summary.Add("| Candidate | Variant | Threads | Median KH/s | Mean KH/s | StdDev | H/s/thread | Flags |")
$summary.Add("|---|---|---:|---:|---:|---:|---:|---|")
$summary.Add(("| {0} | {1} | {2} | {3} | {4} | {5} | {6} | `{7}` |" -f
    $best.candidate,
    $best.variant,
    $best.threads,
    $best.medianKhs.ToString("F2", $culture),
    $best.meanKhs.ToString("F2", $culture),
    $best.stddevKhs.ToString("F2", $culture),
    $best.meanPerThreadHs.ToString("F1", $culture),
    (Format-Flags $best)))
$summary.Add("")
$summary.Add("## Results")
$summary.Add("")
$summary.Add("| Rank | Candidate | Variant | Threads | Runs | Median KH/s | Min | Max | StdDev | Flags |")
$summary.Add("|---:|---|---|---:|---:|---:|---:|---:|---:|---|")

foreach ($row in $aggregateRows) {
    $summary.Add(("| {0} | {1} | {2} | {3} | {4} | {5} | {6} | {7} | {8} | `{9}` |" -f
        $row.rank,
        $row.candidate,
        $row.variant,
        $row.threads,
        $row.runs,
        $row.medianKhs.ToString("F2", $culture),
        $row.minKhs.ToString("F2", $culture),
        $row.maxKhs.ToString("F2", $culture),
        $row.stddevKhs.ToString("F2", $culture),
        (Format-Flags $row)))
}

$summary.Add("")
$summary.Add("Raw log: ``raw.log``")
$summary.Add("Rows: ``results.csv``")
$summary.Add("Aggregates: ``aggregate.csv``")
$summary.Add("Environment: ``env.json``")

$summary | Set-Content -LiteralPath $summaryPath

Write-Host ""
Write-Host "benchmark results: $runDir"
Write-Host ("best: {0}, {1} threads, median {2} KH/s" -f $best.variant, $best.threads, $best.medianKhs.ToString("F2", $culture))
