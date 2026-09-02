[CmdletBinding()]
param(
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')]
    [string]$ReleaseVersion,
    [string[]]$Components = @(),
    [switch]$Publish
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'operations-common.ps1')

function Invoke-MobileEgressNativeResult {
    param(
        [Parameter(Mandatory)]
        [string]$FilePath,
        [string[]]$Arguments = @()
    )

    $hadNativePreference = Test-Path Variable:PSNativeCommandUseErrorActionPreference
    if ($hadNativePreference) {
        $originalNativePreference = $PSNativeCommandUseErrorActionPreference
        $PSNativeCommandUseErrorActionPreference = $false
    }
    try {
        $output = @(& $FilePath @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    } finally {
        if ($hadNativePreference) {
            $PSNativeCommandUseErrorActionPreference = $originalNativePreference
        }
    }
    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = ($output | Out-String).Trim()
    }
}

function Invoke-MobileEgressNativeCommand {
    param(
        [Parameter(Mandatory)]
        [string]$FilePath,
        [string[]]$Arguments = @(),
        [Parameter(Mandatory)]
        [string]$Description
    )

    $result = Invoke-MobileEgressNativeResult -FilePath $FilePath -Arguments $Arguments
    if ($result.ExitCode -ne 0) {
        throw "$Description failed.`n$($result.Output)"
    }
    return $result.Output
}

function Get-MobileEgressPowerShellExecutable {
    $processPath = (Get-Process -Id $PID).Path
    if ([string]::IsNullOrWhiteSpace($processPath) -or -not (Test-Path -LiteralPath $processPath -PathType Leaf)) {
        throw 'Unable to resolve the current PowerShell executable.'
    }
    return $processPath
}

function Invoke-MobileEgressPowerShellScript {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [string[]]$Arguments = @()
    )

    $engine = Get-MobileEgressPowerShellExecutable
    return Invoke-MobileEgressNativeResult -FilePath $engine -Arguments (@('-NoProfile', '-File', $Path) + $Arguments)
}

function Invoke-MobileEgressRequiredPowerShellScript {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [string[]]$Arguments = @(),
        [Parameter(Mandatory)]
        [string]$Description
    )

    $result = Invoke-MobileEgressPowerShellScript -Path $Path -Arguments $Arguments
    if (-not [string]::IsNullOrWhiteSpace($result.Output)) {
        Write-Host $result.Output
    }
    if ($result.ExitCode -ne 0) {
        throw "$Description failed with exit code $($result.ExitCode)."
    }
}

function Invoke-MobileEgressComponentGate {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [Parameter(Mandatory)]
        [ValidateSet('Windows', 'Android')]
        [string[]]$Components
    )

    $global:LASTEXITCODE = 0
    & $Path -Components $Components
    if ($LASTEXITCODE -ne 0) {
        throw "Component repository gate failed with exit code $LASTEXITCODE."
    }
}

function Import-MobileEgressReleaseEnvironment {
    param(
        [scriptblock]$ReadPersistentValue = {
            param($Name, $Scope)
            $target = if ($Scope -eq 'User') {
                [System.EnvironmentVariableTarget]::User
            } else {
                [System.EnvironmentVariableTarget]::Machine
            }
            return [System.Environment]::GetEnvironmentVariable($Name, $target)
        }
    )

    foreach ($name in @('JAVA_HOME', 'ANDROID_HOME', 'ANDROID_SDK_ROOT')) {
        $value = & $ReadPersistentValue $name 'User'
        if ([string]::IsNullOrWhiteSpace($value)) {
            $value = & $ReadPersistentValue $name 'Machine'
        }
        if (-not [string]::IsNullOrWhiteSpace($value)) {
            Set-Item -LiteralPath "Env:$name" -Value $value
        }
    }
}

function Invoke-MobileEgressAndroidRelease {
    param(
        [Parameter(Mandatory)]
        [scriptblock]$InvokeRelease,
        [Parameter(Mandatory)]
        [scriptblock]$ShowDaemonStatus,
        [Parameter(Mandatory)]
        [scriptblock]$StopDaemons
    )

    $first = & $InvokeRelease
    if ($first.ExitCode -eq 0) {
        return
    }
    $knownLock = $first.Output -match 'Unable to delete directory' -and $first.Output -match '(?i)lint-cache'
    if (-not $knownLock) {
        throw "Android release failed with exit code $($first.ExitCode)."
    }

    Write-Host 'The known Gradle lint-cache lock was detected. Stopping only Gradle daemons and retrying once.'
    & $ShowDaemonStatus
    & $StopDaemons
    $second = & $InvokeRelease
    if ($second.ExitCode -ne 0) {
        throw "Android release failed after the one permitted Gradle lock retry with exit code $($second.ExitCode)."
    }
}

