[CmdletBinding()]
param(
    [switch]$Installer
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot

& (Join-Path $PSScriptRoot 'preflight.ps1') -Components Go, Node, WebView2
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$wailsArguments = @('run', 'github.com/wailsapp/wails/v2/cmd/wails@v2.14.0', 'build', '-clean')
if ($Installer) {
    $wailsArguments += '-nsis'
}

Push-Location (Join-Path $repositoryRoot 'windows-client')
try {
    go @wailsArguments
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}
