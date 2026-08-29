[CmdletBinding()]
param(
    [string]$Tag = 'mobile-egress-relay:local'
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot

& (Join-Path $PSScriptRoot 'preflight.ps1') -Components Docker
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

Push-Location $repositoryRoot
try {
    docker build --file relay/Dockerfile --tag $Tag .
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}