function Wait-MobileEgressDraftAsset {
    param(
        [Parameter(Mandatory)]
        [pscustomobject]$Artifact,
        [Parameter(Mandatory)]
        [scriptblock]$GetAssets,
        [int]$PollIntervalMilliseconds = 2000,
        [int]$TimeoutSeconds = 600
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ($true) {
        $assets = @(& $GetAssets)
        $matches = @($assets | Where-Object { $_.name -ceq $Artifact.Name })
        if ($matches.Count -gt 1) {
            throw "GitHub draft contains duplicate asset $($Artifact.Name)."
        }
        if ($matches.Count -eq 1) {
            $remote = $matches[0]
            if (-not [string]::IsNullOrWhiteSpace($remote.digest) -and $remote.digest -cne $Artifact.Digest) {
                throw "GitHub draft asset $($Artifact.Name) has a different digest; it will not be overwritten."
            }
            if ($remote.state -eq 'uploaded' -and $remote.digest -ceq $Artifact.Digest) {
                return
            }
        }
        if ([DateTime]::UtcNow -ge $deadline) {
            throw "GitHub did not confirm the uploaded digest for $($Artifact.Name) within $TimeoutSeconds seconds. The draft remains unpublished."
        }
        if ($PollIntervalMilliseconds -gt 0) {
            Start-Sleep -Milliseconds $PollIntervalMilliseconds
        }
    }
}

function Sync-MobileEgressDraftAssets {
    param(
        [Parameter(Mandatory)]
        [object[]]$Artifacts,
        [Parameter(Mandatory)]
        [scriptblock]$GetAssets,
        [Parameter(Mandatory)]
        [scriptblock]$UploadAsset,
        [int]$PollIntervalMilliseconds = 2000,
        [int]$TimeoutSeconds = 600
    )

    $expectedNames = @($Artifacts | ForEach-Object { $_.Name })
    $initialAssets = @(& $GetAssets)
    $unexpected = @($initialAssets | Where-Object { $expectedNames -cnotcontains $_.name })
    if ($unexpected.Count -ne 0) {
        throw "GitHub draft contains unexpected assets: $($unexpected.name -join ', '). The draft remains unpublished."
    }

    foreach ($artifact in $Artifacts) {
        $currentAssets = @(& $GetAssets)
        $current = @($currentAssets | Where-Object { $_.name -ceq $artifact.Name })
        if ($current.Count -gt 1) {
            throw "GitHub draft contains duplicate asset $($artifact.Name)."
        }
        if ($current.Count -eq 0) {
            Write-Host "Uploading $($artifact.Name)..."
            & $UploadAsset $artifact
        }
        Wait-MobileEgressDraftAsset `
            -Artifact $artifact `
            -GetAssets $GetAssets `
            -PollIntervalMilliseconds $PollIntervalMilliseconds `
            -TimeoutSeconds $TimeoutSeconds
        Write-Host "Verified GitHub digest for $($artifact.Name)."
    }

    $finalAssets = @(& $GetAssets)
    if ($finalAssets.Count -ne $Artifacts.Count) {
        throw 'GitHub draft does not contain exactly the required release assets.'
    }
    foreach ($artifact in $Artifacts) {
        $remote = @($finalAssets | Where-Object { $_.name -ceq $artifact.Name })
        if ($remote.Count -ne 1 -or $remote[0].state -ne 'uploaded' -or $remote[0].digest -cne $artifact.Digest) {
            throw "GitHub draft asset verification failed for $($artifact.Name)."
        }
    }
}

function Ensure-MobileEgressLocalReleaseTag {
    param(
        [Parameter(Mandatory)]
        [string]$Tag,
        [Parameter(Mandatory)]
        [string]$HeadCommit,
        [Parameter(Mandatory)]
        [scriptblock]$GetTagCommit,
        [Parameter(Mandatory)]
        [scriptblock]$CreateTag
    )

    $existingCommit = & $GetTagCommit
    if (-not [string]::IsNullOrWhiteSpace($existingCommit)) {
        if ($existingCommit -ne $HeadCommit) {
            throw "$Tag already identifies a different commit and must never be moved."
        }
        return $existingCommit
    }
    & $CreateTag $Tag
    return $HeadCommit
}

function Assert-MobileEgressAndroidReleaseVersion {
    param(
        [Parameter(Mandatory)]
        [string]$BuildFileContent,
        [Parameter(Mandatory)]
        [string]$ExpectedVersion,
        [int]$MaximumPriorVersionCode = 0
    )

    $nameMatch = [regex]::Match($BuildFileContent, '(?m)^\s*versionName\s*=\s*(?:"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))\s*$')
    $codeMatch = [regex]::Match($BuildFileContent, '(?m)^\s*versionCode\s*=\s*([0-9]+)\s*$')
    $actualVersion = if ($nameMatch.Success -and -not [string]::IsNullOrWhiteSpace($nameMatch.Groups[1].Value)) {
        $nameMatch.Groups[1].Value
    } elseif ($nameMatch.Success) {
        $constantName = [regex]::Escape($nameMatch.Groups[2].Value)
        $constantMatch = [regex]::Match($BuildFileContent, "(?m)^\s*val\s+$constantName\s*=\s*`"([^`"]+)`"\s*$")
        if ($constantMatch.Success) { $constantMatch.Groups[1].Value } else { '' }
    } else {
        ''
    }
    if ($actualVersion -ne $ExpectedVersion) {
        throw "android/app/build.gradle.kts versionName must equal $ExpectedVersion before release."
    }
    if (-not $codeMatch.Success) {
        throw 'android/app/build.gradle.kts versionCode is missing or malformed.'
    }
    $versionCode = [int]$codeMatch.Groups[1].Value
    if ($versionCode -le $MaximumPriorVersionCode) {
        throw "Android versionCode must be greater than $MaximumPriorVersionCode before release."
    }
}

function Get-MobileEgressMaximumTaggedAndroidVersionCode {
    param(
        [Parameter(Mandatory)]
        [string]$RepositoryRoot,
        [Parameter(Mandatory)]
        [string]$ExcludedTag
    )

    $tagOutput = Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'tag', '--list', 'v[0-9]*') -Description 'Git release tag lookup'
    $maximum = 0
    foreach ($existingTag in @($tagOutput -split "`r?`n" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) -and $_ -ne $ExcludedTag })) {
        $result = Invoke-MobileEgressNativeResult -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'show', "${existingTag}:android/app/build.gradle.kts")
        if ($result.ExitCode -ne 0) {
            throw "Unable to inspect Android versionCode from $existingTag."
        }
        $match = [regex]::Match($result.Output, '(?m)^\s*versionCode\s*=\s*([0-9]+)\s*$')
        if (-not $match.Success) {
            throw "Android versionCode is missing or malformed in $existingTag."
        }
        $code = [int]$match.Groups[1].Value
        if ($code -gt $maximum) {
            $maximum = $code
        }
    }
    return $maximum
}

function Get-MobileEgressGitHubRelease {
    param(
        [Parameter(Mandatory)]
        [string]$Tag
    )

    $result = Invoke-MobileEgressNativeResult -FilePath 'gh' -Arguments @(
        'release', 'view', $Tag,
        '--repo', 'cbjjensen/mobile-egress',
        '--json', 'tagName,isDraft,isPrerelease,url,body,assets'
    )
    if ($result.ExitCode -ne 0) {
        if ($result.Output -match '(?i)release not found|not found') {
            return $null
        }
        throw "GitHub release lookup failed.`n$($result.Output)"
    }
    return $result.Output | ConvertFrom-Json
}

