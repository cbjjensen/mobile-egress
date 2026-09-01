$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path -Parent $PSScriptRoot

& (Join-Path $PSScriptRoot 'stage-branding.ps1')

Push-Location $clientRoot
try {
    go run github.com/wailsapp/wails/v2/cmd/wails@v2.14.0 dev
} finally {
    Pop-Location
}
