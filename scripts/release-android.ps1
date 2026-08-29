[CmdletBinding()]
param(
    [switch]$ValidateOnly
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$androidRoot = Join-Path $repositoryRoot 'android'
$propertiesPath = Join-Path $androidRoot 'keystore.properties'

function Get-SigningPropertyNames {
    param([string]$Path)

    $names = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    foreach ($line in Get-Content -LiteralPath $Path) {
        if ($line -match '^\s*(storeFile|storePassword|keyAlias|keyPassword)\s*=\s*(.+)\s*$') {
            [void]$names.Add($matches[1])
        }
    }

    return $names
}

if (-not (Test-Path -LiteralPath $propertiesPath -PathType Leaf)) {
    Write-Host 'Missing Android signing inputs. Create the ignored android\keystore.properties from android\keystore.properties.example and keep the keystore outside this repository.'
    exit 10
}

git -C $repositoryRoot ls-files --error-unmatch -- android/keystore.properties *> $null
if ($LASTEXITCODE -eq 0) {
    Write-Host 'Refusing to release: android\keystore.properties is tracked by Git. Remove it from version control before retrying.'
    exit 11
}

git -C $repositoryRoot check-ignore -q -- android/keystore.properties
if ($LASTEXITCODE -ne 0) {
    Write-Host 'Refusing to release: android\keystore.properties must remain ignored by Git.'
    exit 11
}

$signingPropertyNames = Get-SigningPropertyNames -Path $propertiesPath
$requiredSigningProperties = @('storeFile', 'storePassword', 'keyAlias', 'keyPassword')
$missingSigningProperties = $requiredSigningProperties | Where-Object { -not $signingPropertyNames.Contains($_) }
if ($missingSigningProperties.Count -gt 0) {
    Write-Host 'Android signing inputs are incomplete. Complete the ignored local signing template; values are never printed.'
    exit 10
}

if ($ValidateOnly) {
    Write-Host 'Android signing inputs are present, untracked, and not displayed.'
    exit 0
}

& (Join-Path $PSScriptRoot 'preflight.ps1') -Components Android
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$sdkRoot = if (-not [string]::IsNullOrWhiteSpace($env:ANDROID_HOME)) { $env:ANDROID_HOME } else { $env:ANDROID_SDK_ROOT }
if ([string]::IsNullOrWhiteSpace($sdkRoot)) {
    Write-Host 'Android SDK location is unavailable after validation. Set ANDROID_HOME before running the release command.'
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

    & (Join-Path $apksigner 'apksigner.bat') verify --verbose .\app\build\outputs\apk\release\app-release.apk
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}

Write-Host 'Signed Android release APK built and verified without printing signing values.'
