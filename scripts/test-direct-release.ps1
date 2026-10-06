$ErrorActionPreference = 'Stop'
function Assert-DirectRelease([bool]$Condition,[string]$Message) { if (-not $Condition) { throw "Assertion failed: $Message" } }
. (Join-Path $PSScriptRoot 'release-all.ps1')
. (Join-Path $PSScriptRoot 'release-desktop.ps1')

$direct = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '2.0.0' -Components Desktop,Android)
Assert-DirectRelease (($direct.Name -join ',') -ceq 'MobileEgressClientSetup.exe,mobile-egress-client-macos-2.0.0-arm64.pkg,zfnf-mobile-egress-android-2.0.0.apk') 'Direct releases must publish only Client installers and selected Android.'
$windows = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'C:\fixture' -Version '2.0.0' -Components Windows,Android)
Assert-DirectRelease (($windows.Name -join ',') -ceq 'MobileEgressClientSetup.exe,zfnf-mobile-egress-android-2.0.0.apk') 'Windows scoped direct release must exclude Mac and retired assets.'
Assert-DirectRelease ((Get-MobileEgressWindowsDownloadName -Version '2.0.0') -ceq 'MobileEgressClientSetup.exe') 'Direct Windows download must be the canonical Client filename.'
Assert-DirectRelease ((Get-MobileEgressWindowsBuildScriptName -Version '2.0.0') -ceq 'build-client-windows.ps1') 'Direct builds must route only to Client commands.'
$history = @([pscustomobject]@{tagName='v1.2.3';isDraft=$false;assets=@([pscustomobject]@{name='MobileEgressClientSetup.exe'},[pscustomobject]@{name='mobile-egress-client-macos-1.2.3-arm64.pkg'},[pscustomobject]@{name='zfnf-mobile-egress-android-1.2.3.apk'})})
$links = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v2.0.0' -Version '2.0.0' -ReleasedArtifacts @([pscustomobject]@{Name='MobileEgressClientSetup.exe'}) -PublishedReleases $history)
Assert-DirectRelease (($links.Key -join ',') -ceq 'client-windows,client-macos,android') 'Direct download section must not advertise retired products.'
Assert-DirectRelease (@($links | Where-Object { $_.Url -match '/v1\.' }).Count -eq 0) 'Direct releases must never fall back to incompatible 1.x downloads.'
Assert-DirectRelease (($links | Where-Object Key -eq 'client-macos').UnavailableReason -ceq 'Not included in this release scope; use a later Desktop release for macOS') 'A Windows-scoped direct release must explain that Mac is outside its scope.'
$history += [pscustomobject]@{tagName='v2.0.1';isDraft=$false;assets=@([pscustomobject]@{name='zfnf-mobile-egress-android-2.0.1.apk'})}
$links = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v2.0.2' -Version '2.0.2' -ReleasedArtifacts @([pscustomobject]@{Name='InevitableMobileRelaySetup.exe'}) -PublishedReleases $history)
Assert-DirectRelease (($links | Where-Object Key -eq 'android').Tag -ceq 'v2.0.1') 'Compatible same-major fallback should remain available.'