function Get-MobileEgressGitHubReleases {
    param(
        [scriptblock]$ListReleases = {
            $result = Invoke-MobileEgressNativeResult -FilePath 'gh' -Arguments @(
                'release', 'list',
                '--repo', 'cbjjensen/mobile-egress',
                '--limit', '100',
                '--json', 'tagName,isDraft,isPrerelease'
            )
            if ($result.ExitCode -ne 0) {
                throw "GitHub release list failed.`n$($result.Output)"
            }
            return @($result.Output | ConvertFrom-Json)
        },
        [scriptblock]$ViewRelease = {
            param($Tag)
            return Get-MobileEgressGitHubRelease -Tag $Tag
        }
    )

    foreach ($release in @(& $ListReleases | Where-Object { -not $_.isDraft })) {
        & $ViewRelease $release.tagName
    }
}

function Set-MobileEgressGitHubReleaseBody {
    param(
        [Parameter(Mandatory)]
        [string]$Tag,
        [AllowEmptyString()]
        [string]$Body
    )

    $notesFile = New-TemporaryFile
    try {
        Set-Content -LiteralPath $notesFile.FullName -Value $Body -Encoding UTF8
        $null = Invoke-MobileEgressNativeCommand -FilePath 'gh' -Arguments @(
            'release', 'edit', $Tag,
            '--repo', 'cbjjensen/mobile-egress',
            '--notes-file', $notesFile.FullName
        ) -Description 'Updating GitHub release download links'
    } finally {
        Remove-Item -LiteralPath $notesFile.FullName -Force -ErrorAction SilentlyContinue
    }
}

function Assert-MobileEgressReleaseZipMatchesSources {
    param(
        [Parameter(Mandatory)]
        [string]$ZipPath,
        [Parameter(Mandatory)]
        [System.Collections.IDictionary]$ExpectedSources
    )

    if (-not (Test-Path -LiteralPath $ZipPath -PathType Leaf)) {
        throw "Release ZIP is missing: $ZipPath"
    }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($ZipPath)
    try {
        $fileEntries = @($archive.Entries | Where-Object { -not [string]::IsNullOrWhiteSpace($_.Name) })
        if ($fileEntries.Count -ne $ExpectedSources.Count) {
            throw 'Release ZIP does not contain exactly the expected files.'
        }
        foreach ($entry in $ExpectedSources.GetEnumerator()) {
            $matches = @($fileEntries | Where-Object { $_.FullName -ceq $entry.Key })
            if ($matches.Count -ne 1) {
                throw "Release ZIP does not contain exactly one $($entry.Key)."
            }
            if (-not (Test-Path -LiteralPath $entry.Value -PathType Leaf)) {
                throw "Verified release source is missing: $($entry.Value)"
            }
            $sourceHash = (Get-FileHash -LiteralPath $entry.Value -Algorithm SHA256).Hash.ToLowerInvariant()
            $stream = $matches[0].Open()
            $hasher = [System.Security.Cryptography.SHA256]::Create()
            try {
                $zipHash = ([System.BitConverter]::ToString($hasher.ComputeHash($stream))).Replace('-', '').ToLowerInvariant()
            } finally {
                $hasher.Dispose()
                $stream.Dispose()
            }
            if ($zipHash -cne $sourceHash) {
                throw "Release ZIP entry $($entry.Key) does not match its verified source."
            }
        }
    } finally {
        $archive.Dispose()
    }
}

function Resolve-MobileEgressReleaseComponents {
    param([AllowEmptyCollection()][string[]]$Components = @())

    if ($null -eq $Components -or $Components.Count -eq 0) {
        return @('Desktop', 'Android')
    }

    if ($Components -contains 'macOS') {
        throw 'macOS cannot be released separately. Select Desktop to release Windows and macOS together.'
    }
    $unsupported = @($Components | Where-Object { $_ -notin @('Desktop', 'Windows', 'Android') } | Select-Object -Unique)
    if ($unsupported.Count -ne 0) {
        throw "Unsupported release component: $($unsupported -join ', '). Supported components are Desktop, Windows, and Android."
    }
    if (($Components -contains 'Desktop') -and ($Components -contains 'Windows')) {
        throw 'Desktop and Windows cannot be selected together because Desktop already contains Windows.'
    }

    $resolved = [System.Collections.Generic.List[string]]::new()
    foreach ($component in @('Desktop', 'Windows', 'Android')) {
        if ($Components -contains $component) {
            $resolved.Add($component)
        }
    }
    return @($resolved)
}

function Assert-MobileEgressApprovedReleaseScope {
    param(
        [Parameter(Mandatory)]
        [string]$Version,
        [Parameter(Mandatory)]
        [string[]]$Components
    )

    $resolved = @(Resolve-MobileEgressReleaseComponents -Components $Components)
    $scope = $resolved -join ','
    if ($Version -eq '1.1.0') {
        if ($scope -cne 'Windows,Android') {
            throw 'The immutable v1.1.0 interim release must contain exactly Windows and Android.'
        }
        return
    }
    if ($resolved -contains 'Windows') {
        throw 'The uncoupled Windows selector is only approved for v1.1.0 with Android; use Desktop for later releases.'
    }
}

function Get-MobileEgressReleaseGateComponents {
    param([Parameter(Mandatory)][string[]]$Components)

    $resolved = @(Resolve-MobileEgressReleaseComponents -Components $Components)
    $gateComponents = [System.Collections.Generic.List[string]]::new()
    if (($resolved -contains 'Desktop') -or ($resolved -contains 'Windows')) { $gateComponents.Add('Windows') }
    if ($resolved -contains 'Android') { $gateComponents.Add('Android') }
    return @($gateComponents)
}

function Get-MobileEgressAndroidApkName {
    param(
        [Parameter(Mandatory)]
        [string]$Version
    )

    return "zfnf-mobile-egress-android-$Version.apk"
}

function Get-MobileEgressReleaseArtifactDefinitions {
    param(
        [Parameter(Mandatory)]
        [string]$RepositoryRoot,
        [Parameter(Mandatory)]
        [string]$Version,
        [Parameter(Mandatory)]
        [string[]]$Components
    )

    $resolvedComponents = @(Resolve-MobileEgressReleaseComponents -Components $Components)
    if (($resolvedComponents -contains 'Desktop') -or ($resolvedComponents -contains 'Windows')) {
        [pscustomobject]@{
            Name = "mobile-egress-windows-$Version.zip"
            Path = Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-windows-$Version.zip"
        }
        [pscustomobject]@{
            Name = 'mobile-egress-client.exe'
            Path = Join-Path $RepositoryRoot 'windows-client\build\bin\mobile-egress-client.exe'
        }
    }
    if ($resolvedComponents -contains 'Desktop') {
        [pscustomobject]@{
            Name = "mobile-egress-macos-$Version-arm64.pkg"
            Path = Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-macos-$Version-arm64.pkg"
        }
    }
    if ($resolvedComponents -contains 'Android') {
        $androidApkName = Get-MobileEgressAndroidApkName -Version $Version
        [pscustomobject]@{
            Name = $androidApkName
            Path = Join-Path $RepositoryRoot "android\app\build\outputs\apk\release\$androidApkName"
        }
    }
}

