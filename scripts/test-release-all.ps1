$ErrorActionPreference = 'Stop'

function Assert-Condition {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw "Assertion failed: $Message"
    }
}

$releaseScript = Join-Path $PSScriptRoot 'release-all.ps1'
Assert-Condition (Test-Path -LiteralPath $releaseScript -PathType Leaf) 'The deterministic release script must exist.'
. $releaseScript

$allComponents = @(Resolve-MobileEgressReleaseComponents -Components @())
Assert-Condition (($allComponents -join ',') -eq 'Desktop,Android') 'An unspecified component set must release both desktop platforms plus Android.'
$canonicalComponents = @(Resolve-MobileEgressReleaseComponents -Components @('Android', 'Desktop', 'Android'))
Assert-Condition (($canonicalComponents -join ',') -eq 'Desktop,Android') 'Release components must be deduplicated into deterministic order.'
$windowsComponents = @(Resolve-MobileEgressReleaseComponents -Components @('Windows'))
Assert-Condition (($windowsComponents -join ',') -eq 'Windows') 'An explicitly selected Windows release must remain independent from the unavailable macOS package.'
$interimComponents = @(Resolve-MobileEgressReleaseComponents -Components @('Android', 'Windows', 'Android'))
Assert-Condition (($interimComponents -join ',') -eq 'Windows,Android') 'An interim Windows and Android release must be deduplicated into deterministic order.'
Assert-MobileEgressApprovedReleaseScope -Version '1.1.0' -Components $interimComponents
$windowsOnlyInterimScopeRejected = $false
try {
    Assert-MobileEgressApprovedReleaseScope -Version '1.1.0' -Components @('Windows')
} catch {
    $windowsOnlyInterimScopeRejected = $_.Exception.Message -match 'exactly Windows and Android'
}
Assert-Condition $windowsOnlyInterimScopeRejected 'The immutable v1.1.0 scope must remain exactly Windows and Android.'
$narrowInterimScopeRejected = $false
try {
    Assert-MobileEgressApprovedReleaseScope -Version '1.1.0' -Components @('Android')
} catch {
    $narrowInterimScopeRejected = $_.Exception.Message -match 'exactly Windows and Android'
}
Assert-Condition $narrowInterimScopeRejected 'The frozen v1.1.0 release must not resume or publish with a narrowed component set.'
Assert-MobileEgressApprovedReleaseScope -Version '1.1.1' -Components @('Windows')
$hotfixAndroidScopeRejected = $false
try {
    Assert-MobileEgressApprovedReleaseScope -Version '1.1.1' -Components @('Android')
} catch {
    $hotfixAndroidScopeRejected = $_.Exception.Message -match 'exactly Windows'
}
Assert-Condition $hotfixAndroidScopeRejected 'The v1.1.1 hotfix must reject Android-only release scope.'
$hotfixDesktopScopeRejected = $false
try {
    Assert-MobileEgressApprovedReleaseScope -Version '1.1.1' -Components @('Desktop')
} catch {
    $hotfixDesktopScopeRejected = $_.Exception.Message -match 'exactly Windows'
}
Assert-Condition $hotfixDesktopScopeRejected 'The v1.1.1 hotfix must reject coupled Desktop release scope.'
$hotfixWindowsAndroidScopeRejected = $false
try {
    Assert-MobileEgressApprovedReleaseScope -Version '1.1.1' -Components @('Windows', 'Android')
} catch {
    $hotfixWindowsAndroidScopeRejected = $_.Exception.Message -match 'exactly Windows'
}
Assert-Condition $hotfixWindowsAndroidScopeRejected 'The v1.1.1 hotfix must reject Windows and Android release scope.'
$futureWindowsExceptionRejected = $false
try {
    Assert-MobileEgressApprovedReleaseScope -Version '1.1.2' -Components @('Windows')
} catch {
    $futureWindowsExceptionRejected = $_.Exception.Message -match 'paired with Android'
}
Assert-Condition $futureWindowsExceptionRejected 'The uncoupled Windows selector must require Android outside the explicit v1.1.1 hotfix.'
Assert-MobileEgressApprovedReleaseScope -Version '1.1.2' -Components @('Android')
Assert-MobileEgressApprovedReleaseScope -Version '1.1.3' -Components @('Windows', 'Android')
Assert-MobileEgressApprovedReleaseScope -Version '1.1.4' -Components @('Windows', 'Android')
$desktopWindowsConflictRejected = $false
try {
    $null = Resolve-MobileEgressReleaseComponents -Components @('Desktop', 'Windows')
} catch {
    $desktopWindowsConflictRejected = $_.Exception.Message -match 'cannot be selected together'
}
Assert-Condition $desktopWindowsConflictRejected 'Desktop and Windows must not be selected together because Desktop already contains Windows.'
$macosOnlyRejected = $false
try {
    $null = Resolve-MobileEgressReleaseComponents -Components @('macOS')
} catch {
    $macosOnlyRejected = $_.Exception.Message -match 'Desktop'
}
Assert-Condition $macosOnlyRejected 'A macOS-only desktop release must be rejected.'
$invalidComponentRejected = $false
try {
    $null = Resolve-MobileEgressReleaseComponents -Components @('Relay')
} catch {
    $invalidComponentRejected = $_.Exception.Message -match 'Unsupported release component'
}
Assert-Condition $invalidComponentRejected 'Unknown release components must stop before build or publication.'
$fullGateComponents = @(Get-MobileEgressReleaseGateComponents -Components @('Desktop', 'Android'))
Assert-Condition (($fullGateComponents -join ',') -eq 'Windows,Android') 'The coupled Desktop scope must map to the existing Windows gate while Android retains its independent gate.'
$interimGateComponents = @(Get-MobileEgressReleaseGateComponents -Components @('Windows', 'Android'))
Assert-Condition (($interimGateComponents -join ',') -eq 'Windows,Android') 'The interim release must run the Windows and Android gates without resolving macOS prerequisites.'
$androidGateComponents = @(Get-MobileEgressReleaseGateComponents -Components @('Android'))
Assert-Condition (($androidGateComponents -join ',') -eq 'Android') 'An Android-only release must not resolve Windows or macOS build prerequisites.'

$desktopDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Desktop'))
Assert-Condition (($desktopDefinitions.Name -join ',') -eq 'mobile-egress-windows-1.2.3.zip,mobile-egress-client.exe,mobile-egress-macos-1.2.3-arm64.pkg') 'A Desktop release must publish the Windows bundle, EC2 Client, and macOS PKG together.'
$windowsDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Windows'))
Assert-Condition (($windowsDefinitions.Name -join ',') -eq 'mobile-egress-windows-1.2.3.zip,mobile-egress-client.exe') 'An interim Windows release must publish only the signed Windows bundle and EC2 Client.'
$hotfixDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.1.1' -Components @('Windows'))
Assert-Condition (($hotfixDefinitions.Name -join ',') -ceq 'mobile-egress-windows-1.1.1.zip,mobile-egress-client.exe') 'The v1.1.1 Windows-only hotfix must contain only the Windows ZIP and EC2 Client.'
$androidDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Android'))
Assert-Condition (($androidDefinitions.Name -join ',') -eq 'zfnf-mobile-egress-android-1.2.3.apk') 'An Android release must publish only the versioned ZFNF APK.'
$interimDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Windows', 'Android'))
Assert-Condition (($interimDefinitions.Name -join ',') -eq 'mobile-egress-windows-1.2.3.zip,mobile-egress-client.exe,zfnf-mobile-egress-android-1.2.3.apk') 'The interim release must contain exactly Windows, EC2 Client, and Android artifacts.'
$allDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Desktop', 'Android'))
Assert-Condition (($allDefinitions.Name -join ',') -eq 'mobile-egress-windows-1.2.3.zip,mobile-egress-client.exe,mobile-egress-macos-1.2.3-arm64.pkg,zfnf-mobile-egress-android-1.2.3.apk') 'The full release must contain the coupled Desktop assets followed by Android.'

