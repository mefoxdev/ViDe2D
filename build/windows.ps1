# Windows build on Windows: .\build\windows.ps1 [-Arch amd64|arm64]
param([ValidateSet("amd64", "arm64")][string]$Arch = "amd64")
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$version = (git -C $root describe --tags --always --dirty 2>$null); if (-not $version) { $version = "dev" }
$dist = Join-Path $root "dist"
$out = Join-Path $dist "windows-$Arch"
$zip = Join-Path $dist "vide2d-$version-windows-$Arch.zip"
# Start clean: no stale files from an earlier build in the output or the archive.
if (Test-Path $out) { Remove-Item -Recurse -Force $out }
if (Test-Path $zip) { Remove-Item -Force $zip }
New-Item -ItemType Directory -Force $out | Out-Null
# The caller's Go environment is restored on every exit path.
$saved = @{}
foreach ($n in "CGO_ENABLED", "GOOS", "GOARCH") { $saved[$n] = [Environment]::GetEnvironmentVariable($n, "Process") }
Push-Location $root
try {
    $env:CGO_ENABLED = "0"; $env:GOOS = "windows"; $env:GOARCH = $Arch
    foreach ($t in @(@("vide-run", "", "./cmd/vide-run"), @("vide-client", "-H=windowsgui", "./cmd/vide-client"), @("vide-studio", "-H=windowsgui", "./cmd/vide-studio"))) {
        go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$version $($t[1])" -o "$out\$($t[0]).exe" $t[2]
        if ($LASTEXITCODE -ne 0) { throw "build of $($t[0]) failed" }
    }
}
finally {
    Pop-Location
    foreach ($n in $saved.Keys) { [Environment]::SetEnvironmentVariable($n, $saved[$n], "Process") }
}
# Note: Compress-Archive is not byte-reproducible (it stamps file times); use build/windows.sh for release archives.
Compress-Archive -Force -Path $out -DestinationPath $zip
Write-Host "-> $zip"
