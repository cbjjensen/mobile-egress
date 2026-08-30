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

$dockerOutput = & $preflight -Components Docker -SimulateDockerUnavailable *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 11) 'A present but unavailable Docker daemon must be reported as a validation failure.'
Assert-Condition ($dockerOutput -match 'INVALID: Docker Engine could not be validated') 'Docker daemon validation failure was not reported accurately.'
Assert-Condition ($dockerOutput -match 'OK: Docker Compose v2 is available') 'Docker Compose availability must still be reported independently.'

$javaHomeMismatchOutput = & $preflight -Components Android -SimulateJavaHomeMismatch *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 11) 'A JDK 17 PATH with JDK 8 JAVA_HOME mismatch must fail validation.'
Assert-Condition ($javaHomeMismatchOutput -match 'JAVA_HOME.*JDK 8') 'JAVA_HOME must take precedence over a newer PATH javac.'
Assert-Condition ($javaHomeMismatchOutput -match 'PATH JDK 17 is ignored') 'The mismatch remediation must explain that PATH is ignored while JAVA_HOME is set.'

$commonOperations = Join-Path $PSScriptRoot 'operations-common.ps1'
. $commonOperations
$temporarySdkFixture = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-sdk-root-" + [guid]::NewGuid().ToString('N'))
$originalAndroidHome = $env:ANDROID_HOME
$originalAndroidSdkRoot = $env:ANDROID_SDK_ROOT
try {
    $null = New-Item -ItemType Directory -Path $temporarySdkFixture
    $fixtureAndroidDirectory = Join-Path $temporarySdkFixture 'android'
    $null = New-Item -ItemType Directory -Path $fixtureAndroidDirectory
    Set-Content -LiteralPath (Join-Path $fixtureAndroidDirectory 'local.properties') -Value 'sdk.dir=C\:\\fixture-sdk'
    $env:ANDROID_HOME = $null
    $env:ANDROID_SDK_ROOT = $null
    $resolvedSdkRoot = Get-MobileEgressAndroidSdkRoot -RepositoryRoot $temporarySdkFixture
    Assert-Condition ($resolvedSdkRoot -eq 'C:\fixture-sdk') 'The shared SDK resolver must use android/local.properties when environment roots are absent.'
} finally {
    $env:ANDROID_HOME = $originalAndroidHome
    $env:ANDROID_SDK_ROOT = $originalAndroidSdkRoot
    if (Test-Path -LiteralPath $temporarySdkFixture) {
        Remove-Item -LiteralPath (Join-Path $fixtureAndroidDirectory 'local.properties') -Force
        Remove-Item -LiteralPath $fixtureAndroidDirectory -Force
        Remove-Item -LiteralPath $temporarySdkFixture -Force
    }
}

$preflightScript = Get-Content -Raw $preflight
$releaseScript = Get-Content -Raw (Join-Path $PSScriptRoot 'release-android.ps1')
$windowsReleaseScript = Get-Content -Raw (Join-Path $PSScriptRoot 'build-windows.ps1')
Assert-Condition ($preflightScript -match "operations-common\.ps1'\)") 'Preflight must load the shared operations resolver.'
Assert-Condition ($releaseScript -match "operations-common\.ps1'\)") 'Android release must load the shared operations resolver.'
Assert-Condition ($preflightScript -match 'Get-MobileEgressAndroidSdkRoot -RepositoryRoot') 'Preflight must use the shared Android SDK-root resolver.'
Assert-Condition ($releaseScript -match 'Get-MobileEgressAndroidSdkRoot -RepositoryRoot') 'Android release must use the shared Android SDK-root resolver.'

