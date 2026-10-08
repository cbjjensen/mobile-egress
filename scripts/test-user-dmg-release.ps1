$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-all.ps1')
. (Join-Path $PSScriptRoot 'release-desktop.ps1')
function Assert-Dmg([bool]$Condition, [string]$Message) { if (-not $Condition) { throw "Assertion failed: $Message" } }

foreach ($version in @('2.0.5','2.0.6','2.0.10','2.1.0','3.0.0')) {
    $definitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'G:\fixture' -Version $version -Components Desktop)
    $expected = if ([version]$version -ge [version]'2.0.6') { "InevitableMobileRelaySetup.exe,inevitable-mobile-relay-macos-$version-arm64.pkg,inevitable-mobile-relay-macos-$version-arm64.dmg" } else { "InevitableMobileRelaySetup.exe,inevitable-mobile-relay-macos-$version-arm64.pkg" }
    Assert-Dmg (($definitions.Name -join ',') -ceq $expected) "Desktop $version must enforce its versioned artifact set."
    $links = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag "v$version" -Version $version -ReleasedArtifacts $definitions)
    Assert-Dmg (($links | Where-Object Url).Name.Count -eq $definitions.Count) 'Release notes must link all selected Mac formats.'
}

$fixture = Join-Path ([IO.Path]::GetTempPath()) ('mobile-relay-dmg-release-' + [guid]::NewGuid().ToString('N'))
$config = [pscustomobject]@{SshTarget='builder@example.local';SshKeyPath='G:\fixture\key';RepositoryPath='/Users/builder/repo';TeamID='ABCDEFGHIJ';ApplicationIdentity='Developer ID Application: Example (ABCDEFGHIJ)';InstallerIdentity='Developer ID Installer: Example (ABCDEFGHIJ)';NotaryApiKeyPath='/Users/builder/key.p8';NotaryApiKeyID='ABCDEFGHIJ';NotaryApiIssuerID='11111111-2222-3333-4444-555555555555';MacKeychainPassword='fixture'}
try {
    foreach ($fault in @('none','dmg-hash','dmg-validation','concurrent-record')) {
        $caseRoot = Join-Path $fixture $fault
        $events = [Collections.Generic.List[string]]::new()
        $releaseDirectory = Join-Path $caseRoot 'windows-client\build\release'
        $pkg = Join-Path $releaseDirectory 'inevitable-mobile-relay-macos-2.0.6-arm64.pkg'
        $pkgRecord = $pkg.Replace('.pkg','.verification.json')
        $dmg = $pkg.Replace('.pkg','.dmg')
        $dmgRecord = $pkg.Replace('.pkg','.dmg.verification.json')
        $failure = $null
        try {
            $result = Invoke-MobileEgressDesktopBuild -RepositoryRoot $caseRoot -Version '2.0.6' -SourceCommit ('b' * 40) -Config $config -BuildWindows {} -CreateSourceBundle {
                param($Context)
                [IO.File]::WriteAllText($Context.LocalSourceBundlePath, 'synthetic source')
            } -InvokeMacAction {
                param($Action, $Context)
                $events.Add("$Action|$($Context.ArtifactName)")
                if ($Action -eq 'remote-hash') {
                    if ($Context.DmgArtifact -and $fault -eq 'dmg-hash') { return ('f' * 64) }
                    return '59638dd7840153c42541c7a8b84d3b4adf498cc7b24bc09975c91b488d677fa4'
                }
                if ($Action -eq 'download-pkg') { [IO.File]::WriteAllText($Context.LocalPkgPath, 'mac-pkg-fixture', [Text.UTF8Encoding]::new($false)) }
                if ($Action -eq 'download-record') { [IO.File]::WriteAllText($Context.LocalRecordPath, '{}') }
            } -ValidateRecord {
                param($Context)
                Assert-Dmg (-not (Test-Path -LiteralPath $pkg) -and -not (Test-Path -LiteralPath $dmg)) 'Neither Mac format may be promoted before all records pass.'
                $events.Add("validate|$($Context.ArtifactName)")
                if ($Context.DmgArtifact -and $fault -eq 'dmg-validation') { throw 'rejected DMG record' }
                if ($Context.DmgArtifact -and $fault -eq 'concurrent-record') { [IO.File]::WriteAllText($dmgRecord, 'concurrent evidence') }
            }
        } catch { $failure = $_.Exception.Message }
        if ($fault -eq 'none') {
            Assert-Dmg ($null -eq $failure) "Valid coupled Mac transfer failed: $failure"
            foreach ($path in @($pkg,$pkgRecord,$dmg,$dmgRecord)) { Assert-Dmg (Test-Path -LiteralPath $path -PathType Leaf) "Verified output missing: $path" }
            Assert-Dmg ($events.Contains('release-dmg|inevitable-mobile-relay-macos-2.0.6-arm64.dmg')) 'User DMG must have its own guarded Mac build action.'
            Assert-Dmg ($events.Contains('validate|inevitable-mobile-relay-macos-2.0.6-arm64.dmg')) 'DMG record must be verified before promotion.'
            $before = [IO.File]::ReadAllText($dmg)
            $rejected = $false
            try { Invoke-MobileEgressDesktopBuild -RepositoryRoot $caseRoot -Version '2.0.6' -SourceCommit ('b' * 40) -Config $config -BuildWindows { throw 'should not build' } }
            catch { $rejected = $_.Exception.Message -match 'will not be overwritten' }
            Assert-Dmg $rejected 'Existing DMG output must stop before a build.'
            Assert-Dmg ([IO.File]::ReadAllText($dmg) -ceq $before) 'Existing output bytes changed.'
        } else {
            Assert-Dmg ($null -ne $failure) "Fault $fault must abort transfer."
            foreach ($path in @($pkg,$pkgRecord,$dmg)) { Assert-Dmg (-not (Test-Path -LiteralPath $path)) "Fault $fault left promoted output: $path" }
            if ($fault -eq 'concurrent-record') { Assert-Dmg ([IO.File]::ReadAllText($dmgRecord) -ceq 'concurrent evidence') 'Concurrent output must be preserved.' }
        }
        Assert-Dmg (@(Get-ChildItem -LiteralPath $releaseDirectory -Filter '*.partial' -Force).Count -eq 0) 'Transfer partials must be removed after success or failure.'
    }
} finally {
    $resolved = [IO.Path]::GetFullPath($fixture)
    if ($resolved.StartsWith([IO.Path]::GetFullPath([IO.Path]::GetTempPath()), [StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($resolved).StartsWith('mobile-relay-dmg-release-')) {
        if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
    }
}
Write-Host 'PASS: DMG contract, coupled transfer validation, failure cleanup and immutable promotion.'
