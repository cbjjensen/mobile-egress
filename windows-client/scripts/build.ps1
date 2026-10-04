$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Push-Location $repositoryRoot
try {
    & (Join-Path $repositoryRoot 'scripts/preflight.ps1') -Components Go
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    $output = Join-Path $repositoryRoot '.local/client-dev'
    New-Item -ItemType Directory -Path $output -Force | Out-Null
    go build -o (Join-Path $output 'mobile-egress-client-app.exe') ./windows-client/cmd/mobile-egress-client-app
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    go build -o (Join-Path $output 'mobile-egress-client.exe') ./windows-client/cmd/mobile-egress-client
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    Write-Host 'Unsigned development Clients built under .local/client-dev. Use the guarded release process for distribution.'
} finally { Pop-Location }
