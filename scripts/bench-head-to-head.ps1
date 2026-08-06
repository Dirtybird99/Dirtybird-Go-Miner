[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$GoBinary,
    [Parameter(Mandatory)] [string]$RustBinary,
    [Parameter(Mandatory)] [string]$ZigBinary,
    [ValidateSet("x1", "x2")] [string]$Pipeline = "x2",
    [int[]]$Threads = @(1, 20),
    [int]$DurationSecs = 30,
    [int]$CooldownSecs = 180,
    [string]$OutDir = "bench-results\head-to-head",
    [switch]$PinHigh,
    [switch]$DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
if (Test-Path Variable:PSNativeCommandUseErrorActionPreference) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$repoRoot = Split-Path $PSScriptRoot -Parent
$benchRoot = [System.IO.Path]::GetFullPath((Join-Path $repoRoot "bench-results"))

function Resolve-BinaryPath {
    param([string]$Path, [string]$Name)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "$Name binary not found: $Path"
    }
    (Resolve-Path -LiteralPath $Path).Path
}

function Format-Command {
    param([string]$FilePath, [string[]]$ArgumentList)
    (@($FilePath) + $ArgumentList | ForEach-Object {
        if ($_ -match '[\s"]') { '"' + $_.Replace('"', '\"') + '"' } else { $_ }
    }) -join " "
}

function Get-ToolVersion {
    param([string]$Command, [string[]]$ArgumentList)
    try {
        $tool = Get-Command $Command -ErrorAction Stop
        $output = & $tool.Source @ArgumentList 2>&1
        if ($LASTEXITCODE -ne 0) { return "unavailable" }
        (@($output) -join " ").Trim()
    } catch {
        "unavailable"
    }
}

