$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Push-Location $repositoryRoot
try {
    & (Join-Path $repositoryRoot 'scripts/preflight.ps1') -Components Go
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    # The native GUI talks to the installed protected Client service.
    go run ./windows-client/cmd/mobile-egress-client-app
    exit $LASTEXITCODE
} finally { Pop-Location }