$desktopDownloadLinks = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v1.2.3' -Version '1.2.3' -ReleasedArtifacts $desktopDefinitions -PublishedReleases @(
    [pscustomobject]@{
        tagName = 'v1.2.2'
        isDraft = $false
        assets = @(
            [pscustomobject]@{ name = 'mobile-egress-windows-1.2.2.zip' },
            [pscustomobject]@{ name = 'mobile-egress-client.exe' },
            [pscustomobject]@{ name = 'mobile-egress-macos-1.2.2-arm64.pkg' },
            [pscustomobject]@{ name = 'zfnf-mobile-egress-android-1.2.2.apk' }
        )
    }
))
Assert-Condition ($desktopDownloadLinks.Count -eq 4) 'Release notes must cover Windows, macOS, Client, and Android downloads even for scoped releases.'
Assert-Condition (($desktopDownloadLinks | Where-Object { $_.Key -eq 'windows' }).Tag -eq 'v1.2.3') 'A scoped Desktop release must link its new Windows bundle from the current tag.'
Assert-Condition (($desktopDownloadLinks | Where-Object { $_.Key -eq 'client' }).Tag -eq 'v1.2.3') 'A scoped Desktop release must link its new EC2 Client from the current tag.'
Assert-Condition (($desktopDownloadLinks | Where-Object { $_.Key -eq 'macos' }).Name -eq 'mobile-egress-macos-1.2.3-arm64.pkg') 'A scoped Desktop release must link its same-version macOS PKG.'
Assert-Condition (($desktopDownloadLinks | Where-Object { $_.Key -eq 'android' }).Tag -eq 'v1.2.2') 'A scoped Desktop release must link the latest published Android APK when Android was not rebuilt.'

$androidDownloadLinks = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v1.2.4' -Version '1.2.4' -ReleasedArtifacts $androidDefinitions -PublishedReleases @(
    [pscustomobject]@{
        tagName = 'v1.2.3'
        isDraft = $false
        assets = @(
            [pscustomobject]@{ name = 'mobile-egress-windows-1.2.3.zip' },
            [pscustomobject]@{ name = 'mobile-egress-client.exe' },
            [pscustomobject]@{ name = 'mobile-egress-macos-1.2.3-arm64.pkg' }
        )
    }
))
Assert-Condition (($androidDownloadLinks | Where-Object { $_.Key -eq 'windows' }).Name -eq 'mobile-egress-windows-1.2.3.zip') 'A scoped Android release must link the latest versioned Windows bundle when Windows was not rebuilt.'
Assert-Condition (($androidDownloadLinks | Where-Object { $_.Key -eq 'client' }).Tag -eq 'v1.2.3') 'A scoped Android release must link the latest EC2 Client when Windows was not rebuilt.'
Assert-Condition (($androidDownloadLinks | Where-Object { $_.Key -eq 'macos' }).Tag -eq 'v1.2.3') 'A scoped Android release must link the same published Desktop release for macOS.'
Assert-Condition (($androidDownloadLinks | Where-Object { $_.Key -eq 'android' }).Tag -eq 'v1.2.4') 'A scoped Android release must link its new APK from the current tag.'

$interimDownloadLinks = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v1.1.0' -Version '1.1.0' -ReleasedArtifacts $interimDefinitions -PublishedReleases @())
Assert-Condition (($interimDownloadLinks | Where-Object { $_.Key -eq 'windows' }).Tag -eq 'v1.1.0') 'The interim release must link its Windows bundle from the current tag.'
Assert-Condition (($interimDownloadLinks | Where-Object { $_.Key -eq 'client' }).Tag -eq 'v1.1.0') 'The interim release must link its EC2 Client from the current tag.'
Assert-Condition ([string]::IsNullOrWhiteSpace(($interimDownloadLinks | Where-Object { $_.Key -eq 'macos' }).Url)) 'The interim release must not manufacture a macOS download.'
Assert-Condition (($interimDownloadLinks | Where-Object { $_.Key -eq 'macos' }).UnavailableReason -match 'Apple Developer Program') 'The interim release must explain why macOS is deferred.'
Assert-Condition (($interimDownloadLinks | Where-Object { $_.Key -eq 'android' }).Tag -eq 'v1.1.0') 'The interim release must link its Android APK from the current tag.'

$futureWindowsAndroidDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.1.4' -Components @('Windows', 'Android'))
$futureWindowsAndroidLinks = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v1.1.4' -Version '1.1.4' -ReleasedArtifacts $futureWindowsAndroidDefinitions -PublishedReleases @())
Assert-Condition (($futureWindowsAndroidLinks | Where-Object { $_.Key -eq 'windows' }).Tag -eq 'v1.1.4') 'A Windows and Android release must link its Windows bundle from the current tag.'
Assert-Condition (($futureWindowsAndroidLinks | Where-Object { $_.Key -eq 'client' }).Tag -eq 'v1.1.4') 'A Windows and Android release must link its EC2 Client from the current tag.'
Assert-Condition ([string]::IsNullOrWhiteSpace(($futureWindowsAndroidLinks | Where-Object { $_.Key -eq 'macos' }).Url)) 'A Windows and Android release must not manufacture a macOS download.'
Assert-Condition (($futureWindowsAndroidLinks | Where-Object { $_.Key -eq 'macos' }).UnavailableReason -match 'not included') 'A Windows and Android release must explain that macOS is outside the release scope.'
Assert-Condition (($futureWindowsAndroidLinks | Where-Object { $_.Key -eq 'android' }).Tag -eq 'v1.1.4') 'A Windows and Android release must link its Android APK from the current tag.'