function Get-Artifact {
    param([string]$Name, [string]$Path)
    $item = Get-Item -LiteralPath $Path
    [ordered]@{
        name         = $Name
        path         = $item.FullName
        bytes        = $item.Length
        lastWriteUtc = $item.LastWriteTimeUtc.ToString("o")
        sha256       = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

function Get-RunArguments {
    param([string]$Miner, [int]$ThreadCount)
    switch ($Miner) {
        "Go" {
            $runArgs = @("--sustained", "--secs", "$DurationSecs", "-t", "$ThreadCount", "--sa", "v114")
            $runArgs += if ($Pipeline -eq "x2") { "--pair" } else { "--pair=false" }
            $runArgs += if ($PinHigh) { @("--pin", "--high") } else { "--pin=false" }
            return $runArgs
        }
        "Rust" {
            $runArgs = @("--sustained", "--secs", "$DurationSecs", "-t", "$ThreadCount")
            if ($Pipeline -eq "x1") { $runArgs += "--no-2way" }
            if ($PinHigh) { $runArgs += "--pin" } else { $runArgs += "--normal-priority" }
            return $runArgs
        }
        "Zig" {
            # Pass bench.exe for x1 or bench2.exe for x2; both use this positional CLI.
            return @("$ThreadCount", "$DurationSecs", $(if ($PinHigh) { "1" } else { "0" }), "0", "0")
        }
    }
    throw "unknown miner: $Miner"
}

function Get-Khs {
    param([string]$Miner, [string]$Output)
    $pattern = switch ($Miner) {
        "Go" { '(?m)^\d+ hashes in \S+ = (?<khs>[0-9.]+) KH/s' }
        "Rust" { '(?m)^HASHRATE\s+:.*\((?<khs>[0-9.]+) KH/s\)' }
        "Zig" { '(?m)-> (?<khs>[0-9.]+) KH/s total' }
    }
    $match = [regex]::Match($Output, $pattern)
    if ($match.Success) { return [double]$match.Groups["khs"].Value }
    [double]::NaN
}

function Get-ObservedPipeline {
    param([string]$Miner, [string]$Output)
    $pattern = switch ($Miner) {
        "Go" { '(?m)^go-miner .* sustained bench:.*pipeline=(?<pipeline>x[12])$' }
        "Rust" { '(?m)^sustained: .*pipeline=(?<pipeline>x[12]),' }
        "Zig" { '(?m)^(?<pipeline>bench2|bench):' }
    }
    $match = [regex]::Match($Output, $pattern)
    if (-not $match.Success) { return "" }
    if ($Miner -eq "Zig") {
        return $(if ($match.Groups["pipeline"].Value -eq "bench2") { "x2" } else { "x1" })
    }
    $match.Groups["pipeline"].Value
}

if ($DurationSecs -le 0) { throw "-DurationSecs must be greater than zero" }
if ($CooldownSecs -lt 0) { throw "-CooldownSecs must not be negative" }
if ($Threads.Count -eq 0 -or @($Threads | Where-Object { $_ -le 0 -or $_ -gt 255 }).Count -gt 0) {
    throw "-Threads must contain values from 1 through 255"
}
if ($PinHigh -and @($Threads | Where-Object { $_ -gt 24 }).Count -gt 0) {
    throw "-PinHigh supports at most 24 threads with the frozen Zig affinity map"
}

$goPath = Resolve-BinaryPath $GoBinary "Go"
$rustPath = Resolve-BinaryPath $RustBinary "Rust"
$zigPath = Resolve-BinaryPath $ZigBinary "Zig"
$binaryByName = @{ Go = $goPath; Rust = $rustPath; Zig = $zigPath }
$threadCounts = @($Threads | Sort-Object -Unique)
$balancedOrder = @("Go", "Rust", "Zig", "Zig", "Rust", "Go")

# Make Rust's runtime-selected pipeline/topology independent of inherited shell
# state, then restore every caller value even on DryRun or failure. Go and Zig
# receive equivalent explicit CLI settings below.
$rustEnvNames = @(
    "MINER_2WAY", "PIN_SMART", "SUSTAINED_NOPRIO", "PIN_CORES",
    "DERO_MATERIALIZE", "DERO_NO_FUSE", "AVX2OPS", "DERO_V114_CPP",
    "DLUNA_STAGE5_COUNT1_SINGLETONS"
)
$rustEnvBefore = @{}
foreach ($name in $rustEnvNames) {
    $rustEnvBefore[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

try {
    $env:MINER_2WAY = $(if ($Pipeline -eq "x2") { "1" } else { "0" })
    $env:PIN_SMART = $(if ($PinHigh) { "1" } else { "0" })
    $env:SUSTAINED_NOPRIO = $(if ($PinHigh) { "0" } else { "1" })
    foreach ($name in @(
        "PIN_CORES", "DERO_MATERIALIZE", "DERO_NO_FUSE", "AVX2OPS",
        "DERO_V114_CPP", "DLUNA_STAGE5_COUNT1_SINGLETONS"
    )) {
        [Environment]::SetEnvironmentVariable($name, $null, "Process")
    }

$outBase = if ([System.IO.Path]::IsPathRooted($OutDir)) {
    [System.IO.Path]::GetFullPath($OutDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repoRoot $OutDir))
}
if (-not ($outBase.Equals($benchRoot, [System.StringComparison]::OrdinalIgnoreCase) -or
          $outBase.StartsWith($benchRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase))) {
    throw "-OutDir must stay under ignored bench-results: $benchRoot"
}

Write-Host "diagnostic head-to-head: pipeline=$Pipeline threads=$($threadCounts -join ',') duration=${DurationSecs}s cooldown=${CooldownSecs}s pinHigh=$($PinHigh.IsPresent)"
Write-Host "order per thread count: $($balancedOrder -join '-')"
foreach ($threadCount in $threadCounts) {
    foreach ($minerName in $balancedOrder) {
        $runArgs = @(Get-RunArguments $minerName $threadCount)
        Write-Host ("cooldown {0}s -> {1}" -f $CooldownSecs, (Format-Command $binaryByName[$minerName] $runArgs))
    }
}
if ($DryRun) { return }

$stamp = Get-Date -Format "yyyyMMdd-HHmmss-fff"
$runDir = Join-Path $outBase $stamp
New-Item -ItemType Directory -Force -Path $runDir | Out-Null
$runsPath = Join-Path $runDir "runs.csv"
$manifestPath = Join-Path $runDir "manifest.json"
$zigVersion = Get-ToolVersion "zig" @("version")
if ($zigVersion -eq "unavailable") {
    $zigRepo = Split-Path (Split-Path (Split-Path $zigPath -Parent) -Parent) -Parent
    $bundledZig = Join-Path $zigRepo ".tools\zig\zig.exe"
    if (Test-Path -LiteralPath $bundledZig -PathType Leaf) {
        $zigVersion = Get-ToolVersion $bundledZig @("version")
    }
}

$manifest = [ordered]@{
    status       = "diagnostic"
    started      = (Get-Date).ToString("o")
    pipeline     = $Pipeline
    threads      = $threadCounts
    durationSecs = $DurationSecs
    cooldownSecs = $CooldownSecs
    pinHigh      = $PinHigh.IsPresent
    order        = $balancedOrder
    rustControls = [ordered]@{
        MINER_2WAY       = $env:MINER_2WAY
        PIN_SMART        = $env:PIN_SMART
        SUSTAINED_NOPRIO = $env:SUSTAINED_NOPRIO
        PIN_CORES        = "cleared"
        DERO_MATERIALIZE = "cleared"
        DERO_NO_FUSE     = "cleared"
        AVX2OPS          = "cleared"
        DERO_V114_CPP    = "cleared"
        DLUNA_STAGE5_COUNT1_SINGLETONS = "cleared"
    }
    artifacts    = @(
        Get-Artifact "Go" $goPath
        Get-Artifact "Rust" $rustPath
        Get-Artifact "Zig" $zigPath
    )
    tools        = [ordered]@{
        powershell = $PSVersionTable.PSVersion.ToString()
        go         = Get-ToolVersion "go" @("version")
        rustc      = Get-ToolVersion "rustc" @("--version")
        cargo      = Get-ToolVersion "cargo" @("--version")
        zig        = $zigVersion
    }
}
$manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath

$runs = [System.Collections.Generic.List[object]]::new()
$sequence = 0
foreach ($threadCount in $threadCounts) {
    foreach ($minerName in $balancedOrder) {
        $sequence++
        $runArgs = @(Get-RunArguments $minerName $threadCount)
        $command = Format-Command $binaryByName[$minerName] $runArgs
        Write-Host ""
        Write-Host "[$sequence] cooling down for $CooldownSecs seconds"
        if ($CooldownSecs -gt 0) { Start-Sleep -Seconds $CooldownSecs }
        Write-Host "[$sequence] $command"

        $started = Get-Date
        $launchError = ""
        $exitCode = $null
        try {
            $nativeOutput = & $binaryByName[$minerName] @runArgs 2>&1
            $exitCode = $LASTEXITCODE
        } catch {
            $launchError = $_.Exception.Message
            $nativeOutput = @("PowerShell launch error: $launchError")
        }
        $ended = Get-Date
        $outputLines = @($nativeOutput | ForEach-Object { "$_" })
        $outputLines | ForEach-Object { Write-Host $_ }
        $logName = "{0:d2}-{1}t-{2}.log" -f $sequence, $threadCount, $minerName.ToLowerInvariant()
        $logPath = Join-Path $runDir $logName
        @(
            "# started: $($started.ToString('o'))"
            "# command: $command"
            "# exitCode: $exitCode"
            $outputLines
        ) | Set-Content -LiteralPath $logPath

        $outputText = $outputLines -join "`n"
        $observedPipeline = Get-ObservedPipeline $minerName $outputText
        $khs = Get-Khs $minerName $outputText
        $khsParsed = -not [double]::IsNaN($khs)
        $elapsedSeconds = ($ended - $started).TotalSeconds
        $failure = if ($launchError) {
            "launch error: $launchError"
        } elseif ($exitCode -ne 0) {
            "exit code $exitCode"
        } elseif ($observedPipeline -ne $Pipeline) {
            "requested $Pipeline but observed '$observedPipeline'"
        } elseif (-not $khsParsed) {
            "could not parse final KH/s"
        } elseif ($elapsedSeconds -lt 0.8 * $DurationSecs) {
            # a miner that ignores the duration argument produces a
            # real-looking rate from a run too short to have warmed up
            "leg ended after {0:n2}s against a {1}s window" -f $elapsedSeconds, $DurationSecs
        } else {
            ""
        }
        # a failed leg records no rate: the CSV must never carry a plausible
        # number beside a non-empty error
        $recordedKhs = if ($khsParsed -and -not $failure) { $khs } else { $null }

        $runs.Add([pscustomobject]@{
            sequence  = $sequence
            threads   = $threadCount
            miner     = $minerName
            pipeline  = $Pipeline
            observedPipeline = $observedPipeline
            khs       = $recordedKhs
            started   = $started.ToString("o")
            elapsed   = $elapsedSeconds
            exitCode  = $exitCode
            error     = $failure
            command   = $command
            outputLog = $logName
        })
        $runs | Export-Csv -LiteralPath $runsPath -NoTypeInformation
        $manifest["runs"] = $runs
        $manifest["updated"] = (Get-Date).ToString("o")
        if ($failure) { $manifest["status"] = "failed" }
        $manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath
        if ($failure) {
            throw "$minerName benchmark failed: $failure; partial results: $runDir"
        }
    }
}

$manifest["status"] = "diagnostic-complete"
$manifest["completed"] = (Get-Date).ToString("o")
$manifest["runs"] = $runs
$manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath
Write-Host ""
Write-Host "head-to-head results: $runDir"
} finally {
    foreach ($name in $rustEnvNames) {
        [Environment]::SetEnvironmentVariable($name, $rustEnvBefore[$name], "Process")
    }
}
