[CmdletBinding()]
param(
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')]
    [string]$ReleaseVersion,
    [switch]$Publish,
    [switch]$BuildArtifacts,
    [switch]$ValidateArtifacts,
    [ValidatePattern('^[0-9a-f]{40}$')]
    [string]$SourceCommit
)

$ErrorActionPreference = 'Stop'
$desktopRepositoryRoot = Split-Path -Parent $PSScriptRoot

function Invoke-MobileEgressDesktopNativeCommand {
    param(
        [Parameter(Mandatory)]
        [string]$FilePath,
        [string[]]$Arguments = @(),
        [Parameter(Mandatory)]
        [string]$Description
    )

    $hadNativePreference = Test-Path Variable:PSNativeCommandUseErrorActionPreference
    if ($hadNativePreference) {
        $originalNativePreference = $PSNativeCommandUseErrorActionPreference
        $PSNativeCommandUseErrorActionPreference = $false
    }
    $originalErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $output = @(& $FilePath @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $originalErrorActionPreference
        if ($hadNativePreference) {
            $PSNativeCommandUseErrorActionPreference = $originalNativePreference
        }
    }
    $text = ($output | Out-String).Trim()
    if ($exitCode -ne 0) {
        throw "$Description failed with exit code $exitCode.`n$text"
    }
    return $text
}

