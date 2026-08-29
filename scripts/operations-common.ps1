function Get-MobileEgressAndroidSdkRoot {
    param([string]$RepositoryRoot)

    $configuredRoot = $env:ANDROID_HOME
    if ([string]::IsNullOrWhiteSpace($configuredRoot)) {
        $configuredRoot = $env:ANDROID_SDK_ROOT
    }

    if (-not [string]::IsNullOrWhiteSpace($configuredRoot)) {
        return $configuredRoot
    }

    $localProperties = Join-Path $RepositoryRoot 'android\local.properties'
    if (-not (Test-Path -LiteralPath $localProperties -PathType Leaf)) {
        return $null
    }

    $sdkLine = Get-Content -LiteralPath $localProperties | Where-Object { $_ -match '^\s*sdk\.dir\s*=' } | Select-Object -First 1
    if ($null -eq $sdkLine) {
        return $null
    }

    $value = ($sdkLine -replace '^\s*sdk\.dir\s*=', '').Trim()
    return ($value -replace '\\\\', '\' -replace '\\:', ':')
}

function Get-MobileEgressAndroidSdkRemediation {
    return 'Android SDK Platform 35 and Build-Tools 35. Install them with Android Studio SDK Manager, then set ANDROID_HOME, ANDROID_SDK_ROOT, or android/local.properties.'
}