function Get-MobileEgressReleaseDownloadItemDefinitions {
    param(
        [Parameter(Mandatory)]
        [string]$Version
    )

    return @(
        [pscustomobject]@{
            Key = 'windows'
            Label = 'Windows controller bundle'
            CurrentName = "mobile-egress-windows-$Version.zip"
        },
        [pscustomobject]@{
            Key = 'client'
            Label = 'EC2 Client'
            CurrentName = 'mobile-egress-client.exe'
        },
        [pscustomobject]@{
            Key = 'macos'
            Label = 'macOS controller PKG (Apple Silicon)'
            CurrentName = "mobile-egress-macos-$Version-arm64.pkg"
        },
        [pscustomobject]@{
            Key = 'android'
            Label = 'Android agent APK'
            CurrentName = Get-MobileEgressAndroidApkName -Version $Version
        }
    )
}

function Test-MobileEgressReleaseDownloadAssetName {
    param(
        [Parameter(Mandatory)]
        [string]$Key,
        [Parameter(Mandatory)]
        [string]$Name
    )

    switch ($Key) {
        'windows' { return $Name -match '^mobile-egress-windows-[0-9]+\.[0-9]+\.[0-9]+\.zip$' }
        'client' { return $Name -ceq 'mobile-egress-client.exe' }
        'macos' { return $Name -match '^mobile-egress-macos-[0-9]+\.[0-9]+\.[0-9]+-arm64\.pkg$' }
        'android' { return $Name -match '^zfnf-mobile-egress-android-[0-9]+\.[0-9]+\.[0-9]+\.apk$' -or $Name -ceq 'app-release.apk' }
        default { throw "Unsupported download item: $Key" }
    }
}

function New-MobileEgressReleaseDownloadUrl {
    param(
        [Parameter(Mandatory)]
        [string]$Tag,
        [Parameter(Mandatory)]
        [string]$Name
    )

    $escapedName = [System.Uri]::EscapeDataString($Name)
    return "https://github.com/cbjjensen/mobile-egress/releases/download/$Tag/$escapedName"
}

function Resolve-MobileEgressReleaseDownloadLinks {
    param(
        [Parameter(Mandatory)]
        [string]$CurrentTag,
        [Parameter(Mandatory)]
        [string]$Version,
        [Parameter(Mandatory)]
        [object[]]$ReleasedArtifacts,
        [AllowEmptyCollection()]
        [object[]]$PublishedReleases = @()
    )

    $releasedNames = @($ReleasedArtifacts | ForEach-Object { $_.Name })
    $currentWindowsReleased = @($releasedNames | Where-Object { Test-MobileEgressReleaseDownloadAssetName -Key 'windows' -Name $_ }).Count -gt 0
    foreach ($item in @(Get-MobileEgressReleaseDownloadItemDefinitions -Version $Version)) {
        $currentMatch = @($releasedNames | Where-Object { Test-MobileEgressReleaseDownloadAssetName -Key $item.Key -Name $_ } | Select-Object -First 1)
        if ($currentMatch.Count -ne 0) {
            [pscustomobject]@{
                Key = $item.Key
                Label = $item.Label
                Tag = $CurrentTag
                Name = $currentMatch[0]
                Url = New-MobileEgressReleaseDownloadUrl -Tag $CurrentTag -Name $currentMatch[0]
                UnavailableReason = ''
            }
            continue
        }

        $fallback = $null
        foreach ($release in @($PublishedReleases | Where-Object { -not $_.isDraft })) {
            foreach ($asset in @($release.assets)) {
                if (Test-MobileEgressReleaseDownloadAssetName -Key $item.Key -Name $asset.name) {
                    $fallback = [pscustomobject]@{
                        Tag = $release.tagName
                        Name = $asset.name
                    }
                    break
                }
            }
            if ($null -ne $fallback) {
                break
            }
        }

        [pscustomobject]@{
            Key = $item.Key
            Label = $item.Label
            Tag = if ($null -ne $fallback) { $fallback.Tag } else { '' }
            Name = if ($null -ne $fallback) { $fallback.Name } else { '' }
            Url = if ($null -ne $fallback) { New-MobileEgressReleaseDownloadUrl -Tag $fallback.Tag -Name $fallback.Name } else { '' }
            UnavailableReason = if ($null -eq $fallback -and $item.Key -eq 'macos' -and $currentWindowsReleased) {
                'Deferred to a later release pending Apple Developer Program enrollment'
            } else {
                ''
            }
        }
    }
}

function Format-MobileEgressReleaseDownloadSection {
    param(
        [Parameter(Mandatory)]
        [object[]]$DownloadLinks
    )

    $lines = [System.Collections.Generic.List[string]]::new()
    $lines.Add('## Downloads')
    $lines.Add('')
    foreach ($link in $DownloadLinks) {
        if ([string]::IsNullOrWhiteSpace($link.Url)) {
            $reason = if ([string]::IsNullOrWhiteSpace([string]$link.UnavailableReason)) { 'Not available yet' } else { [string]$link.UnavailableReason }
            $lines.Add("- $($link.Label): $reason")
        } else {
            $lines.Add("- $($link.Label): [$($link.Name)]($($link.Url))")
        }
    }
    return ($lines -join "`n")
}

