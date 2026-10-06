function Test-MobileEgressBrandedRelease {
    param([Parameter(Mandatory)][string]$Version)
    return [version]($Version -split '-')[0] -ge [version]'2.0.2'
}

function Assert-MobileEgressCurrentReleaseBuildVersion {
    param([Parameter(Mandatory)][string]$Version, [switch]$FrozenResume)
    if (-not $FrozenResume -and -not (Test-MobileEgressBrandedRelease -Version $Version)) {
        throw 'Renamed source requires release version 2.0.2 or later; rebuild only from the original historical source checkout.'
    }
}

function Get-MobileEgressClientWindowsInstallerName {
    param([Parameter(Mandatory)][string]$Version)
    if (Test-MobileEgressBrandedRelease -Version $Version) { return 'InevitableMobileRelaySetup.exe' }
    return 'MobileEgressClientSetup.exe'
}

function Get-MobileEgressClientMacPackageName {
    param([Parameter(Mandatory)][string]$Version)
    if (Test-MobileEgressBrandedRelease -Version $Version) { return "inevitable-mobile-relay-macos-$Version-arm64.pkg" }
    return "mobile-egress-client-macos-$Version-arm64.pkg"
}

function Get-MobileEgressClientMacRecordName {
    param([Parameter(Mandatory)][string]$Version)
    return (Get-MobileEgressClientMacPackageName -Version $Version).Replace('.pkg', '.verification.json')
}

function Get-MobileEgressAndroidApkName {
    param([Parameter(Mandatory)][string]$Version)
    if (Test-MobileEgressBrandedRelease -Version $Version) { return "inevitable-mobile-relay-android-$Version.apk" }
    return "zfnf-mobile-egress-android-$Version.apk"
}

function Get-MobileEgressAndroidVersionName {
    param([Parameter(Mandatory)][string]$BuildFileContent)
    $nameMatch = [regex]::Match($BuildFileContent, '(?m)^\s*versionName\s*=\s*(?:"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))\s*$')
    if (-not $nameMatch.Success) { return '' }
    if (-not [string]::IsNullOrWhiteSpace($nameMatch.Groups[1].Value)) { return $nameMatch.Groups[1].Value }
    $constantName = [regex]::Escape($nameMatch.Groups[2].Value)
    $constantMatch = [regex]::Match($BuildFileContent, "(?m)^\s*val\s+$constantName\s*=\s*`"([^`"]+)`"\s*$")
    if ($constantMatch.Success) { return $constantMatch.Groups[1].Value }
    return ''
}

function Get-MobileEgressReleaseDisplayName {
    param([Parameter(Mandatory)][string]$Version)
    if (Test-MobileEgressBrandedRelease -Version $Version) { return 'Inevitable Mobile Relay' }
    return 'Mobile Egress'
}