$fixture = Join-Path ([IO.Path]::GetTempPath()) ('mobile-egress-direct-release-' + [guid]::NewGuid().ToString('N'))
try {
    $config = [pscustomobject]@{SshTarget='builder@example.local';SshKeyPath='C:\fixture\key';RepositoryPath='/Users/builder/repo';TeamID='ABCDEFGHIJ';ApplicationIdentity='Developer ID Application: Example (ABCDEFGHIJ)';InstallerIdentity='Developer ID Installer: Example (ABCDEFGHIJ)';NotaryApiKeyPath='/Users/builder/key.p8';NotaryApiKeyID='ABCDEFGHIJ';NotaryApiIssuerID='11111111-2222-3333-4444-555555555555';MacKeychainPassword='fixture'}
    $events = [Collections.Generic.List[string]]::new()
    $result = Invoke-MobileEgressDesktopBuild -RepositoryRoot $fixture -Version '2.0.0' -SourceCommit ('b' * 40) -Config $config -BuildWindows {
        param($Context)
        $events.Add('windows')
        Assert-DirectRelease ($Context.ClientArtifact -and [string]::IsNullOrEmpty($Context.ManifestPath)) 'Direct Mac build must not require a controller manifest.'
    } -CreateSourceBundle {
        param($Context)
        $events.Add('source-bundle')
        [IO.File]::WriteAllText($Context.LocalSourceBundlePath,'source')
    } -InvokeMacAction {
        param($Action,$Context)
        $events.Add($Action)
        if ($Action -eq 'remote-hash') { return '59638dd7840153c42541c7a8b84d3b4adf498cc7b24bc09975c91b488d677fa4' }
        if ($Action -eq 'download-pkg') { [IO.File]::WriteAllText($Context.LocalPkgPath,'mac-pkg-fixture',[Text.UTF8Encoding]::new($false)) }
        if ($Action -eq 'download-record') { [IO.File]::WriteAllText($Context.LocalRecordPath,'{}') }
    } -ValidateRecord {
        param($Context)
        $events.Add('verify')
        Assert-DirectRelease ($Context.ClientArtifact -and $Context.ManifestSha256 -ceq 'unused') 'Direct PKG must use Client verification expectations.'
        Assert-DirectRelease (-not (Test-Path -LiteralPath $Context.FinalPkgPath)) 'Unverified artifacts must not be promoted.'
    }
    Assert-DirectRelease ($result.ArtifactName -ceq 'mobile-egress-client-macos-2.0.0-arm64.pkg') 'Direct Desktop must produce only the Client PKG.'
    Assert-DirectRelease (($events -join ',') -ceq 'windows,source-bundle,upload-source,prepare,release,remote-hash,download-pkg,download-record,verify') 'Direct build must omit manifest transfer and controller artifact stages.'
    foreach ($failure in @('missing-record','invalid-record','wrong-hash')) {
        $failureRoot = Join-Path $fixture $failure
        $rejected = $false
        try {
            $null = Invoke-MobileEgressDesktopBuild -RepositoryRoot $failureRoot -Version '2.0.0' -SourceCommit ('b' * 40) -Config $config -BuildWindows {} -CreateSourceBundle {
                param($Context)
                [IO.File]::WriteAllText($Context.LocalSourceBundlePath,'source')
            } -InvokeMacAction {
                param($Action,$Context)
                if ($Action -eq 'remote-hash') { if ($failure -eq 'wrong-hash') { return ('f' * 64) }; return '59638dd7840153c42541c7a8b84d3b4adf498cc7b24bc09975c91b488d677fa4' }
                if ($Action -eq 'download-pkg') { [IO.File]::WriteAllText($Context.LocalPkgPath,'mac-pkg-fixture',[Text.UTF8Encoding]::new($false)) }
                if ($Action -eq 'download-record' -and $failure -ne 'missing-record') { [IO.File]::WriteAllText($Context.LocalRecordPath,'{}') }
            } -ValidateRecord {
                param($Context)
                throw 'fixture verifier rejects invalid Client record'
            }
        } catch { $rejected = $true }
        Assert-DirectRelease $rejected "Direct build must reject $failure."
        $failureRelease = Join-Path $failureRoot 'windows-client\build\release'
        Assert-DirectRelease (@(Get-ChildItem -LiteralPath $failureRelease -Force -File).Count -eq 0) "Failed $failure must leave neither promoted artifacts nor transfer partials."
    }
    $signing = Join-Path $fixture 'windows-signing'
    $null = New-Item -ItemType Directory -Path $signing
    foreach ($name in @('mobile-egress-code-signing.cer','release-signing-certificate.txt')) { Copy-Item -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) "windows-signing\$name") -Destination (Join-Path $signing $name) }
    $clientRoot = Join-Path $fixture 'windows-client\build\release\mobile-egress-client-windows-2.0.0'
    $null = New-Item -ItemType Directory -Path $clientRoot
    foreach ($name in @('mobile-egress-client.exe','mobile-egress-client-app.exe','MobileEgressClientSetup.exe','payload-verification.zip')) { [IO.File]::WriteAllText((Join-Path $clientRoot $name),'fixture') }
    $publicCertificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new((Join-Path $signing 'mobile-egress-code-signing.cer'))
    $verifyParameters = @{
        RepositoryRoot=$fixture;Version='2.0.0';SourceCommit=('b'*40)
        SignatureReader={param($Path) [pscustomobject]@{Status='Valid';SignerCertificate=$publicCertificate;TimeStamperCertificate=$publicCertificate}}
        BuildInfoReader={param($Path) $command=if ([IO.Path]::GetFileName($Path) -ceq 'MobileEgressClientSetup.exe') {'mobile-egress-setup'}else{[IO.Path]::GetFileNameWithoutExtension($Path)}; "path`t mobile-egress/windows-client/cmd/$command`nbuild`t vcs.revision=$('b'*40)`nbuild`t vcs.modified=false`nbuild`t GOOS=windows`nbuild`t GOARCH=amd64"}
        VersionReader={param($Path) '2.0.0'}
        PayloadVerifier={param($InstallerPath,$PayloadPath,$Sources) Assert-DirectRelease (($Sources.Keys -join ',') -ceq 'mobile-egress-client.exe,mobile-egress-client-app.exe,mobile-egress-code-signing.cer,release-signing-certificate.txt') 'Direct installer payload must contain exactly Client binaries and publisher proofs.'}
    }
    try {
        Assert-MobileEgressDirectWindowsArtifacts @verifyParameters
        $verifyParameters.BuildInfoReader={param($Path) "build vcs.revision=$('c'*40)`nbuild vcs.modified=false"}
        $rejected=$false;try { Assert-MobileEgressDirectWindowsArtifacts @verifyParameters } catch { $rejected=$true }
        Assert-DirectRelease $rejected 'A direct Windows artifact from another commit must be rejected.'
        if ($env:OS -eq 'Windows_NT') {
            $source = Join-Path $fixture 'version-fixture.go'
            [IO.File]::WriteAllText($source, 'package main; import "fmt"; func main() { fmt.Println("2.0.0") }')
            $guiVersion = Join-Path $clientRoot 'mobile-egress-client-app.exe'
            $builtFixture = Join-Path $fixture 'version-gui.exe'
            & go build -ldflags '-H windowsgui' -o $builtFixture $source
            if ($LASTEXITCODE -ne 0) { throw 'Could not build the real Windows GUI version fixture.' }
            Copy-Item -LiteralPath $builtFixture -Destination $guiVersion -Force
            Copy-Item -LiteralPath $guiVersion -Destination (Join-Path $clientRoot 'mobile-egress-client.exe') -Force
            $verifyParameters.BuildInfoReader={param($Path) $command=if ([IO.Path]::GetFileName($Path) -ceq 'MobileEgressClientSetup.exe') {'mobile-egress-setup'}else{[IO.Path]::GetFileNameWithoutExtension($Path)}; "path`t mobile-egress/windows-client/cmd/$command`nbuild`t vcs.revision=$('b'*40)`nbuild`t vcs.modified=false`nbuild`t GOOS=windows`nbuild`t GOARCH=amd64"}
            $verifyParameters.Remove('VersionReader')
            Assert-MobileEgressDirectWindowsArtifacts @verifyParameters
            $verifyParameters.VersionReader={param($Path) '2.0.1'}
            $rejected=$false;try { Assert-MobileEgressDirectWindowsArtifacts @verifyParameters } catch { $rejected=$true }
            Assert-DirectRelease $rejected 'Capturing GUI output must still reject the wrong version.'
        }
    } finally { $publicCertificate.Dispose() }
} finally {
    $resolved = [IO.Path]::GetFullPath($fixture)
    if ($resolved.StartsWith([IO.Path]::GetFullPath([IO.Path]::GetTempPath()),[StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($resolved).StartsWith('mobile-egress-direct-release-')) { if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force } }
}

Write-Host 'Direct release contract checks passed.'
