$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path -Parent $PSScriptRoot

Push-Location $clientRoot
try {
    go run github.com/wailsapp/wails/v2/cmd/wails@v2.14.0 build -clean
} finally {
    Pop-Location
}
