$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-all.ps1')
. (Join-Path $PSScriptRoot 'release-desktop.ps1')
. (Join-Path $PSScriptRoot 'release-android.ps1')
function Assert-Branding([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw "Assertion failed: $Message" }
}

foreach ($historicalVersion in @('1.1.7', '2.0.0', '2.0.1')) {
    $historicalBuildRejected = $false
    try { Assert-MobileEgressCurrentReleaseBuildVersion -Version $historicalVersion }
    catch { $historicalBuildRejected = $_.Exception.Message -match 'original historical source checkout' }
    Assert-Branding $historicalBuildRejected 'Current renamed source must never rebuild a historical release.'
    Assert-MobileEgressCurrentReleaseBuildVersion -Version $historicalVersion -FrozenResume
}
Assert-MobileEgressCurrentReleaseBuildVersion -Version '2.0.2'

$androidBuildSource = Get-Content -Raw -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'android\app\build.gradle.kts')
$trackedAndroidVersion = Get-MobileEgressAndroidVersionName -BuildFileContent $androidBuildSource
Assert-Branding (-not [string]::IsNullOrWhiteSpace($trackedAndroidVersion)) 'The actual Gradle version declaration must remain readable.'
# Normalize fixture copies independently of the version being released.
$androidHistoricalSource = $androidBuildSource.Replace("val androidVersionName = `"$trackedAndroidVersion`"", 'val androidVersionName = "2.0.1"')
$androidHistoricalRejected = $false
try { Get-MobileEgressAndroidBuildApkName -BuildFileContent $androidHistoricalSource }
catch { $androidHistoricalRejected = $_.Exception.Message -match 'original historical source checkout' }
Assert-Branding $androidHistoricalRejected 'The actual Gradle constant form must parse before rejecting frozen 2.0.1.'
$androidBrandedSource = $androidBuildSource.Replace("val androidVersionName = `"$trackedAndroidVersion`"", 'val androidVersionName = "2.0.2"')
Assert-Branding ((Get-MobileEgressAndroidBuildApkName -BuildFileContent $androidBrandedSource) -ceq 'inevitable-mobile-relay-android-2.0.2.apk') 'A 2.0.2 bump in the actual Gradle constant must select its branded APK before preflight.'
Assert-Branding ((Get-MobileEgressAndroidBuildApkName -BuildFileContent 'versionName = "2.0.2"') -ceq 'inevitable-mobile-relay-android-2.0.2.apk') 'Literal Gradle versionName compatibility must remain supported.'

foreach ($version in @('2.0.0', '2.0.1', '2.0.2', '2.0.10', '2.1.0', '3.0.0')) {
    $branded = [version]$version -ge [version]'2.0.2'
    $windows = if ($branded) { 'InevitableMobileRelaySetup.exe' } else { 'MobileEgressClientSetup.exe' }
    $mac = if ($branded) { "inevitable-mobile-relay-macos-$version-arm64.pkg" } else { "mobile-egress-client-macos-$version-arm64.pkg" }
    $android = if ($branded) { "inevitable-mobile-relay-android-$version.apk" } else { "zfnf-mobile-egress-android-$version.apk" }
    $dmg = if ([version]$version -ge [version]'2.0.6') { "inevitable-mobile-relay-macos-$version-arm64.dmg" } else { '' }
    $expectedNames = if ($dmg) { "$windows,$mac,$dmg,$android" } else { "$windows,$mac,$android" }
    $definitions = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'G:\fixture' -Version $version -Components Desktop,Android)
    Assert-Branding (($definitions.Name -join ',') -ceq $expectedNames) "Release $version must use its exact historical or branded artifact names."
    Assert-Branding ($definitions[0].Path -ceq "G:\fixture\windows-client\build\release\mobile-egress-client-windows-$version\$windows") 'Windows internal release directory must remain unchanged.'
    Assert-Branding ($definitions[1].Path -ceq "G:\fixture\windows-client\build\release\$mac") 'Mac path must use the canonical package basename.'
    Assert-Branding ($definitions[-1].Path -ceq "G:\fixture\android\app\build\outputs\apk\release\$android") 'Android path must agree with the canonical Gradle output.'
    $links = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag "v$version" -Version $version -ReleasedArtifacts $definitions)
    Assert-Branding (($links.Name -join ',') -ceq $expectedNames) 'Current direct links must expose exactly the selected artifact contract.'
}

