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
Assert-Condition (($allComponents -join ',') -eq 'Windows,Android') 'An unspecified component set must preserve the full release workflow.'
$canonicalComponents = @(Resolve-MobileEgressReleaseComponents -Components @('Android', 'Windows', 'Android'))
Assert-Condition (($canonicalComponents -join ',') -eq 'Windows,Android') 'Release components must be deduplicated into deterministic order.'
$invalidComponentRejected = $false
try {
    $null = Resolve-MobileEgressReleaseComponents -Components @('Relay')
} catch {
    $invalidComponentRejected = $_.Exception.Message -match 'Unsupported release component'
}
Assert-Condition $invalidComponentRejected 'Unknown release components must stop before build or publication.'

$windowsDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Windows'))
Assert-Condition (($windowsDefinitions.Name -join ',') -eq 'mobile-egress-windows-1.2.3.zip,mobile-egress-client.exe') 'A Windows release must publish the controller bundle and its coupled EC2 Client, but not Android.'
$androidDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Android'))
Assert-Condition (($androidDefinitions.Name -join ',') -eq 'app-release.apk') 'An Android release must publish only the signed APK.'
$allDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '1.2.3' -Components @('Windows', 'Android'))
Assert-Condition (($allDefinitions.Name -join ',') -eq 'mobile-egress-windows-1.2.3.zip,mobile-egress-client.exe,app-release.apk') 'The full release must retain the established three-artifact set.'

$windowsEntryPoint = Join-Path $PSScriptRoot 'release-windows.ps1'
Assert-Condition (Test-Path -LiteralPath $windowsEntryPoint -PathType Leaf) 'The fast Windows release entry point must exist.'
$windowsEntryPointContent = Get-Content -Raw -LiteralPath $windowsEntryPoint
Assert-Condition ($windowsEntryPointContent -match "release-all\.ps1.*-Components\s+'Windows'") 'The Windows entry point must route through the deterministic orchestrator with Windows scope.'
$androidEntryPointContent = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'release-android.ps1')
Assert-Condition ($androidEntryPointContent -match "release-all\.ps1.*-Components\s+'Android'") 'The public Android release entry point must route versioned publication through the deterministic orchestrator with Android scope.'

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