function Update-MobileEgressReleaseBodyDownloadSection {
    param(
        [AllowEmptyString()]
        [string]$Body = '',
        [Parameter(Mandatory)]
        [string]$DownloadSection
    )

    $startMarker = '<!-- mobile-egress-downloads:start -->'
    $endMarker = '<!-- mobile-egress-downloads:end -->'
    $managedSection = "$startMarker`n$DownloadSection`n$endMarker"
    $pattern = '(?s)<!-- mobile-egress-downloads:start -->.*?<!-- mobile-egress-downloads:end -->'
    if ($Body -match $pattern) {
        $evaluator = [System.Text.RegularExpressions.MatchEvaluator]{ param($Match) $managedSection }
        return [regex]::Replace($Body, $pattern, $evaluator, 1)
    }
    if ([string]::IsNullOrWhiteSpace($Body)) {
        return $managedSection
    }
    return "$($Body.TrimEnd())`n`n$managedSection"
}

function Sync-MobileEgressReleaseDownloadNotes {
    param(
        [Parameter(Mandatory)]
        [string]$CurrentTag,
        [Parameter(Mandatory)]
        [string]$Version,
        [Parameter(Mandatory)]
        [object[]]$ReleasedArtifacts,
        [AllowEmptyString()]
        [string]$CurrentBody = '',
        [AllowEmptyCollection()]
        [object[]]$PublishedReleases = @(),
        [Parameter(Mandatory)]
        [scriptblock]$UpdateReleaseBody
    )

    $downloadLinks = Resolve-MobileEgressReleaseDownloadLinks `
        -CurrentTag $CurrentTag `
        -Version $Version `
        -ReleasedArtifacts $ReleasedArtifacts `
        -PublishedReleases $PublishedReleases
    $downloadSection = Format-MobileEgressReleaseDownloadSection -DownloadLinks $downloadLinks
    $updatedBody = Update-MobileEgressReleaseBodyDownloadSection -Body $CurrentBody -DownloadSection $downloadSection
    & $UpdateReleaseBody $updatedBody
}

function Get-MobileEgressReleaseArtifacts {
    param(
        [Parameter(Mandatory)]
        [string]$RepositoryRoot,
        [Parameter(Mandatory)]
        [string]$Version,
        [Parameter(Mandatory)]
        [string[]]$Components
    )

    $artifacts = foreach ($definition in @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot $RepositoryRoot -Version $Version -Components $Components)) {
        if (-not (Test-Path -LiteralPath $definition.Path -PathType Leaf)) {
            throw "Required release artifact is missing: $($definition.Path)"
        }
        $hash = (Get-FileHash -LiteralPath $definition.Path -Algorithm SHA256).Hash.ToLowerInvariant()
        [pscustomobject]@{
            Name = $definition.Name
            Path = $definition.Path
            Digest = "sha256:$hash"
        }
    }
    return @($artifacts)
}