foreach ($fallbackVersion in @('2.0.0', '2.0.1', '2.0.2')) {
    $artifacts = @(Get-MobileEgressReleaseArtifactDefinitions -RepositoryRoot 'G:\fixture' -Version $fallbackVersion -Components Desktop,Android)
    $history = @([pscustomobject]@{tagName="v$fallbackVersion";isDraft=$false;assets=@($artifacts | ForEach-Object { [pscustomobject]@{name=$_.Name} })})
    $links = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v2.0.3' -Version '2.0.3' -ReleasedArtifacts @([pscustomobject]@{Name='not-released.txt'}) -PublishedReleases $history)
    Assert-Branding (($links.Name -join ',') -ceq ($artifacts.Name -join ',')) 'Same-major fallback must retain the exact canonical name of its source version.'
    Assert-Branding (@($links | Where-Object Tag -CNE "v$fallbackVersion").Count -eq 0) 'Fallback URLs must retain their historical tag.'
}
$incorrectHistory = @(
    [pscustomobject]@{tagName='v2.0.2';isDraft=$false;assets=@([pscustomobject]@{name='MobileEgressClientSetup.exe'},[pscustomobject]@{name='mobile-egress-client-macos-2.0.2-arm64.pkg'},[pscustomobject]@{name='zfnf-mobile-egress-android-2.0.2.apk'})},
    [pscustomobject]@{tagName='v2.0.1';isDraft=$false;assets=@([pscustomobject]@{name='InevitableMobileRelaySetup.exe'},[pscustomobject]@{name='inevitable-mobile-relay-macos-2.0.1-arm64.pkg'},[pscustomobject]@{name='inevitable-mobile-relay-android-2.0.1.apk'})}
)
$links = @(Resolve-MobileEgressReleaseDownloadLinks -CurrentTag 'v2.0.3' -Version '2.0.3' -ReleasedArtifacts @([pscustomobject]@{Name='not-released.txt'}) -PublishedReleases $incorrectHistory)
Assert-Branding (@($links | Where-Object { $_.Url }).Count -eq 0) 'Fallback must reject names from the wrong branding contract.'

