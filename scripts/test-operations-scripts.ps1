$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot

function Assert-Condition {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw "Assertion failed: $Message"
    }
}

$preflight = Join-Path $PSScriptRoot 'preflight.ps1'
$missingAndroidOutput = & $preflight -Components Android -SimulateMissing Android *>&1 | Out-String
$missingAndroid = [pscustomobject]@{
    ExitCode = $LASTEXITCODE
    Output   = $missingAndroidOutput
}

Assert-Condition ($missingAndroid.ExitCode -eq 10) 'A simulated missing Android prerequisite must exit with code 10.'
Assert-Condition ($missingAndroid.Output -match 'MISSING: JDK 17 or later') 'Missing JDK remediation was not reported.'
Assert-Condition ($missingAndroid.Output -match 'MISSING: Android SDK Platform 35 and Build-Tools 35') 'Missing Android SDK remediation was not reported.'
Assert-Condition ($missingAndroid.Output -notmatch '(?i)storePassword|keyPassword|super-secret') 'Prerequisite output must not expose signing values.'

$goOutput = & $preflight -Components Go *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 0) 'The repository-required Go 1.26 installation must satisfy preflight.'
Assert-Condition ($goOutput -match 'OK: Go version 26') 'Go versions must be parsed from the go1.26 format, not as major version 1.'

$machineAndroidOutput = & $preflight -Components Android *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 11) 'This machine must report its discovered JDK 8 as an invalid JDK 17+ prerequisite.'
Assert-Condition ($machineAndroidOutput -match 'INVALID: JDK version 8 is below the required 17') 'The JDK 8 validation result was not reported accurately.'
Assert-Condition ($machineAndroidOutput -match 'MISSING: Android SDK Platform 35 and Build-Tools 35') 'The missing Android SDK result was not reported accurately.'

$dockerOutput = & $preflight -Components Docker *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 11) 'A present but unavailable Docker daemon must be reported as a validation failure.'
Assert-Condition ($dockerOutput -match 'INVALID: Docker Engine could not be validated') 'Docker daemon validation failure was not reported accurately.'
Assert-Condition ($dockerOutput -match 'OK: Docker Compose v2 is available') 'Docker Compose availability must still be reported independently.'

$releaseScript = Get-Content -Raw (Join-Path $PSScriptRoot 'release-android.ps1')
Assert-Condition ($releaseScript -notmatch '(?i)write-(host|output|information).*?(storePassword|keyPassword|keyAlias|storeFile)') 'The release script must not print signing properties.'
Assert-Condition ($releaseScript -match 'check-ignore -q -- android/keystore.properties') 'The release script must require the signing properties file to remain ignored.'

$releaseValidationOutput = & (Join-Path $PSScriptRoot 'release-android.ps1') -ValidateOnly *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 10) 'Missing Android signing inputs must exit with code 10.'
Assert-Condition ($releaseValidationOutput -match 'Missing Android signing inputs') 'Missing Android signing inputs need direct remediation.'
Assert-Condition ($releaseValidationOutput -notmatch '(?i)storePassword|keyPassword|super-secret') 'Release validation output must not expose signing values.'

$testAllScript = Get-Content -Raw (Join-Path $PSScriptRoot 'test-all.ps1')
Assert-Condition ($testAllScript -match "preflight\.ps1'\) -Components Go, Node\s") 'test-all must run Go and frontend checks before Android without requiring a running Docker daemon.'
Assert-Condition ($testAllScript -match 'docker compose -f deploy/docker-compose.yml config --quiet') 'test-all must validate the Compose configuration directly.'
Assert-Condition ($testAllScript -match '\$androidPrerequisiteExit') 'test-all must preserve a nonzero Android prerequisite exit after printing remediation.'

Write-Host 'Operations script checks passed.'