$hotfixDownloadLinks = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v1.1.1' -Version '1.1.1' -ReleasedArtifacts $hotfixDefinitions -PublishedReleases @(
    [pscustomobject]@{
        tagName = 'v1.2.0'
        isDraft = $false
        assets = @(
            [pscustomobject]@{ name = 'mobile-egress-macos-1.2.0-arm64.pkg' },
            [pscustomobject]@{ name = 'zfnf-mobile-egress-android-1.2.0.apk' }
        )
    },
    [pscustomobject]@{
        tagName = 'v1.1.0'
        isDraft = $false
        assets = @(
            [pscustomobject]@{ name = 'mobile-egress-windows-1.1.0.zip' },
            [pscustomobject]@{ name = 'mobile-egress-client.exe' },
            [pscustomobject]@{ name = 'zfnf-mobile-egress-android-1.1.0.apk' }
        )
    }
))
Assert-Condition (($hotfixDownloadLinks | Where-Object { $_.Key -eq 'windows' }).Url -ceq 'https://github.com/cbjjensen/mobile-egress/releases/download/v1.1.1/mobile-egress-windows-1.1.1.zip') 'The v1.1.1 notes must link the current Windows ZIP.'
Assert-Condition (($hotfixDownloadLinks | Where-Object { $_.Key -eq 'client' }).Url -ceq 'https://github.com/cbjjensen/mobile-egress/releases/download/v1.1.1/mobile-egress-client.exe') 'The v1.1.1 notes must link the current EC2 Client.'
$hotfixAndroidDownload = $hotfixDownloadLinks | Where-Object { $_.Key -eq 'android' }
Assert-Condition ($hotfixAndroidDownload.Tag -ceq 'v1.1.0') 'The v1.1.1 notes must pin the Android fallback tag to v1.1.0 even when another published Android release is listed first.'
Assert-Condition ($hotfixAndroidDownload.Name -ceq 'zfnf-mobile-egress-android-1.1.0.apk') 'The v1.1.1 notes must pin the Android fallback asset to the versioned v1.1.0 APK.'
Assert-Condition ([string]::IsNullOrWhiteSpace(($hotfixDownloadLinks | Where-Object { $_.Key -eq 'macos' }).Url)) 'The v1.1.1 notes must not manufacture a macOS download.'
Assert-Condition (($hotfixDownloadLinks | Where-Object { $_.Key -eq 'macos' }).UnavailableReason -match 'Apple Developer Program') 'The v1.1.1 notes must mark macOS unavailable.'
Assert-Condition ($hotfixAndroidDownload.Url -ceq 'https://github.com/cbjjensen/mobile-egress/releases/download/v1.1.0/zfnf-mobile-egress-android-1.1.0.apk') 'The v1.1.1 notes must fall back to the published v1.1.0 Android APK.'

$downloadSection = Format-MobileEgressReleaseDownloadSection -DownloadLinks $desktopDownloadLinks
Assert-Condition ($downloadSection -match '## Downloads') 'The generated release notes section must be clearly titled.'
Assert-Condition ($downloadSection -match '\[zfnf-mobile-egress-android-1\.2\.2\.apk\]\(https://github\.com/cbjjensen/mobile-egress/releases/download/v1\.2\.2/zfnf-mobile-egress-android-1\.2\.2\.apk\)') 'The download section must render fallback assets as direct GitHub download links.'
$interimDownloadSection = Format-MobileEgressReleaseDownloadSection -DownloadLinks $interimDownloadLinks
Assert-Condition ($interimDownloadSection -match 'macOS controller PKG.*Deferred to a later release pending Apple Developer Program enrollment') 'The interim release notes must state the macOS deferral explicitly.'
$hotfixDownloadSection = Format-MobileEgressReleaseDownloadSection -DownloadLinks $hotfixDownloadLinks
Assert-Condition ($hotfixDownloadSection -match '\[zfnf-mobile-egress-android-1\.1\.0\.apk\]\(https://github\.com/cbjjensen/mobile-egress/releases/download/v1\.1\.0/zfnf-mobile-egress-android-1\.1\.0\.apk\)') 'The v1.1.1 release notes must render the published v1.1.0 Android fallback link.'
Assert-Condition ($hotfixDownloadSection -match 'macOS controller PKG.*Deferred to a later release pending Apple Developer Program enrollment') 'The v1.1.1 release notes must state that macOS is unavailable.'