$fixture = Join-Path ([IO.Path]::GetTempPath()) ('mobile-relay-release-branding-' + [guid]::NewGuid().ToString('N'))
try {
    $config = [pscustomobject]@{SshTarget='builder@example.local';SshKeyPath='G:\fixture\key';RepositoryPath='/Users/builder/repo';TeamID='ABCDEFGHIJ';ApplicationIdentity='Developer ID Application: Example (ABCDEFGHIJ)';InstallerIdentity='Developer ID Installer: Example (ABCDEFGHIJ)';NotaryApiKeyPath='/Users/builder/key.p8';NotaryApiKeyID='ABCDEFGHIJ';NotaryApiIssuerID='11111111-2222-3333-4444-555555555555';MacKeychainPassword='fixture'}
    foreach ($version in @('2.0.0', '2.0.2')) {
        $caseRoot = Join-Path $fixture $version
        $expectedMac = if ($version -eq '2.0.0') { 'mobile-egress-client-macos-2.0.0-arm64.pkg' } else { 'inevitable-mobile-relay-macos-2.0.2-arm64.pkg' }
        $expectedRecord = $expectedMac.Replace('.pkg', '.verification.json')
        $result = Invoke-MobileEgressDesktopBuild -RepositoryRoot $caseRoot -Version $version -SourceCommit ('b' * 40) -Config $config -BuildWindows {} -CreateSourceBundle {
            param($Context)
            [IO.File]::WriteAllText($Context.LocalSourceBundlePath, 'synthetic source')
        } -InvokeMacAction {
            param($Action, $Context)
            Assert-Branding ($Context.ArtifactName -ceq $expectedMac -and $Context.RecordName -ceq $expectedRecord) 'Mac transfer must use matching package and verification record basenames.'
            if ($Action -eq 'remote-hash') { return '59638dd7840153c42541c7a8b84d3b4adf498cc7b24bc09975c91b488d677fa4' }
            if ($Action -eq 'download-pkg') { [IO.File]::WriteAllText($Context.LocalPkgPath, 'mac-pkg-fixture', [Text.UTF8Encoding]::new($false)) }
            if ($Action -eq 'download-record') { [IO.File]::WriteAllText($Context.LocalRecordPath, '{}') }
        } -ValidateRecord {
            param($Context)
            Assert-Branding ($Context.ClientArtifact -and $Context.ManifestSha256 -ceq 'unused') 'Branding must preserve Client record ownership and direct build settings.'
        }
        Assert-Branding ($result.ArtifactName -ceq $expectedMac) 'Verified package must be promoted under its canonical filename.'
        Assert-Branding ([IO.Path]::GetFileName($result.RecordPath) -ceq $expectedRecord) 'Private verification record must use the package basename.'
    }
    $signing = Join-Path $fixture 'windows-signing'
    $null = New-Item -ItemType Directory -Path $signing
    foreach ($name in @('mobile-egress-code-signing.cer', 'release-signing-certificate.txt')) {
        Copy-Item -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) "windows-signing\$name") -Destination (Join-Path $signing $name)
    }
    $publicCertificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new((Join-Path $signing 'mobile-egress-code-signing.cer'))
    try {
        foreach ($version in @('2.0.0', '2.0.2')) {
            $setupName = if ($version -eq '2.0.0') { 'MobileEgressClientSetup.exe' } else { 'InevitableMobileRelaySetup.exe' }
            $clientRoot = Join-Path $fixture "windows-client\build\release\mobile-egress-client-windows-$version"
            $null = New-Item -ItemType Directory -Path $clientRoot
            foreach ($name in @('mobile-egress-client.exe', 'mobile-egress-client-app.exe', $setupName, 'payload-verification.zip')) {
                [IO.File]::WriteAllText((Join-Path $clientRoot $name), 'synthetic fixture')
            }
            Assert-MobileEgressDirectWindowsArtifacts -RepositoryRoot $fixture -Version $version -SourceCommit ('b' * 40) -SignatureReader {
                param($Path)
                [pscustomobject]@{Status='Valid';SignerCertificate=$publicCertificate;TimeStamperCertificate=$publicCertificate}
            } -BuildInfoReader {
                param($Path)
                $command = if ([IO.Path]::GetFileName($Path) -ceq $setupName) { 'mobile-egress-setup' } else { [IO.Path]::GetFileNameWithoutExtension($Path) }
                "path`t mobile-egress/windows-client/cmd/$command`nbuild`t vcs.revision=$('b' * 40)`nbuild`t vcs.modified=false`nbuild`t GOOS=windows`nbuild`t GOARCH=amd64"
            } -VersionReader { param($Path) $version } -PayloadVerifier {
                param($InstallerPath, $PayloadPath, $Sources)
                Assert-Branding ([IO.Path]::GetFileName($InstallerPath) -ceq $setupName) 'Payload verification must inspect the canonical installer.'
                Assert-Branding (($Sources.Keys -join ',') -ceq 'mobile-egress-client.exe,mobile-egress-client-app.exe,mobile-egress-code-signing.cer,release-signing-certificate.txt') 'Payload membership and technical identities must stay unchanged.'
            }
        }
    } finally { $publicCertificate.Dispose() }
    foreach ($historicalVersion in @('2.0.0', '2.0.1')) {
        $rejected = $false
        try { & (Join-Path $PSScriptRoot 'build-client-windows.ps1') -ReleaseVersion $historicalVersion }
        catch { $rejected = $_.Exception.Message -match 'original historical source checkout' }
        Assert-Branding $rejected 'Raw Windows builds must reject historical versions before any build or signing action.'
    }
} finally {
    $resolved = [IO.Path]::GetFullPath($fixture)
    if ($resolved.StartsWith([IO.Path]::GetFullPath([IO.Path]::GetTempPath()), [StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($resolved).StartsWith('mobile-relay-release-branding-')) {
        if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
    }
}
$global:LASTEXITCODE = 0
Write-Host 'PASS: historical/branded artifact contracts, canonical fallbacks, Windows payload verification and matching Mac verification records.'