function Assert-MobileEgressReleaseFreezeRecord {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [Parameter(Mandatory)]
        [string]$Tag,
        [Parameter(Mandatory)]
        [string]$SourceCommit,
        [Parameter(Mandatory)]
        [string[]]$Components,
        [Parameter(Mandatory)]
        [object[]]$Artifacts,
        [switch]$CreateIfMissing
    )

    $expectedArtifacts = @($Artifacts | ForEach-Object {
        [ordered]@{
            name = [string]$_.Name
            digest = [string]$_.Digest
        }
    })
    $expected = [ordered]@{
        schemaVersion = 1
        tag = $Tag
        sourceCommit = $SourceCommit
        components = @($Components)
        artifacts = $expectedArtifacts
    }

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        if (-not $CreateIfMissing) {
            throw "The tagged release freeze record is missing: $Path"
        }
        $directory = Split-Path -Parent $Path
        $null = New-Item -ItemType Directory -Force -Path $directory
        $json = $expected | ConvertTo-Json -Depth 5
        [System.IO.File]::WriteAllText($Path, $json, [System.Text.UTF8Encoding]::new($false))
    }

    try {
        $actual = Get-Content -Raw -LiteralPath $Path | ConvertFrom-Json
    } catch {
        throw "The release freeze record is malformed: $Path"
    }
    $actualComponents = @($actual.components | ForEach-Object { [string]$_ })
    $actualArtifacts = @($actual.artifacts)
    $matches = `
        $actual.schemaVersion -eq 1 -and `
        [string]$actual.tag -ceq $Tag -and `
        [string]$actual.sourceCommit -ceq $SourceCommit -and `
        ($actualComponents -join ',') -ceq (@($Components) -join ',') -and `
        $actualArtifacts.Count -eq $expectedArtifacts.Count
    if ($matches) {
        for ($index = 0; $index -lt $expectedArtifacts.Count; $index++) {
            if (
                [string]$actualArtifacts[$index].name -cne [string]$expectedArtifacts[$index].name -or
                [string]$actualArtifacts[$index].digest -cne [string]$expectedArtifacts[$index].digest
            ) {
                $matches = $false
                break
            }
        }
    }
    if (-not $matches) {
        throw 'The current release evidence does not match the frozen source, component scope, artifact names, and digests.'
    }
}

function Assert-MobileEgressReleaseArtifacts {
    param(
        [Parameter(Mandatory)]
        [string]$RepositoryRoot,
        [Parameter(Mandatory)]
        [string]$Version,
        [Parameter(Mandatory)]
        [string[]]$Components,
        [string]$SourceCommit = ''
    )

    $resolvedComponents = @(Resolve-MobileEgressReleaseComponents -Components $Components)
    $includesDesktop = $resolvedComponents -contains 'Desktop'
    $includesWindows = $includesDesktop -or ($resolvedComponents -contains 'Windows')
    if ($includesWindows) {
        if ($SourceCommit -notmatch '^[0-9a-f]{40}$') {
            throw 'SourceCommit is required to validate Windows release artifacts.'
        }
        $record = Get-Content -Raw -LiteralPath (Join-Path $RepositoryRoot 'windows-signing\release-signing-certificate.txt')
        $thumbprintMatch = [regex]::Match($record, '(?im)^SHA-1 thumbprint:\s*([0-9A-F]{40})\s*$')
        if (-not $thumbprintMatch.Success) {
            throw 'The tracked Windows publisher thumbprint is missing or malformed.'
        }
        $expectedThumbprint = $thumbprintMatch.Groups[1].Value
        $binRoot = Join-Path $RepositoryRoot 'windows-client\build\bin'
        $expectedExecutables = @(
            'mobile-egress-admin.exe',
            'mobile-egress-client.exe',
            'mobile-egress-relay.exe',
            'mobile-egress-windows.exe',
            'MobileEgressSetup.exe'
        )
        foreach ($name in $expectedExecutables) {
            $path = Join-Path $binRoot $name
            if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
                throw "Signed Windows executable is missing: $name"
            }
            $signature = Get-AuthenticodeSignature -LiteralPath $path
            if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $expectedThumbprint -or $null -eq $signature.TimeStamperCertificate) {
                throw "Windows signature validation failed for $name."
            }
        }

        $controllerVersionInfo = (Get-Item -LiteralPath (Join-Path $binRoot 'mobile-egress-windows.exe')).VersionInfo
        if ($controllerVersionInfo.FileVersionRaw -ne [version]"$Version.0") {
            throw 'The signed Windows controller metadata does not match the requested release.'
        }

        $manifestPath = Join-Path $binRoot 'release-manifest.json'
        $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
        $clientPath = Join-Path $binRoot 'mobile-egress-client.exe'
        $clientHash = (Get-FileHash -LiteralPath $clientPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($manifest.version -ne 2 -or $manifest.client.version -ne $Version -or $manifest.client.sha256 -cne $clientHash -or $manifest.client.signerThumbprint -cne $expectedThumbprint.ToLowerInvariant()) {
            throw 'The Windows release manifest does not match the requested release and signed Client.'
        }

        $zipSources = [ordered]@{}
        foreach ($name in $expectedExecutables) {
            $zipSources[$name] = Join-Path $binRoot $name
        }
        $zipSources['mobile-egress-code-signing.cer'] = Join-Path $RepositoryRoot 'windows-signing\mobile-egress-code-signing.cer'
        $zipSources['release-manifest.json'] = $manifestPath
        $zipSources['release-signing-certificate.txt'] = Join-Path $RepositoryRoot 'windows-signing\release-signing-certificate.txt'
        Assert-MobileEgressReleaseZipMatchesSources `
            -ZipPath (Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-windows-$Version.zip") `
            -ExpectedSources $zipSources

    }

    if ($includesDesktop) {
        Invoke-MobileEgressRequiredPowerShellScript `
            -Path (Join-Path $PSScriptRoot 'release-desktop.ps1') `
            -Arguments @('-ReleaseVersion', $Version, '-SourceCommit', $SourceCommit, '-ValidateArtifacts') `
            -Description 'macOS Desktop release verification'
    }

    if ($resolvedComponents -contains 'Android') {
        $sdkRoot = Get-MobileEgressAndroidSdkRoot -RepositoryRoot $RepositoryRoot
        $apksignerDirectory = Get-ChildItem -LiteralPath (Join-Path $sdkRoot 'build-tools') -Directory |
            Where-Object { $_.Name -match '^35(\.|$)' -and (Test-Path -LiteralPath (Join-Path $_.FullName 'apksigner.bat') -PathType Leaf) } |
            Sort-Object Name -Descending |
            Select-Object -First 1
        if ($null -eq $apksignerDirectory) {
            throw 'Android Build-Tools 35 apksigner is unavailable.'
        }
        $apkPath = Join-Path $RepositoryRoot "android\app\build\outputs\apk\release\$(Get-MobileEgressAndroidApkName -Version $Version)"
        $apkVerification = Invoke-MobileEgressNativeResult -FilePath (Join-Path $apksignerDirectory.FullName 'apksigner.bat') -Arguments @('verify', '--verbose', '--print-certs', $apkPath)
        if ($apkVerification.ExitCode -ne 0) {
            throw 'APK signature verification failed.'
        }
        $androidRecord = Get-Content -Raw -LiteralPath (Join-Path $RepositoryRoot 'android\release-signing-certificate.txt')
        $expectedAndroidMatch = [regex]::Match($androidRecord, '(?im)^SHA-256 fingerprint:\s*((?:[0-9A-F]{2}:){31}[0-9A-F]{2})\s*$')
        $actualAndroidMatch = [regex]::Match($apkVerification.Output, '(?im)^Signer #1 certificate SHA-256 digest:\s*([0-9a-f]{64})\s*$')
        if (-not $expectedAndroidMatch.Success -or -not $actualAndroidMatch.Success -or $expectedAndroidMatch.Groups[1].Value.Replace(':', '').ToLowerInvariant() -cne $actualAndroidMatch.Groups[1].Value.ToLowerInvariant()) {
            throw 'The APK signer does not match the tracked Android signing identity.'
        }
    }
}

function Invoke-MobileEgressRelease {
    param(
        [Parameter(Mandatory)]
        [string]$Version,
        [Parameter(Mandatory)]
        [string]$RepositoryRoot,
        [AllowEmptyCollection()]
        [string[]]$Components = @(),
        [switch]$PublishRelease
    )

    if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
        throw 'ReleaseVersion must be a three-part semantic version such as 1.0.4.'
    }
    if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
        throw 'The signed Mobile Egress release workflow must run on the Windows publisher workstation.'
    }

    Import-MobileEgressReleaseEnvironment
    $tag = "v$Version"
    $resolvedComponents = @(Resolve-MobileEgressReleaseComponents -Components $Components)
    Assert-MobileEgressApprovedReleaseScope -Version $Version -Components $resolvedComponents
    $includesDesktop = $resolvedComponents -contains 'Desktop'
    $includesWindows = $includesDesktop -or ($resolvedComponents -contains 'Windows')
    $includesAndroid = $resolvedComponents -contains 'Android'

    $status = Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'status', '--porcelain') -Description 'Git worktree check'
    if (-not [string]::IsNullOrWhiteSpace($status)) {
        throw 'The release requires a clean worktree. Commit the intended source and version changes first.'
    }
    $branch = Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'branch', '--show-current') -Description 'Git branch check'
    if ($branch.Trim() -ne 'main') {
        throw 'The release must run from main.'
    }
    $head = (Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'rev-parse', 'HEAD') -Description 'Git commit lookup').Trim()

    $remoteMain = ''
    if ($PublishRelease) {
        $null = Invoke-MobileEgressNativeCommand -FilePath 'gh' -Arguments @('auth', 'status') -Description 'GitHub authentication'
        $remote = (Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'remote', 'get-url', 'origin') -Description 'Git origin lookup').Trim()
        if ($remote -notmatch '(?i)(?:github\.com[:/])cbjjensen/mobile-egress(?:\.git)?$') {
            throw 'Git origin is not cbjjensen/mobile-egress.'
        }
        $null = Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'fetch', 'origin', 'main', '--tags') -Description 'Git remote refresh'
        $remoteMain = (Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'rev-parse', 'origin/main') -Description 'Remote main lookup').Trim()
        $ancestor = Invoke-MobileEgressNativeResult -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'merge-base', '--is-ancestor', $remoteMain, $head)
        if ($ancestor.ExitCode -ne 0) {
            throw 'Local main does not contain origin/main. Reconcile the branch before release.'
        }
    }

    if ($includesAndroid) {
        $androidBuild = Get-Content -Raw -LiteralPath (Join-Path $RepositoryRoot 'android\app\build.gradle.kts')
        $maximumPriorVersionCode = Get-MobileEgressMaximumTaggedAndroidVersionCode -RepositoryRoot $RepositoryRoot -ExcludedTag $tag
        Assert-MobileEgressAndroidReleaseVersion `
            -BuildFileContent $androidBuild `
            -ExpectedVersion $Version `
            -MaximumPriorVersionCode $maximumPriorVersionCode
    }

    $localTagResult = Invoke-MobileEgressNativeResult -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'rev-list', '-n', '1', $tag)
    $localTagCommit = if ($localTagResult.ExitCode -eq 0) { $localTagResult.Output.Trim() } else { '' }
    if (-not [string]::IsNullOrWhiteSpace($localTagCommit) -and $localTagCommit -ne $head) {
        throw "$tag already identifies a different commit and must never be moved."
    }

    $remoteTagCommit = ''
    if ($PublishRelease) {
        $remoteTagResult = Invoke-MobileEgressNativeResult -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'ls-remote', 'origin', "refs/tags/$tag^{}")
        if ($remoteTagResult.ExitCode -ne 0) {
            throw "Remote tag lookup failed.`n$($remoteTagResult.Output)"
        }
        if (-not [string]::IsNullOrWhiteSpace($remoteTagResult.Output)) {
            $remoteTagCommit = ($remoteTagResult.Output -split "`t")[0]
        }
        if (-not [string]::IsNullOrWhiteSpace($remoteTagCommit) -and $remoteTagCommit -ne $head) {
            throw "$tag already identifies a different remote commit and must never be moved."
        }
    }

    $resumeArtifacts = -not [string]::IsNullOrWhiteSpace($localTagCommit) -or -not [string]::IsNullOrWhiteSpace($remoteTagCommit)
    $artifactDefinitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot $RepositoryRoot -Version $Version -Components $resolvedComponents)
    if ($resumeArtifacts) {
        $missingResumeArtifacts = @($artifactDefinitions | Where-Object { -not (Test-Path -LiteralPath $_.Path -PathType Leaf) })
        if ($missingResumeArtifacts.Count -ne 0) {
            throw "$tag already exists, but the local signed artifacts are unavailable. Restore the exact artifacts; never rebuild or replace a tagged release."
        }
    }

    $windowsSigningScript = Join-Path $PSScriptRoot 'setup-windows-signing.ps1'
    $androidReleaseScript = Join-Path $PSScriptRoot 'release-android.ps1'
    $desktopReleaseScript = Join-Path $PSScriptRoot 'release-desktop.ps1'
    $windowsBuildScript = Join-Path $PSScriptRoot 'build-windows.ps1'
    if ($includesWindows) {
        Invoke-MobileEgressRequiredPowerShellScript -Path $windowsSigningScript -Arguments @('-ValidateOnly') -Description 'Windows publisher validation'
    }
    if ($includesAndroid) {
        Invoke-MobileEgressRequiredPowerShellScript -Path $androidReleaseScript -Arguments @('-ValidateOnly') -Description 'Android signing validation'
    }
    $gateComponents = @(Get-MobileEgressReleaseGateComponents -Components $resolvedComponents)
    Invoke-MobileEgressComponentGate -Path (Join-Path $PSScriptRoot 'test-all.ps1') -Components $gateComponents

    if (-not $resumeArtifacts) {
        if ($includesDesktop) {
            $windowsZip = Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-windows-$Version.zip"
            $macPkg = Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-macos-$Version-arm64.pkg"
            $macRecord = Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-macos-$Version-arm64.verification.json"
            $existingDesktopOutput = @(@($windowsZip, $macPkg, $macRecord) | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1)
            if ($existingDesktopOutput.Count -ne 0) {
                throw "Unsigned release state is ambiguous because $($existingDesktopOutput[0]) already exists. Do not overwrite it automatically."
            }
            Invoke-MobileEgressRequiredPowerShellScript `
                -Path $desktopReleaseScript `
                -Arguments @('-ReleaseVersion', $Version, '-SourceCommit', $head, '-BuildArtifacts') `
                -Description 'Coupled Windows and macOS Desktop release'
        } elseif ($includesWindows) {
            $windowsZip = Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-windows-$Version.zip"
            if (Test-Path -LiteralPath $windowsZip) {
                throw "Unsigned release state is ambiguous because $windowsZip already exists. Do not overwrite it automatically."
            }
            Invoke-MobileEgressRequiredPowerShellScript `
                -Path $windowsBuildScript `
                -Arguments @('-ReleaseVersion', $Version) `
                -Description 'Signed Windows release'
        }

        if ($includesAndroid) {
            $invokeAndroid = {
                $result = Invoke-MobileEgressPowerShellScript -Path $androidReleaseScript
                if (-not [string]::IsNullOrWhiteSpace($result.Output)) {
                    Write-Host $result.Output
                }
                return $result
            }
            Invoke-MobileEgressAndroidRelease `
                -InvokeRelease $invokeAndroid `
                -ShowDaemonStatus {
                    $result = Invoke-MobileEgressNativeResult -FilePath (Join-Path $RepositoryRoot 'android\gradlew.bat') -Arguments @('--status')
                    if (-not [string]::IsNullOrWhiteSpace($result.Output)) { Write-Host $result.Output }
                } `
                -StopDaemons {
                    $result = Invoke-MobileEgressNativeResult -FilePath (Join-Path $RepositoryRoot 'android\gradlew.bat') -Arguments @('--stop')
                    if (-not [string]::IsNullOrWhiteSpace($result.Output)) { Write-Host $result.Output }
                    if ($result.ExitCode -ne 0) { throw 'Stopping Gradle daemons failed.' }
                }
        }
    }

    Assert-MobileEgressReleaseArtifacts -RepositoryRoot $RepositoryRoot -Version $Version -Components $resolvedComponents -SourceCommit $head
    $artifacts = Get-MobileEgressReleaseArtifacts -RepositoryRoot $RepositoryRoot -Version $Version -Components $resolvedComponents
    $freezeRecordPath = Join-Path $RepositoryRoot "windows-client\build\release\mobile-egress-$Version.freeze.json"
    Assert-MobileEgressReleaseFreezeRecord `
        -Path $freezeRecordPath `
        -Tag $tag `
        -SourceCommit $head `
        -Components $resolvedComponents `
        -Artifacts $artifacts `
        -CreateIfMissing:(-not $resumeArtifacts)
    foreach ($artifact in $artifacts) {
        Write-Host "$($artifact.Digest)  $($artifact.Name)"
    }
    $localTagCommit = Ensure-MobileEgressLocalReleaseTag -Tag $tag -HeadCommit $head -GetTagCommit {
        $result = Invoke-MobileEgressNativeResult -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'rev-list', '-n', '1', $tag)
        if ($result.ExitCode -eq 0) { return $result.Output.Trim() }
        return ''
    } -CreateTag {
        param($TagToCreate)
        $null = Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'tag', '-a', $TagToCreate, '-m', "Mobile Egress $Version") -Description 'Creating local release tag'
    }
    if (-not $PublishRelease) {
        Write-Host "Signed $tag artifacts are verified and frozen at $head locally. Re-run with -Publish after explicit publication approval."
        return
    }

    if ($remoteMain -ne $head) {
        $null = Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'push', 'origin', 'main') -Description 'Pushing main'
    }

    $remoteTagResult = Invoke-MobileEgressNativeResult -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'ls-remote', 'origin', "refs/tags/$tag^{}")
    $remoteTagCommit = if ($remoteTagResult.ExitCode -eq 0 -and -not [string]::IsNullOrWhiteSpace($remoteTagResult.Output)) { ($remoteTagResult.Output -split "`t")[0] } else { '' }
    if ([string]::IsNullOrWhiteSpace($remoteTagCommit)) {
        $null = Invoke-MobileEgressNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'push', 'origin', $tag) -Description 'Pushing release tag'
        $remoteTagResult = Invoke-MobileEgressNativeResult -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'ls-remote', 'origin', "refs/tags/$tag^{}")
        $remoteTagCommit = if ($remoteTagResult.ExitCode -eq 0 -and -not [string]::IsNullOrWhiteSpace($remoteTagResult.Output)) { ($remoteTagResult.Output -split "`t")[0] } else { '' }
    }
    if ($localTagCommit -ne $head -or $remoteTagCommit -ne $head) {
        throw 'The checkout, local release tag, and remote release tag do not identify the same commit.'
    }

    $release = Get-MobileEgressGitHubRelease -Tag $tag
    if ($null -eq $release) {
        $null = Invoke-MobileEgressNativeCommand -FilePath 'gh' -Arguments @(
            'release', 'create', $tag,
            '--repo', 'cbjjensen/mobile-egress',
            '--verify-tag', '--draft',
            '--title', "Mobile Egress $Version",
            '--generate-notes'
        ) -Description 'Creating GitHub draft release'
        $release = Get-MobileEgressGitHubRelease -Tag $tag
    }

    $getAssets = {
        $current = Get-MobileEgressGitHubRelease -Tag $tag
        return @($current.assets)
    }
    if (-not $release.isDraft) {
        Sync-MobileEgressDraftAssets -Artifacts $artifacts -GetAssets $getAssets -UploadAsset {
            throw 'Published release assets are immutable and cannot be uploaded or replaced.'
        }
        $publishedReleases = @(Get-MobileEgressGitHubReleases)
        Sync-MobileEgressReleaseDownloadNotes `
            -CurrentTag $tag `
            -Version $Version `
            -ReleasedArtifacts $artifacts `
            -CurrentBody $release.body `
            -PublishedReleases $publishedReleases `
            -UpdateReleaseBody {
                param($Body)
                Set-MobileEgressGitHubReleaseBody -Tag $tag -Body $Body
            }
        Write-Host "$tag is already published with the verified artifacts: $($release.url)"
        return
    }

    Sync-MobileEgressDraftAssets -Artifacts $artifacts -GetAssets $getAssets -UploadAsset {
        param($Artifact)
        $null = Invoke-MobileEgressNativeCommand -FilePath 'gh' -Arguments @(
            'release', 'upload', $tag, $Artifact.Path,
            '--repo', 'cbjjensen/mobile-egress'
        ) -Description "Uploading $($Artifact.Name)"
    }
    $release = Get-MobileEgressGitHubRelease -Tag $tag
    $publishedReleases = @(Get-MobileEgressGitHubReleases)
    Sync-MobileEgressReleaseDownloadNotes `
        -CurrentTag $tag `
        -Version $Version `
        -ReleasedArtifacts $artifacts `
        -CurrentBody $release.body `
        -PublishedReleases $publishedReleases `
        -UpdateReleaseBody {
            param($Body)
            Set-MobileEgressGitHubReleaseBody -Tag $tag -Body $Body
        }
    $null = Invoke-MobileEgressNativeCommand -FilePath 'gh' -Arguments @(
        'release', 'edit', $tag,
        '--repo', 'cbjjensen/mobile-egress',
        '--draft=false', '--prerelease'
    ) -Description 'Publishing GitHub prerelease'

    $published = Get-MobileEgressGitHubRelease -Tag $tag
    if ($published.isDraft -or -not $published.isPrerelease) {
        throw 'GitHub release publication verification failed.'
    }
    Sync-MobileEgressDraftAssets -Artifacts $artifacts -GetAssets $getAssets -UploadAsset {
        throw 'Published release assets are immutable and cannot be uploaded or replaced.'
    }
    Write-Host "Published verified prerelease: $($published.url)"
}

if ($MyInvocation.InvocationName -eq '.') {
    return
}
if ([string]::IsNullOrWhiteSpace($ReleaseVersion)) {
    throw 'ReleaseVersion is required. Example: .\scripts\release-all.ps1 -ReleaseVersion 1.0.4'
}

Invoke-MobileEgressRelease -Version $ReleaseVersion -RepositoryRoot $repositoryRoot -Components $Components -PublishRelease:$Publish