function Invoke-MobileEgressDesktopPowerShellScript {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [string[]]$Arguments = @(),
        [Parameter(Mandatory)]
        [string]$Description
    )

    $engine = (Get-Process -Id $PID).Path
    if ([string]::IsNullOrWhiteSpace($engine) -or -not (Test-Path -LiteralPath $engine -PathType Leaf)) {
        throw 'Unable to resolve the current PowerShell executable.'
    }
    $output = Invoke-MobileEgressDesktopNativeCommand `
        -FilePath $engine `
        -Arguments (@('-NoProfile', '-File', $Path) + $Arguments) `
        -Description $Description
    if (-not [string]::IsNullOrWhiteSpace($output)) {
        Write-Host $output
    }
}

function ConvertTo-MobileEgressPosixLiteral {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Value)

    if ($Value.IndexOf([char]0) -ge 0 -or $Value.Contains("`r") -or $Value.Contains("`n")) {
        throw 'Remote command values must be single-line text.'
    }
    $singleQuoteEscape = "'" + '"' + "'" + '"' + "'"
    return "'" + $Value.Replace("'", $singleQuoteEscape) + "'"
}

function Get-MobileEgressDesktopConfig {
    param([Parameter(Mandatory)][string]$RepositoryRoot)

    $localDirectory = Join-Path $RepositoryRoot '.local\mac-build-server'
    $configPath = Join-Path $localDirectory 'release-desktop.psd1'
    if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) {
        throw "Desktop release configuration is missing: $configPath"
    }
    $data = Import-PowerShellDataFile -LiteralPath $configPath
    $required = @(
        'SshTarget',
        'SshKeyPath',
        'RepositoryPath',
        'TeamID',
        'ApplicationIdentity',
        'InstallerIdentity',
        'NotaryKeychainProfile',
        'NotaryApiKeyPath',
        'NotaryApiKeyID',
        'NotaryApiIssuerID',
        'ProvisioningProfilePath'
    )
    foreach ($name in $required) {
        if (-not $data.ContainsKey($name) -or [string]::IsNullOrWhiteSpace([string]$data[$name])) {
            throw "Desktop release configuration value is missing: $name"
        }
    }
    $unexpected = @($data.Keys | Where-Object { $_ -notin $required })
    if ($unexpected.Count -ne 0) {
        throw "Unsupported Desktop release configuration value: $($unexpected -join ', ')"
    }
    if ([string]$data.SshTarget -notmatch '^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+$') {
        throw 'Desktop release SshTarget must be a user and host name.'
    }
    if ([string]$data.RepositoryPath -notmatch '^/[A-Za-z0-9._/-]+$') {
        throw 'Desktop release RepositoryPath must be an absolute macOS path without spaces.'
    }
    if ([string]$data.TeamID -notmatch '^[A-Z0-9]{10}$') {
        throw 'Desktop release TeamID must contain ten uppercase letters or digits.'
    }
    foreach ($name in @('ApplicationIdentity', 'InstallerIdentity', 'NotaryKeychainProfile', 'NotaryApiKeyPath', 'NotaryApiKeyID', 'NotaryApiIssuerID', 'ProvisioningProfilePath')) {
        $value = [string]$data[$name]
        if ($value.IndexOf([char]0) -ge 0 -or $value.Contains("`r") -or $value.Contains("`n")) {
            throw "Desktop release configuration value must be single-line text: $name"
        }
    }
    if ([string]$data.NotaryApiKeyPath -notmatch '^/') {
        throw 'Desktop release NotaryApiKeyPath must be an absolute macOS path.'
    }
    if ([string]$data.NotaryApiKeyID -notmatch '^[A-Z0-9]{10,}$') {
        throw 'Desktop release NotaryApiKeyID must contain at least ten uppercase letters or digits.'
    }
    if ([string]$data.NotaryApiIssuerID -notmatch '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$') {
        throw 'Desktop release NotaryApiIssuerID must be a UUID.'
    }
    if ([string]$data.ProvisioningProfilePath -notmatch '^/') {
        throw 'Desktop release ProvisioningProfilePath must be an absolute macOS path.'
    }

    $keyPath = [string]$data.SshKeyPath
    if (-not [System.IO.Path]::IsPathRooted($keyPath)) {
        $keyPath = Join-Path $RepositoryRoot $keyPath
    }
    $keyPath = [System.IO.Path]::GetFullPath($keyPath)
    $resolvedLocalDirectory = [System.IO.Path]::GetFullPath($localDirectory).TrimEnd('\')
    if (-not [System.IO.Path]::GetDirectoryName($keyPath).Equals($resolvedLocalDirectory, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw 'Desktop release SshKeyPath must be a file directly under .local\mac-build-server.'
    }
    if (-not (Test-Path -LiteralPath $keyPath -PathType Leaf)) {
        throw "Desktop release SSH key is missing: $keyPath"
    }

    foreach ($relativePath in @('.local/mac-build-server/release-desktop.psd1', '.local/mac-build-server/' + [System.IO.Path]::GetFileName($keyPath))) {
        $null = Invoke-MobileEgressDesktopNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'check-ignore', '-q', '--', $relativePath) -Description "Git ignore check for $relativePath"
        $tracked = Invoke-MobileEgressDesktopNativeCommand -FilePath 'git' -Arguments @('-C', $RepositoryRoot, 'ls-files', '--', $relativePath) -Description "Git tracked-file check for $relativePath"
        if (-not [string]::IsNullOrWhiteSpace($tracked)) {
            throw "Desktop release local configuration must not be tracked: $relativePath"
        }
    }

    return [pscustomobject]@{
        SshTarget = [string]$data.SshTarget
        SshKeyPath = $keyPath
        RepositoryPath = ([string]$data.RepositoryPath).TrimEnd('/')
        TeamID = [string]$data.TeamID
        ApplicationIdentity = [string]$data.ApplicationIdentity
        InstallerIdentity = [string]$data.InstallerIdentity
        NotaryKeychainProfile = [string]$data.NotaryKeychainProfile
        NotaryApiKeyPath = [string]$data.NotaryApiKeyPath
        NotaryApiKeyID = [string]$data.NotaryApiKeyID
        NotaryApiIssuerID = [string]$data.NotaryApiIssuerID
        ProvisioningProfilePath = [string]$data.ProvisioningProfilePath
    }
}

function Invoke-MobileEgressDesktopSsh {
    param(
        [Parameter(Mandatory)][pscustomobject]$Context,
        [Parameter(Mandatory)][string]$Command,
        [Parameter(Mandatory)][string]$Description
    )

    return Invoke-MobileEgressDesktopNativeCommand -FilePath 'ssh' -Arguments @(
        '-i', $Context.SshKeyPath,
        '-o', 'BatchMode=yes',
        '-o', 'IdentitiesOnly=yes',
        '-o', 'StrictHostKeyChecking=yes',
        '-o', 'ConnectTimeout=10',
        $Context.SshTarget,
        $Command
    ) -Description $Description
}

function Invoke-MobileEgressDesktopScp {
    param(
        [Parameter(Mandatory)][pscustomobject]$Context,
        [Parameter(Mandatory)][string]$Source,
        [Parameter(Mandatory)][string]$Destination,
        [Parameter(Mandatory)][string]$Description
    )

    $null = Invoke-MobileEgressDesktopNativeCommand -FilePath 'scp' -Arguments @(
        '-i', $Context.SshKeyPath,
        '-o', 'BatchMode=yes',
        '-o', 'IdentitiesOnly=yes',
        '-o', 'StrictHostKeyChecking=yes',
        '-o', 'ConnectTimeout=10',
        $Source,
        $Destination
    ) -Description $Description
}

function Invoke-MobileEgressMacDesktopAction {
    param(
        [Parameter(Mandatory)][string]$Action,
        [Parameter(Mandatory)][pscustomobject]$Context
    )

    $repo = ConvertTo-MobileEgressPosixLiteral $Context.MacRepositoryPath
    $commit = ConvertTo-MobileEgressPosixLiteral $Context.SourceCommit
    switch ($Action) {
        'prepare' {
            $releaseDirectory = ConvertTo-MobileEgressPosixLiteral $Context.RemoteReleaseDirectory
            $sourceBundle = ConvertTo-MobileEgressPosixLiteral $Context.RemoteSourceBundlePath
            $command = "set -eu; cd -- $repo; test -z `"`$(git status --porcelain)`"; git fetch --quiet $sourceBundle HEAD; test `"`$(git rev-parse FETCH_HEAD)`" = $commit; /bin/rm -f -- $sourceBundle; git checkout --detach --quiet $commit; test `"`$(git rev-parse HEAD)`" = $commit; /bin/mkdir -p -- $releaseDirectory"
            $null = Invoke-MobileEgressDesktopSsh -Context $Context -Command $command -Description 'Preparing same-commit Mac checkout'
            return
        }
        'upload-source' {
            Invoke-MobileEgressDesktopScp `
                -Context $Context `
                -Source $Context.LocalSourceBundlePath `
                -Destination ($Context.SshTarget + ':' + $Context.RemoteSourceBundlePath) `
                -Description 'Uploading exact Desktop source commit'
            return
        }
        'upload-manifest' {
            Invoke-MobileEgressDesktopScp `
                -Context $Context `
                -Source $Context.ManifestPath `
                -Destination ($Context.SshTarget + ':' + $Context.RemoteManifestPath) `
                -Description 'Uploading exact Windows node manifest'
            return
        }
        'release' {
            $scriptPath = ConvertTo-MobileEgressPosixLiteral ($Context.MacRepositoryPath + '/scripts/release-macos.sh')
            $manifest = ConvertTo-MobileEgressPosixLiteral $Context.RemoteManifestPath
            $version = ConvertTo-MobileEgressPosixLiteral $Context.Version
            $profile = ConvertTo-MobileEgressPosixLiteral $Context.ProvisioningProfilePath
            $team = ConvertTo-MobileEgressPosixLiteral $Context.TeamID
            $application = ConvertTo-MobileEgressPosixLiteral $Context.ApplicationIdentity
            $installer = ConvertTo-MobileEgressPosixLiteral $Context.InstallerIdentity
            $notary = ConvertTo-MobileEgressPosixLiteral $Context.NotaryKeychainProfile
            $notaryApiKey = ConvertTo-MobileEgressPosixLiteral $Context.NotaryApiKeyPath
            $notaryApiKeyID = ConvertTo-MobileEgressPosixLiteral $Context.NotaryApiKeyID
            $notaryApiIssuerID = ConvertTo-MobileEgressPosixLiteral $Context.NotaryApiIssuerID
            $command = "set -eu; cd -- $repo; /bin/sh $scriptPath --release-version $version --node-manifest $manifest --source-commit $commit --profile $profile --team-id $team --application-identity $application --installer-identity $installer --notary-keychain-profile $notary --notary-api-key $notaryApiKey --notary-api-key-id $notaryApiKeyID --notary-api-issuer-id $notaryApiIssuerID"
            $output = Invoke-MobileEgressDesktopSsh -Context $Context -Command $command -Description 'Building signed notarized macOS release'
            if (-not [string]::IsNullOrWhiteSpace($output)) {
                Write-Host $output
            }
            return
        }
        'remote-hash' {
            $pkg = ConvertTo-MobileEgressPosixLiteral $Context.RemotePkgPath
            $output = Invoke-MobileEgressDesktopSsh -Context $Context -Command ("/usr/bin/shasum -a 256 -- " + $pkg) -Description 'Reading remote macOS package hash'
            $match = [regex]::Match($output, '^([0-9a-f]{64})(?:\s|$)')
            if (-not $match.Success) {
                throw 'The Mac did not return a valid package SHA-256.'
            }
            return $match.Groups[1].Value
        }
        'download-pkg' {
            Invoke-MobileEgressDesktopScp `
                -Context $Context `
                -Source ($Context.SshTarget + ':' + $Context.RemotePkgPath) `
                -Destination $Context.LocalPkgPath `
                -Description 'Downloading notarized macOS package'
            return
        }
        'download-record' {
            Invoke-MobileEgressDesktopScp `
                -Context $Context `
                -Source ($Context.SshTarget + ':' + $Context.RemoteRecordPath) `
                -Destination $Context.LocalRecordPath `
                -Description 'Downloading macOS verification record'
            return
        }
        default {
            throw "Unsupported Mac Desktop action: $Action"
        }
    }
}

