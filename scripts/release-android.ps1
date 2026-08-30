[CmdletBinding()]
param(
    [switch]$ValidateOnly,
    [switch]$SimulateMissingSigningInputs,
    [switch]$SimulateMissingKeystore
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'operations-common.ps1')
$androidRoot = Join-Path $repositoryRoot 'android'
$propertiesPath = Join-Path $androidRoot 'keystore.properties'
$certificateRecordPath = Join-Path $androidRoot 'release-signing-certificate.txt'

function Get-SigningProperties {
    param([string]$Path)

    $properties = @{}
    foreach ($line in Get-Content -LiteralPath $Path) {
        if ($line -match '^\s*(storeFile|storePassword|keyAlias|keyPassword)\s*=\s*(.+?)\s*$') {
            $properties[$matches[1]] = $matches[2]
        }
    }

    return $properties
}

function Test-RepositoryPathTracked {
    param(
        [Parameter(Mandatory)]
        [string]$RepositoryRoot,
        [Parameter(Mandatory)]
        [string]$RelativePath
    )

    $previousErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $null = & git -C $RepositoryRoot ls-files --error-unmatch -- $RelativePath 2>&1
        $gitExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    if ($gitExitCode -eq 0) {
        return $true
    }
    if ($gitExitCode -eq 1) {
        return $false
    }
    throw "Git ls-files failed while checking Android signing path: $RelativePath"
}

if ($MyInvocation.InvocationName -eq '.') {
    return
}

if ($SimulateMissingSigningInputs) {
    Write-Host 'Missing Android signing inputs. Restore the reusable ignored keystore and android\keystore.properties, or configure them before the first release.'
    exit 10
}

$propertiesFileExists = Test-Path -LiteralPath $propertiesPath -PathType Leaf
if ($propertiesFileExists) {
    if (Test-RepositoryPathTracked -RepositoryRoot $repositoryRoot -RelativePath 'android/keystore.properties') {
        Write-Host 'Refusing to release: android\keystore.properties is tracked by Git. Remove it from version control before retrying.'
        exit 11
    }

    git -C $repositoryRoot check-ignore -q -- android/keystore.properties
    if ($LASTEXITCODE -ne 0) {
        Write-Host 'Refusing to release: android\keystore.properties must remain ignored by Git.'
        exit 11
    }

    $signingProperties = Get-SigningProperties -Path $propertiesPath
} elseif ($SimulateMissingKeystore) {
    $signingProperties = @{
        storeFile = 'mobile-egress-release.jks'
        storePassword = 'test-only'
        keyAlias = 'mobile-egress'
        keyPassword = 'test-only'
    }
} else {
    Write-Host 'Missing Android signing inputs. Restore the reusable ignored keystore and android\keystore.properties, or configure them before the first release.'
    exit 10
}

$requiredSigningProperties = @('storeFile', 'storePassword', 'keyAlias', 'keyPassword')
$missingSigningProperties = $requiredSigningProperties | Where-Object { -not $signingProperties.ContainsKey($_) }
if ($missingSigningProperties.Count -gt 0) {
    Write-Host 'Android signing inputs are incomplete. Complete the ignored local signing template; values are never printed.'
    exit 10
}

$configuredStoreFile = $signingProperties.storeFile
$keystorePath = if ([System.IO.Path]::IsPathRooted($configuredStoreFile)) {
    [System.IO.Path]::GetFullPath($configuredStoreFile)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $androidRoot $configuredStoreFile))
}
if ($SimulateMissingKeystore -or -not (Test-Path -LiteralPath $keystorePath -PathType Leaf)) {
    Write-Host 'The configured Android release keystore is missing. Recover the original ignored keystore and credentials together; do not generate a replacement for an established release.'
    exit 10
}

$repositoryPrefix = [System.IO.Path]::GetFullPath($repositoryRoot).TrimEnd('\') + '\'
if ($keystorePath.StartsWith($repositoryPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    $relativeKeystorePath = [System.IO.Path]::GetRelativePath($repositoryRoot, $keystorePath).Replace('\', '/')
    if (Test-RepositoryPathTracked -RepositoryRoot $repositoryRoot -RelativePath $relativeKeystorePath) {
        Write-Host 'Refusing to release: the Android release keystore is tracked by Git.'
        exit 11
    }
    git -C $repositoryRoot check-ignore -q -- $relativeKeystorePath
    if ($LASTEXITCODE -ne 0) {
        Write-Host 'Refusing to release: a repository-local Android release keystore must remain ignored by Git.'
        exit 11
    }
}

$certificateRecord = if (Test-Path -LiteralPath $certificateRecordPath -PathType Leaf) {
    Get-Content -LiteralPath $certificateRecordPath -Raw
} else {
    ''
}
$expectedFingerprintMatch = [regex]::Match(
    $certificateRecord,
    '(?im)^SHA-256 fingerprint:\s*((?:[0-9A-F]{2}:){31}[0-9A-F]{2})\s*$'
)
if (-not $expectedFingerprintMatch.Success) {
    Write-Host 'Android release certificate identity is missing or malformed. Restore android\release-signing-certificate.txt.'
    exit 10
}
$expectedFingerprint = $expectedFingerprintMatch.Groups[1].Value.Replace(':', '').ToLowerInvariant()

if ($ValidateOnly) {
    Write-Host 'Android signing inputs and public certificate identity are present; secrets remain untracked and undisplayed.'
    exit 0
}

& (Join-Path $PSScriptRoot 'preflight.ps1') -Components Android
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$sdkRoot = Get-MobileEgressAndroidSdkRoot -RepositoryRoot $repositoryRoot
if ([string]::IsNullOrWhiteSpace($sdkRoot)) {
    Write-Host (Get-MobileEgressAndroidSdkRemediation)
    exit 10
}

$apksigner = Get-ChildItem -LiteralPath (Join-Path $sdkRoot 'build-tools') -Directory |
    Where-Object { $_.Name -match '^35(\.|$)' -and (Test-Path -LiteralPath (Join-Path $_.FullName 'apksigner.bat') -PathType Leaf) } |
    Select-Object -First 1 -ExpandProperty FullName
if ([string]::IsNullOrWhiteSpace($apksigner)) {
    Write-Host 'Android Build-Tools 35 apksigner is unavailable. Install Build-Tools 35 and retry.'
    exit 10
}

Push-Location $androidRoot
try {
    .\gradlew.bat clean assembleRelease
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $apksignerCommand = Join-Path $apksigner 'apksigner.bat'
    $releaseApk = '.\app\build\outputs\apk\release\app-release.apk'
    & $apksignerCommand verify --verbose $releaseApk
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $certificateOutput = & $apksignerCommand verify --print-certs $releaseApk 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    $actualFingerprintMatch = [regex]::Match(
        $certificateOutput,
        '(?im)^Signer #1 certificate SHA-256 digest:\s*([0-9a-f]{64})\s*$'
    )
    if (-not $actualFingerprintMatch.Success) {
        Write-Host 'Signed APK verification did not return a SHA-256 certificate digest.'
        exit 11
    }
    if ($actualFingerprintMatch.Groups[1].Value.ToLowerInvariant() -ne $expectedFingerprint) {
        Write-Host 'Refusing to release: the APK signer does not match the recorded Mobile Egress Android release certificate.'
        exit 11
    }
} finally {
    Pop-Location
}

Write-Host 'Signed Android release APK built and matched the recorded certificate without printing signing values.'