$updatedBody = Update-MobileEgressReleaseBodyDownloadSection -Body "Generated notes`n`n<!-- mobile-egress-downloads:start -->`nold`n<!-- mobile-egress-downloads:end -->`n" -DownloadSection $downloadSection
Assert-Condition (($updatedBody | Select-String -Pattern '<!-- mobile-egress-downloads:start -->' -AllMatches).Matches.Count -eq 1) 'Updating release notes must replace the managed Downloads section instead of appending duplicates.'
Assert-Condition ($updatedBody -notmatch 'old') 'Updating release notes must remove stale managed download links.'

$viewedReleaseTags = [System.Collections.Generic.List[string]]::new()
$expandedReleases = @(Get-MobileEgressGitHubReleases -ListReleases {
    return @(
        [pscustomobject]@{ tagName = 'v1.2.4'; isDraft = $true; isPrerelease = $true },
        [pscustomobject]@{ tagName = 'v1.2.3'; isDraft = $false; isPrerelease = $true }
    )
} -ViewRelease {
    param($Tag)
    $viewedReleaseTags.Add($Tag)
    return [pscustomobject]@{
        tagName = $Tag
        isDraft = $false
        isPrerelease = $true
        assets = @([pscustomobject]@{ name = 'zfnf-mobile-egress-android-1.2.3.apk' })
    }
})
Assert-Condition (($viewedReleaseTags -join ',') -eq 'v1.2.3') 'GitHub release fallback discovery must view non-draft releases individually because release list does not expose assets.'
Assert-Condition ($expandedReleases[0].assets[0].name -eq 'zfnf-mobile-egress-android-1.2.3.apk') 'Expanded GitHub releases must include asset names for fallback download links.'

$noteUpdates = [System.Collections.Generic.List[string]]::new()
Sync-MobileEgressReleaseDownloadNotes -CurrentTag 'v1.2.4' -Version '1.2.4' -ReleasedArtifacts $androidDefinitions -CurrentBody 'Generated release notes' -PublishedReleases @(
    [pscustomobject]@{
        tagName = 'v1.2.3'
        isDraft = $false
        assets = @(
            [pscustomobject]@{ name = 'mobile-egress-windows-1.2.3.zip' },
            [pscustomobject]@{ name = 'mobile-egress-client.exe' },
            [pscustomobject]@{ name = 'mobile-egress-macos-1.2.3-arm64.pkg' }
        )
    }
) -UpdateReleaseBody {
    param($Body)
    $noteUpdates.Add($Body)
}
Assert-Condition ($noteUpdates.Count -eq 1) 'Release publication must write the managed Downloads section back to GitHub notes.'
Assert-Condition ($noteUpdates[0] -match 'Generated release notes') 'Release download note updates must preserve GitHub-generated notes.'
Assert-Condition ($noteUpdates[0] -match '\[mobile-egress-windows-1\.2\.3\.zip\]\(https://github\.com/cbjjensen/mobile-egress/releases/download/v1\.2\.3/mobile-egress-windows-1\.2\.3\.zip\)') 'Release download note updates must include fallback links before publication.'

$desktopEntryPoint = Join-Path $PSScriptRoot 'release-desktop.ps1'
Assert-Condition (Test-Path -LiteralPath $desktopEntryPoint -PathType Leaf) 'The coupled Desktop release entry point must exist.'
$androidEntryPointContent = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'release-android.ps1')
Assert-Condition ($androidEntryPointContent -match "release-all\.ps1.*-Components\s+'Android'") 'The public Android release entry point must route versioned publication through the deterministic orchestrator with Android scope.'