function Invoke-MobileEgressTask5RecordVerifier {
    param(
        [Parameter(Mandatory)][string]$RepositoryRoot,
        [Parameter(Mandatory)][string]$RecordPath,
        [Parameter(Mandatory)][string]$Version,
        [Parameter(Mandatory)][string]$SourceCommit,
        [Parameter(Mandatory)][string]$ManifestSha256,
        [Parameter(Mandatory)][string]$ArtifactSha256,
        [Parameter(Mandatory)][string]$ApplicationIdentity,
        [Parameter(Mandatory)][string]$InstallerIdentity
    )

    $goCommand = Get-Command 'go' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $goCommand) {
        $goExecutable = $goCommand.Source
    } else {
        $goCandidates = @()
        if (-not [string]::IsNullOrWhiteSpace($env:GOROOT)) {
            $goCandidates += Join-Path $env:GOROOT 'bin\go.exe'
        }
        $localApplicationData = [System.Environment]::GetFolderPath([System.Environment+SpecialFolder]::LocalApplicationData)
        if (-not [string]::IsNullOrWhiteSpace($localApplicationData)) {
            $goCandidates += Join-Path $localApplicationData 'Programs\Go\bin\go.exe'
        }
        $goExecutable = @($goCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1)
        if ($goExecutable.Count -eq 0) {
            throw 'Go is required to validate the Task 5 macOS verification record.'
        }
        $goExecutable = $goExecutable[0]
    }

    Push-Location $RepositoryRoot
    try {
        $null = Invoke-MobileEgressDesktopNativeCommand -FilePath $goExecutable -Arguments @(
            'run', './windows-client/cmd/mobile-egress-macos-release',
            'validate-record',
            $RecordPath,
            $Version,
            $SourceCommit,
            $ManifestSha256,
            $ArtifactSha256,
            $ApplicationIdentity,
            $InstallerIdentity
        ) -Description 'Task 5 macOS verification-record validation'
    } finally {
        Pop-Location
    }
}

