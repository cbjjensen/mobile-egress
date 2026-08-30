$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot

function Invoke-RequiredCommand {
    param(
        [string]$Name,
        [scriptblock]$Command
    )

    Write-Host "==> $Name"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE."
    }
}

Push-Location $repositoryRoot
try {
    & (Join-Path $PSScriptRoot 'preflight.ps1') -Components Go, Node
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    Invoke-RequiredCommand -Name 'Go tests' -Command { go test ./... }
    Invoke-RequiredCommand -Name 'Go vet' -Command { go vet ./... }
    Invoke-RequiredCommand -Name 'Go build' -Command { go build ./... }

    Push-Location (Join-Path $repositoryRoot 'windows-client\frontend')
    try {
        Invoke-RequiredCommand -Name 'Frontend typecheck' -Command { npm run check }
        Invoke-RequiredCommand -Name 'Frontend build' -Command { npm run build }
    } finally {
        Pop-Location
    }

    & (Join-Path $PSScriptRoot 'preflight.ps1') -Components Android
    if ($LASTEXITCODE -ne 0) {
        $androidPrerequisiteExit = $LASTEXITCODE
        Write-Host 'Android unit, lint, and assemble checks were not run. Install JDK 17+ and Android SDK Platform 35 with Build-Tools 35, then set JAVA_HOME and ANDROID_HOME (or android/local.properties) before retrying scripts\test-all.ps1.'
        exit $androidPrerequisiteExit
    }

    Push-Location (Join-Path $repositoryRoot 'android')
    try {
        Invoke-RequiredCommand -Name 'Android unit tests' -Command { .\gradlew.bat testDebugUnitTest }
        Invoke-RequiredCommand -Name 'Android lint' -Command { .\gradlew.bat lintDebug }
        Invoke-RequiredCommand -Name 'Android debug assemble' -Command { .\gradlew.bat assembleDebug }
    } finally {
        Pop-Location
    }
} finally {
    Pop-Location
}

Write-Host 'All integration checks passed.'