$gateFixture = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-gate-env-test-" + [guid]::NewGuid().ToString('N') + '.ps1')
$originalGateValue = $env:MOBILE_EGRESS_GATE_ENV_TEST
try {
    Set-Content -LiteralPath $gateFixture -Value 'param([string[]]$Components) $env:MOBILE_EGRESS_GATE_ENV_TEST = ''resolved-by-gate:'' + ($Components -join '','')'
    Invoke-MobileEgressComponentGate -Path $gateFixture -Components 'Windows'
    Assert-Condition ($env:MOBILE_EGRESS_GATE_ENV_TEST -eq 'resolved-by-gate:Windows') 'The component gate must run in-process with its typed component scope so its resolved toolchain environment reaches the signed build.'
} finally {
    $env:MOBILE_EGRESS_GATE_ENV_TEST = $originalGateValue
    Remove-Item -LiteralPath $gateFixture -Force -ErrorAction SilentlyContinue
}

$originalJavaHome = $env:JAVA_HOME
$originalAndroidHome = $env:ANDROID_HOME
$originalAndroidSdkRoot = $env:ANDROID_SDK_ROOT
try {
    $env:JAVA_HOME = 'C:\stale-jdk8'
    $env:ANDROID_HOME = $null
    $env:ANDROID_SDK_ROOT = $null
    $persistentValues = @{
        'User:JAVA_HOME' = 'C:\current-jdk17'
        'User:ANDROID_HOME' = 'C:\current-android-sdk'
        'User:ANDROID_SDK_ROOT' = 'C:\current-android-sdk'
    }
    Import-MobileEgressReleaseEnvironment -ReadPersistentValue {
        param($Name, $Scope)
        return $persistentValues["$Scope`:$Name"]
    }
    Assert-Condition ($env:JAVA_HOME -eq 'C:\current-jdk17') 'Release setup must replace a stale process JAVA_HOME with the persistent user value.'
    Assert-Condition ($env:ANDROID_HOME -eq 'C:\current-android-sdk') 'Release setup must import the persistent Android SDK root.'
    Assert-Condition ($env:ANDROID_SDK_ROOT -eq 'C:\current-android-sdk') 'Release setup must import the persistent Android SDK compatibility root.'
} finally {
    $env:JAVA_HOME = $originalJavaHome
    $env:ANDROID_HOME = $originalAndroidHome
    $env:ANDROID_SDK_ROOT = $originalAndroidSdkRoot
}

$lockEvents = [System.Collections.Generic.List[string]]::new()
$releaseAttempt = 0
Invoke-MobileEgressAndroidRelease -InvokeRelease {
    $lockEvents.Add('release')
    $script:releaseAttempt++
    if ($script:releaseAttempt -eq 1) {
        return [pscustomobject]@{ ExitCode = 1; Output = "Unable to delete directory C:\fixture\android\app\build`nlint-cache is locked" }
    }
    return [pscustomobject]@{ ExitCode = 0; Output = 'signed' }
} -ShowDaemonStatus {
    $lockEvents.Add('status')
} -StopDaemons {
    $lockEvents.Add('stop')
}
Assert-Condition (($lockEvents -join ',') -eq 'release,status,stop,release') 'The known Gradle lint-cache lock must stop Gradle and retry exactly once.'

$genericFailureEvents = [System.Collections.Generic.List[string]]::new()
$genericFailureRejected = $false
try {
    Invoke-MobileEgressAndroidRelease -InvokeRelease {
        $genericFailureEvents.Add('release')
        return [pscustomobject]@{ ExitCode = 7; Output = 'APK signer mismatch' }
    } -ShowDaemonStatus {
        $genericFailureEvents.Add('status')
    } -StopDaemons {
        $genericFailureEvents.Add('stop')
    }
} catch {
    $genericFailureRejected = $_.Exception.Message -match 'Android release failed'
}
Assert-Condition $genericFailureRejected 'A non-lock Android failure must stop the release.'
Assert-Condition (($genericFailureEvents -join ',') -eq 'release') 'A non-lock Android failure must never be retried.'