function Assert-MobileEgressDesktopMacArtifacts {
    param(
        [Parameter(Mandatory)][string]$RepositoryRoot,
        [Parameter(Mandatory)][string]$Version,
        [Parameter(Mandatory)][string]$SourceCommit,
        [Parameter(Mandatory)][pscustomobject]$Config
    )

    $manifestPath = Join-Path $RepositoryRoot 'windows-client\build\bin\release-manifest.json'
    $releaseDirectory = Join-Path $RepositoryRoot 'windows-client\build\release'
    $pkgPath = Join-Path $releaseDirectory "mobile-egress-macos-$Version-arm64.pkg"
    $recordPath = Join-Path $releaseDirectory "mobile-egress-macos-$Version-arm64.verification.json"
    foreach ($path in @($manifestPath, $pkgPath, $recordPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Required Desktop release evidence is missing: $path"
        }
    }
    $manifestHash = (Get-FileHash -LiteralPath $manifestPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $artifactHash = (Get-FileHash -LiteralPath $pkgPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Invoke-MobileEgressTask5RecordVerifier `
        -RepositoryRoot $RepositoryRoot `
        -RecordPath $recordPath `
        -Version $Version `
        -SourceCommit $SourceCommit `
        -ManifestSha256 $manifestHash `
        -ArtifactSha256 $artifactHash `
        -ApplicationIdentity $Config.ApplicationIdentity `
        -InstallerIdentity $Config.InstallerIdentity
    return [pscustomobject]@{
        ArtifactName = [System.IO.Path]::GetFileName($pkgPath)
        ArtifactPath = $pkgPath
        ArtifactSha256 = $artifactHash
        RecordPath = $recordPath
        ManifestPath = $manifestPath
        ManifestSha256 = $manifestHash
    }
}

function Invoke-MobileEgressDesktopBuild {
    param(
        [Parameter(Mandatory)][string]$RepositoryRoot,
        [Parameter(Mandatory)][ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')][string]$Version,
        [Parameter(Mandatory)][ValidatePattern('^[0-9a-f]{40}$')][string]$SourceCommit,
        [Parameter(Mandatory)][pscustomobject]$Config,
        [scriptblock]$BuildWindows,
        [scriptblock]$CreateSourceBundle,
        [scriptblock]$InvokeMacAction,
        [scriptblock]$ValidateRecord
    )

    $releaseDirectory = Join-Path $RepositoryRoot 'windows-client\build\release'
    $artifactName = "mobile-egress-macos-$Version-arm64.pkg"
    $recordName = "mobile-egress-macos-$Version-arm64.verification.json"
    $transferID = [guid]::NewGuid().ToString('N')
    $finalPkgPath = Join-Path $releaseDirectory $artifactName
    $finalRecordPath = Join-Path $releaseDirectory $recordName
    $context = [pscustomobject][ordered]@{
        RepositoryRoot = $RepositoryRoot
        Version = $Version
        SourceCommit = $SourceCommit
        SshTarget = $Config.SshTarget
        SshKeyPath = $Config.SshKeyPath
        MacRepositoryPath = $Config.RepositoryPath
        TeamID = $Config.TeamID
        ApplicationIdentity = $Config.ApplicationIdentity
        InstallerIdentity = $Config.InstallerIdentity
        NotaryKeychainProfile = $Config.NotaryKeychainProfile
        NotaryApiKeyPath = $Config.NotaryApiKeyPath
        NotaryApiKeyID = $Config.NotaryApiKeyID
        NotaryApiIssuerID = $Config.NotaryApiIssuerID
        ProvisioningProfilePath = $Config.ProvisioningProfilePath
        ManifestPath = Join-Path $RepositoryRoot 'windows-client\build\bin\release-manifest.json'
        ManifestSha256 = ''
        ArtifactName = $artifactName
        RecordName = $recordName
        LocalPkgPath = Join-Path $releaseDirectory (".$artifactName.$transferID.partial")
        LocalRecordPath = Join-Path $releaseDirectory (".$recordName.$transferID.partial")
        FinalPkgPath = $finalPkgPath
        FinalRecordPath = $finalRecordPath
        RemoteReleaseDirectory = $Config.RepositoryPath.TrimEnd('/') + '/windows-client/build/release'
        LocalSourceBundlePath = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-desktop-source-" + [guid]::NewGuid().ToString('N') + '.bundle')
        RemoteSourceBundlePath = "/tmp/mobile-egress-desktop-source-$SourceCommit-$transferID.bundle"
        RemoteManifestPath = $Config.RepositoryPath.TrimEnd('/') + "/windows-client/build/release/windows-node-manifest-$Version.json"
        RemotePkgPath = $Config.RepositoryPath.TrimEnd('/') + "/windows-client/build/release/$artifactName"
        RemoteRecordPath = $Config.RepositoryPath.TrimEnd('/') + "/windows-client/build/release/$recordName"
        ArtifactSha256 = ''
    }
    foreach ($path in @($context.FinalPkgPath, $context.FinalRecordPath)) {
        if (Test-Path -LiteralPath $path) {
            throw "Desktop release output already exists and will not be overwritten: $path"
        }
    }
    $null = New-Item -ItemType Directory -Path $releaseDirectory -Force

    if ($null -eq $BuildWindows) {
        $BuildWindows = {
            param($BuildContext)
            Invoke-MobileEgressDesktopPowerShellScript `
                -Path (Join-Path $PSScriptRoot 'build-windows.ps1') `
                -Arguments @('-ReleaseVersion', $BuildContext.Version) `
                -Description 'Signed Windows Desktop release'
        }
    }
    if ($null -eq $CreateSourceBundle) {
        $CreateSourceBundle = {
            param($BundleContext)
            $head = (Invoke-MobileEgressDesktopNativeCommand -FilePath 'git' -Arguments @('-C', $BundleContext.RepositoryRoot, 'rev-parse', 'HEAD') -Description 'Reading Desktop source commit').Trim()
            if ($head -cne $BundleContext.SourceCommit) {
                throw 'The Desktop source commit changed before the Mac handoff.'
            }
            $null = Invoke-MobileEgressDesktopNativeCommand -FilePath 'git' -Arguments @('-C', $BundleContext.RepositoryRoot, 'bundle', 'create', $BundleContext.LocalSourceBundlePath, 'HEAD') -Description 'Creating exact Desktop source bundle'
            $null = Invoke-MobileEgressDesktopNativeCommand -FilePath 'git' -Arguments @('-C', $BundleContext.RepositoryRoot, 'bundle', 'verify', $BundleContext.LocalSourceBundlePath) -Description 'Verifying exact Desktop source bundle'
            $heads = Invoke-MobileEgressDesktopNativeCommand -FilePath 'git' -Arguments @('-C', $BundleContext.RepositoryRoot, 'bundle', 'list-heads', $BundleContext.LocalSourceBundlePath) -Description 'Reading Desktop source bundle head'
            if ($heads.Trim() -cne ($BundleContext.SourceCommit + ' HEAD')) {
                throw 'The Desktop source bundle does not identify the requested commit.'
            }
        }
    }
    if ($null -eq $InvokeMacAction) {
        $InvokeMacAction = { param($Action, $ActionContext) Invoke-MobileEgressMacDesktopAction -Action $Action -Context $ActionContext }
    }
    if ($null -eq $ValidateRecord) {
        $ValidateRecord = {
            param($ValidationContext)
            Invoke-MobileEgressTask5RecordVerifier `
                -RepositoryRoot $ValidationContext.RepositoryRoot `
                -RecordPath $ValidationContext.LocalRecordPath `
                -Version $ValidationContext.Version `
                -SourceCommit $ValidationContext.SourceCommit `
                -ManifestSha256 $ValidationContext.ManifestSha256 `
                -ArtifactSha256 $ValidationContext.ArtifactSha256 `
                -ApplicationIdentity $ValidationContext.ApplicationIdentity `
                -InstallerIdentity $ValidationContext.InstallerIdentity
        }
    }

    $pkgPromoted = $false
    try {
        & $BuildWindows $context
        if (-not (Test-Path -LiteralPath $context.ManifestPath -PathType Leaf)) {
            throw 'The signed Windows build did not produce release-manifest.json.'
        }
        $context.ManifestSha256 = (Get-FileHash -LiteralPath $context.ManifestPath -Algorithm SHA256).Hash.ToLowerInvariant()

        & $CreateSourceBundle $context
        if (-not (Test-Path -LiteralPath $context.LocalSourceBundlePath -PathType Leaf)) {
            throw 'The exact Desktop source bundle was not created.'
        }
        & $InvokeMacAction 'upload-source' $context
        & $InvokeMacAction 'prepare' $context
        & $InvokeMacAction 'upload-manifest' $context
        & $InvokeMacAction 'release' $context
        $remoteHash = (& $InvokeMacAction 'remote-hash' $context | Out-String).Trim()
        if ($remoteHash -notmatch '^[0-9a-f]{64}$') {
            throw 'The Mac did not return a valid package SHA-256.'
        }
        & $InvokeMacAction 'download-pkg' $context
        & $InvokeMacAction 'download-record' $context
        foreach ($path in @($context.LocalPkgPath, $context.LocalRecordPath)) {
            if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
                throw "The Mac release transfer did not produce: $path"
            }
        }
        $context.ArtifactSha256 = (Get-FileHash -LiteralPath $context.LocalPkgPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($context.ArtifactSha256 -cne $remoteHash) {
            throw 'The downloaded macOS package SHA-256 does not match the Mac build output.'
        }
        & $ValidateRecord $context
        [System.IO.File]::Move($context.LocalPkgPath, $context.FinalPkgPath)
        $pkgPromoted = $true
        try {
            [System.IO.File]::Move($context.LocalRecordPath, $context.FinalRecordPath)
        } catch {
            if ($pkgPromoted -and (Test-Path -LiteralPath $context.FinalPkgPath -PathType Leaf) -and -not (Test-Path -LiteralPath $context.FinalRecordPath)) {
                [System.IO.File]::Delete($context.FinalPkgPath)
            }
            throw
        }
    } finally {
        foreach ($transientPath in @($context.LocalSourceBundlePath, $context.LocalPkgPath, $context.LocalRecordPath)) {
            if (Test-Path -LiteralPath $transientPath -PathType Leaf) {
                [System.IO.File]::Delete($transientPath)
            }
        }
    }

    return [pscustomobject]@{
        ArtifactName = $context.ArtifactName
        ArtifactPath = $context.FinalPkgPath
        ArtifactSha256 = $context.ArtifactSha256
        RecordPath = $context.FinalRecordPath
        ManifestPath = $context.ManifestPath
        ManifestSha256 = $context.ManifestSha256
    }
}

function Invoke-MobileEgressDesktopEntry {
    param(
        [Parameter(Mandatory)][string]$ReleaseVersion,
        [switch]$PublishRelease,
        [scriptblock]$ReleaseAction
    )

    if ($null -eq $ReleaseAction) {
        $ReleaseAction = {
            param($Version, $Components, $ShouldPublish)
            & (Join-Path $PSScriptRoot 'release-all.ps1') -ReleaseVersion $Version -Components $Components -Publish:$ShouldPublish
        }
    }
    & $ReleaseAction $ReleaseVersion @('Desktop') ([bool]$PublishRelease)
}

if ($MyInvocation.InvocationName -eq '.') {
    return
}
if ([string]::IsNullOrWhiteSpace($ReleaseVersion)) {
    throw 'ReleaseVersion is required. Example: .\scripts\release-desktop.ps1 -ReleaseVersion 1.1.0'
}
if ($BuildArtifacts -and $ValidateArtifacts) {
    throw 'BuildArtifacts and ValidateArtifacts cannot be combined.'
}
if ($BuildArtifacts -or $ValidateArtifacts) {
    if ($Publish) {
        throw 'Publish cannot be combined with an internal Desktop artifact operation.'
    }
    if ([string]::IsNullOrWhiteSpace($SourceCommit)) {
        throw 'SourceCommit is required for Desktop artifact operations.'
    }
    $config = Get-MobileEgressDesktopConfig -RepositoryRoot $desktopRepositoryRoot
    if ($BuildArtifacts) {
        $result = Invoke-MobileEgressDesktopBuild -RepositoryRoot $desktopRepositoryRoot -Version $ReleaseVersion -SourceCommit $SourceCommit -Config $config
    } else {
        $result = Assert-MobileEgressDesktopMacArtifacts -RepositoryRoot $desktopRepositoryRoot -Version $ReleaseVersion -SourceCommit $SourceCommit -Config $config
    }
    Write-Host "sha256:$($result.ArtifactSha256)  $($result.ArtifactName)"
    return
}
Invoke-MobileEgressDesktopEntry -ReleaseVersion $ReleaseVersion -PublishRelease:$Publish
