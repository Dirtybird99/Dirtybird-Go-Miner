# Builds the release zip: packaging\build-release.ps1 -Version v0.1.2
param([string]$Version = "v0.1.2")

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

go vet . ./internal/...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
go test -short . ./internal/...
if ($LASTEXITCODE -ne 0) { throw "go test failed" }

$stage = Join-Path $root "release\go-miner-$Version-windows-amd64"
Remove-Item (Join-Path $root "release") -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $stage | Out-Null

$env:GOAMD64 = "v3"
$pgo = if (Test-Path (Join-Path $root "default.pgo")) { "-pgo=default.pgo" } else { "-pgo=off" }
go build $pgo -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $stage "go-miner.exe") .
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

Copy-Item (Join-Path $root "config.json") $stage
Copy-Item (Join-Path $root "start.bat") $stage
Copy-Item (Join-Path $root "README.md") (Join-Path $stage "README.md")
Copy-Item (Join-Path $root "LICENSE") $stage
Copy-Item (Join-Path $root "THIRD-PARTY-LICENSES") $stage
Copy-Item (Join-Path $root "internal\astrobwt\LICENSE-DERO.txt") $stage

& (Join-Path $stage "go-miner.exe") --selftest
if ($LASTEXITCODE -ne 0) { throw "selftest failed" }

$zip = Join-Path $root "release\go-miner-$Version-windows-amd64.zip"
$sums = Join-Path $root "release\SHA256SUMS.txt"
Get-ChildItem $stage -File | ForEach-Object {
    "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
} | Set-Content $sums
Copy-Item $sums $stage

Compress-Archive -Path "$stage\*" -DestinationPath $zip -Force
"{0}  {1}" -f (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower(), (Split-Path $zip -Leaf) | Add-Content $sums
Write-Host "release ready: $zip"
Get-Content $sums