Assert-Condition ($releaseScript -notmatch '(?i)write-(host|output|information).*?(storePassword|keyPassword|keyAlias|storeFile)') 'The release script must not print signing properties.'
Assert-Condition ($releaseScript -match 'check-ignore -q -- android/keystore.properties') 'The release script must require the signing properties file to remain ignored.'
$gitIgnore = Get-Content -Raw (Join-Path $repositoryRoot '.gitignore')
Assert-Condition ($gitIgnore -match '(?m)^android/mobile-egress-release\.jks$') 'The reusable Android release keystore must have an explicit ignore rule.'
$releaseCertificateRecordPath = Join-Path $repositoryRoot 'android\release-signing-certificate.txt'
Assert-Condition (Test-Path -LiteralPath $releaseCertificateRecordPath -PathType Leaf) 'The public Android release certificate identity must be tracked for future comparisons.'
$releaseCertificateRecord = Get-Content -Raw $releaseCertificateRecordPath
Assert-Condition ($releaseCertificateRecord -match '(?im)^SHA-256 fingerprint:\s*(?:[0-9A-F]{2}:){31}[0-9A-F]{2}\s*$') 'The Android release certificate record must contain a colon-delimited SHA-256 fingerprint.'
Assert-Condition ($releaseScript -match 'release-signing-certificate\.txt') 'The Android release must compare against the tracked certificate identity.'
Assert-Condition ($releaseScript -match 'verify --print-certs') 'The Android release must inspect the APK signer certificate.'
Assert-Condition ($windowsReleaseScript -match 'CodeSigningThumbprint') 'Windows release packaging must retain the optional compatibility thumbprint assertion.'
Assert-Condition ($windowsReleaseScript -match 'Get-WindowsReleaseSigningIdentity') 'Windows release packaging must discover the established tracked publisher identity.'
Assert-Condition ($windowsReleaseScript -match 'Set-AuthenticodeSignature') 'Windows release packaging must use built-in PowerShell Authenticode signing.'
Assert-Condition ($windowsReleaseScript -notmatch 'signtool\.exe') 'Windows release packaging must not require Windows SDK signtool.'
Assert-Condition ($windowsReleaseScript -match 'TimeStamperCertificate') 'Windows release packaging must reject signatures without a timestamp certificate.'
Assert-Condition ($windowsReleaseScript -match 'CertificateSha256') 'Windows release packaging must verify the exact SHA-256 certificate identity.'
Assert-Condition ($windowsReleaseScript -match 'release-manifest\.json') 'Windows release packaging must produce the headless Client manifest.'
Assert-Condition ($windowsReleaseScript -match 'embeddedReleaseManifestBase64') 'The signed controller must embed its node-release trust manifest.'
Assert-Condition ($windowsReleaseScript -match 'signerThumbprint') 'The node-release manifest must pin the exact Authenticode signer.'
Assert-Condition ($windowsReleaseScript -notmatch '(?m)^\s*publisher\s*=') 'The mutable release manifest must not choose a trusted publisher.'
Assert-Condition ($windowsReleaseScript -match 'MobileEgressSetup\.exe') 'The controller package must include the guided setup executable.'
Assert-Condition ($windowsReleaseScript -match 'mobile-egress-relay\.exe') 'The controller package must include the local relay.'
Assert-Condition ($windowsReleaseScript -match 'mobile-egress-admin\.exe') 'The controller package must include the elevated helper.'
Assert-Condition ($windowsReleaseScript -match 'mobile-egress-client\.exe') 'The controller package must include the headless Client release.'
Assert-Condition ($windowsReleaseScript -match 'mobile-egress-code-signing\.cer') 'The release ZIP must include the tracked public publisher certificate.'
Assert-Condition ($windowsReleaseScript -match 'release-signing-certificate\.txt') 'The release ZIP must include the public publisher identity record.'

$androidGitRegressionOutput = & (Join-Path $PSScriptRoot 'test-android-git-check.ps1') *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 0) 'The Android Git path check regression must pass under ErrorActionPreference Stop.'
Assert-Condition ($androidGitRegressionOutput -match 'regression passed') 'The Android Git path check regression did not report success.'

$releaseValidationOutput = & (Join-Path $PSScriptRoot 'release-android.ps1') -ValidateOnly -SimulateMissingSigningInputs *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 10) 'Missing Android signing inputs must exit with code 10.'
Assert-Condition ($releaseValidationOutput -match 'Missing Android signing inputs') 'Missing Android signing inputs need direct remediation.'
Assert-Condition ($releaseValidationOutput -notmatch '(?i)storePassword|keyPassword|super-secret') 'Release validation output must not expose signing values.'

$missingKeystoreOutput = & (Join-Path $PSScriptRoot 'release-android.ps1') -ValidateOnly -SimulateMissingKeystore *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 10) 'A missing configured Android keystore must exit with code 10.'
Assert-Condition ($missingKeystoreOutput -match 'keystore is missing') 'A missing Android keystore needs recovery-focused remediation.'
Assert-Condition ($missingKeystoreOutput -notmatch '(?i)storePassword|keyPassword|super-secret') 'Missing-keystore remediation must not expose signing values.'

$testAllScript = Get-Content -Raw (Join-Path $PSScriptRoot 'test-all.ps1')
Assert-Condition ($testAllScript -match "preflight\.ps1'\) -Components Go, Node\s") 'test-all must run Go and frontend checks before Android without requiring a running Docker daemon.'
Assert-Condition ($testAllScript -notmatch 'docker compose|deploy/docker-compose') 'The local Funnel gate must not require the removed public Compose relay deployment.'
Assert-Condition ($testAllScript -match '\$androidPrerequisiteExit') 'test-all must preserve a nonzero Android prerequisite exit after printing remediation.'

Write-Host 'Operations script checks passed.'
exit 0