$artifacts = @(
    [pscustomobject]@{ Name = 'windows.zip'; Path = 'C:\fixture\windows.zip'; Digest = 'sha256:' + ('1' * 64) },
    [pscustomobject]@{ Name = 'client.exe'; Path = 'C:\fixture\client.exe'; Digest = 'sha256:' + ('2' * 64) },
    [pscustomobject]@{ Name = 'agent.apk'; Path = 'C:\fixture\agent.apk'; Digest = 'sha256:' + ('3' * 64) }
)
$freezeFixture = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-freeze-test-" + [guid]::NewGuid().ToString('N'))
try {
    $freezePath = Join-Path $freezeFixture 'v1.1.0.freeze.json'
    Assert-MobileEgressReleaseFreezeRecord `
        -Path $freezePath `
        -Tag 'v1.1.0' `
        -SourceCommit ('a' * 40) `
        -Components @('Windows', 'Android') `
        -Artifacts $artifacts `
        -CreateIfMissing
    Assert-Condition (Test-Path -LiteralPath $freezePath -PathType Leaf) 'A verified untagged build must persist its exact source, scope, names, and digests before the tag is frozen.'
    Assert-MobileEgressReleaseFreezeRecord `
        -Path $freezePath `
        -Tag 'v1.1.0' `
        -SourceCommit ('a' * 40) `
        -Components @('Windows', 'Android') `
        -Artifacts $artifacts

    $narrowFreezeRejected = $false
    try {
        Assert-MobileEgressReleaseFreezeRecord `
            -Path $freezePath `
            -Tag 'v1.1.0' `
            -SourceCommit ('a' * 40) `
            -Components @('Android') `
            -Artifacts @($artifacts[2])
    } catch {
        $narrowFreezeRejected = $_.Exception.Message -match 'does not match'
    }
    Assert-Condition $narrowFreezeRejected 'A tagged release must not resume against a narrower component or artifact set.'

    $changedDigestArtifacts = @($artifacts | ForEach-Object {
        [pscustomobject]@{ Name = $_.Name; Path = $_.Path; Digest = $_.Digest }
    })
    $changedDigestArtifacts[0].Digest = 'sha256:' + ('f' * 64)
    $changedFreezeRejected = $false
    try {
        Assert-MobileEgressReleaseFreezeRecord `
            -Path $freezePath `
            -Tag 'v1.1.0' `
            -SourceCommit ('a' * 40) `
            -Components @('Windows', 'Android') `
            -Artifacts $changedDigestArtifacts
    } catch {
        $changedFreezeRejected = $_.Exception.Message -match 'does not match'
    }
    Assert-Condition $changedFreezeRejected 'A tagged release must not resume after any frozen artifact digest changes.'
} finally {
    if (Test-Path -LiteralPath $freezeFixture) {
        Remove-Item -LiteralPath $freezeFixture -Recurse -Force
    }
}
$remoteAssets = [System.Collections.Generic.List[object]]::new()
$uploadEvents = [System.Collections.Generic.List[string]]::new()
Sync-MobileEgressDraftAssets -Artifacts $artifacts -GetAssets {
    return @($remoteAssets)
} -UploadAsset {
    param($Artifact)
    $uploadEvents.Add($Artifact.Name)
    $remoteAssets.Add([pscustomobject]@{ name = $Artifact.Name; state = 'uploaded'; digest = $Artifact.Digest })
} -PollIntervalMilliseconds 0 -TimeoutSeconds 1
Assert-Condition (($uploadEvents -join ',') -eq 'windows.zip,client.exe,agent.apk') 'Draft assets must upload sequentially in the supplied order.'

$mismatchUploads = [System.Collections.Generic.List[string]]::new()
$mismatchRejected = $false
try {
    Sync-MobileEgressDraftAssets -Artifacts $artifacts -GetAssets {
        return @([pscustomobject]@{ name = 'windows.zip'; state = 'uploaded'; digest = 'sha256:' + ('f' * 64) })
    } -UploadAsset {
        param($Artifact)
        $mismatchUploads.Add($Artifact.Name)
    } -PollIntervalMilliseconds 0 -TimeoutSeconds 1
} catch {
    $mismatchRejected = $_.Exception.Message -match 'different digest'
}
Assert-Condition $mismatchRejected 'A draft asset with the expected name and a different digest must stop the release.'
Assert-Condition ($mismatchUploads.Count -eq 0) 'A mismatched draft asset must never be overwritten.'

$tagEvents = [System.Collections.Generic.List[string]]::new()
$ensuredCommit = Ensure-MobileEgressLocalReleaseTag -Tag 'v1.2.3' -HeadCommit ('a' * 40) -GetTagCommit {
    return ''
} -CreateTag {
    param($Tag)
    $tagEvents.Add("create:$Tag")
}
Assert-Condition ($ensuredCommit -eq ('a' * 40)) 'A verified local build must freeze the current commit for later publication.'
Assert-Condition (($tagEvents -join ',') -eq 'create:v1.2.3') 'A missing local release tag must be created exactly once.'

$tagEvents.Clear()
$ensuredCommit = Ensure-MobileEgressLocalReleaseTag -Tag 'v1.2.3' -HeadCommit ('a' * 40) -GetTagCommit {
    return ('a' * 40)
} -CreateTag {
    param($Tag)
    $tagEvents.Add("create:$Tag")
}
Assert-Condition ($ensuredCommit -eq ('a' * 40)) 'An exact existing local release tag must be reusable.'
Assert-Condition ($tagEvents.Count -eq 0) 'An exact existing local release tag must not be recreated.'

Assert-MobileEgressAndroidReleaseVersion `
    -BuildFileContent "versionCode = 5`nversionName = `"1.0.4`"" `
    -ExpectedVersion '1.0.4' `
    -MaximumPriorVersionCode 4
Assert-MobileEgressAndroidReleaseVersion `
    -BuildFileContent "val androidVersionName = `"1.0.4`"`nversionCode = 5`nversionName = androidVersionName" `
    -ExpectedVersion '1.0.4' `
    -MaximumPriorVersionCode 4
$staleVersionCodeRejected = $false
try {
    Assert-MobileEgressAndroidReleaseVersion `
        -BuildFileContent "versionCode = 4`nversionName = `"1.0.4`"" `
        -ExpectedVersion '1.0.4' `
        -MaximumPriorVersionCode 4
} catch {
    $staleVersionCodeRejected = $_.Exception.Message -match 'greater than 4'
}
Assert-Condition $staleVersionCodeRejected 'A new Android release must increase versionCode beyond every prior tag.'

$trackedAndroidBuildFile = Get-Content -Raw -LiteralPath (Join-Path $repositoryRoot 'android\app\build.gradle.kts')
Assert-MobileEgressAndroidReleaseVersion `
    -BuildFileContent $trackedAndroidBuildFile `
    -ExpectedVersion '1.1.4' `
    -MaximumPriorVersionCode 16
Assert-Condition ($trackedAndroidBuildFile -match '(?m)^\s*versionCode\s*=\s*18\s*$') 'The tracked Android v1.1.4 release must use versionCode 18.'

$zipFixture = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-release-zip-test-" + [guid]::NewGuid().ToString('N'))
try {
    $zipSource = Join-Path $zipFixture 'source'
    $null = New-Item -ItemType Directory -Path $zipSource
    $sourceExecutable = Join-Path $zipSource 'client.exe'
    $sourceManifest = Join-Path $zipSource 'release-manifest.json'
    Set-Content -LiteralPath $sourceExecutable -Value 'signed-client-fixture'
    Set-Content -LiteralPath $sourceManifest -Value '{"version":2}'
    $fixtureZip = Join-Path $zipFixture 'release.zip'
    Compress-Archive -Path (Join-Path $zipSource '*') -DestinationPath $fixtureZip
    $zipSources = [ordered]@{
        'client.exe' = $sourceExecutable
        'release-manifest.json' = $sourceManifest
    }
    Assert-MobileEgressReleaseZipMatchesSources -ZipPath $fixtureZip -ExpectedSources $zipSources

    Set-Content -LiteralPath $sourceManifest -Value '{"version":3}'
    $staleZipRejected = $false
    try {
        Assert-MobileEgressReleaseZipMatchesSources -ZipPath $fixtureZip -ExpectedSources $zipSources
    } catch {
        $staleZipRejected = $_.Exception.Message -match 'does not match'
    }
    Assert-Condition $staleZipRejected 'A release ZIP must be rejected when an archived file differs from its verified source.'
} finally {
    if (Test-Path -LiteralPath $zipFixture) {
        $resolvedFixture = [System.IO.Path]::GetFullPath($zipFixture)
        $resolvedTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
        if ($resolvedFixture.StartsWith($resolvedTemp, [System.StringComparison]::OrdinalIgnoreCase) -and [System.IO.Path]::GetFileName($resolvedFixture).StartsWith('mobile-egress-release-zip-test-', [System.StringComparison]::OrdinalIgnoreCase)) {
            Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
        }
    }
}

Write-Host 'Deterministic release orchestration checks passed.'
exit 0
