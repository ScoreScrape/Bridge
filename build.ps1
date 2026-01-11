param(
    [switch]$SkipPackage  # Build exe only, we can skip fyne packaging
)

$ErrorActionPreference = "Stop"

# Configuration
$AppName    = "ScoreScrape Bridge"
$AppID      = "io.scorescrape.bridge"
$Version    = (Get-Content "VERSION" -Raw).Trim()
$IconPath   = "$PSScriptRoot/assets/Icon.png"
$OutputExe  = "bridge.exe"
$FinalExe   = "$AppName.exe"

Write-Host "Building $AppName v$Version" -ForegroundColor Cyan

# building executable
Write-Host "`n[1/2] Compiling..." -ForegroundColor Yellow
go build -tags gui -o $OutputExe ./cmd/bridge
if ($LASTEXITCODE -ne 0) { exit 1 }
Write-Host "  -> $OutputExe" -ForegroundColor Green

# if we skip packaging, we're done
if ($SkipPackage) {
    Write-Host "`nDone (packaging skipped)" -ForegroundColor Green
    exit 0
}

# packaging application
Write-Host "`n[2/2] Packaging..." -ForegroundColor Yellow
fyne package `
    --target windows `
    --src cmd/bridge `
    --executable $OutputExe `
    --app-id $AppID `
    --name $AppName `
    --app-version $Version `
    --icon $IconPath `
    --tags gui `
    --release

if ($LASTEXITCODE -ne 0) { exit 1 }

# moving packaged exe from cmd/bridge to script directory
$PackagedExe = "cmd/bridge/$FinalExe"
if (Test-Path $PackagedExe) {
    Move-Item $PackagedExe $PSScriptRoot -Force
    Write-Host "  -> $FinalExe" -ForegroundColor Green
}

# remove the moved exe
Remove-Item $OutputExe -ErrorAction SilentlyContinue

Write-Host "`nBuild complete!" -ForegroundColor Green