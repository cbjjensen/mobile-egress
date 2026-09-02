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

$releaseDesktopScript = Join-Path $PSScriptRoot 'release-desktop.ps1'
Assert-Condition (Test-Path -LiteralPath $releaseDesktopScript -PathType Leaf) 'The coupled Desktop release entry point must exist.'
. $releaseDesktopScript

$entryCalls = [System.Collections.Generic.List[string]]::new()
Invoke-MobileEgressDesktopEntry -ReleaseVersion '1.1.0' -ReleaseAction {
    param($Version, $Components, $PublishRelease)
    $entryCalls.Add("$Version|$($Components -join ',')|$PublishRelease")
}
Invoke-MobileEgressDesktopEntry -ReleaseVersion '1.1.0' -PublishRelease -ReleaseAction {
    param($Version, $Components, $PublishRelease)
    $entryCalls.Add("$Version|$($Components -join ',')|$PublishRelease")
}
Assert-Condition (($entryCalls -join ';') -eq '1.1.0|Desktop|False;1.1.0|Desktop|True') 'The Desktop entry point must select only the coupled Desktop scope and must forward Publish explicitly.'

$legacyMessage = ''
try {
    & (Join-Path $PSScriptRoot 'release-windows.ps1') -ReleaseVersion '1.1.0'
} catch {
    $legacyMessage = $_.Exception.Message
}
Assert-Condition ($legacyMessage -eq 'Windows desktop releases are coupled with macOS. Use scripts\release-desktop.ps1 with the same -ReleaseVersion and optional -Publish.') 'The legacy Windows entry point must fail immediately with migration guidance.'

$fixtureRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-desktop-release-test-" + [guid]::NewGuid().ToString('N'))
try {
    $null = New-Item -ItemType Directory -Path $fixtureRoot
    $manifestPath = Join-Path $fixtureRoot 'windows-client\build\bin\release-manifest.json'
    $events = [System.Collections.Generic.List[string]]::new()
    $manifestContent = '{"version":2,"client":{"version":"1.1.0"}}'
    $expectedManifestHash = '17322820a35865258313d11163228a6fe3bd790834aa3a40ec75824b49d2a0d5'
    $expectedPkgHash = '59638dd7840153c42541c7a8b84d3b4adf498cc7b24bc09975c91b488d677fa4'
    $sourceCommit = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
    $transientPaths = [System.Collections.Generic.List[string]]::new()
    $expectedFinalPkg = Join-Path $fixtureRoot 'windows-client\build\release\mobile-egress-macos-1.1.0-arm64.pkg'
    $expectedFinalRecord = Join-Path $fixtureRoot 'windows-client\build\release\mobile-egress-macos-1.1.0-arm64.verification.json'
    $config = [pscustomobject]@{
        SshTarget = 'builder@example.local'
        SshKeyPath = 'C:\fixture\id_ed25519'
        RepositoryPath = '/Users/builder/workspace/mobile-egress'
        TeamID = 'ABCDEFGHIJ'
        ApplicationIdentity = 'Developer ID Application: Example (ABCDEFGHIJ)'
        InstallerIdentity = 'Developer ID Installer: Example (ABCDEFGHIJ)'
        NotaryKeychainProfile = 'mobile-egress-notary'
        NotaryApiKeyPath = '/Users/builder/secrets/AuthKey_ABCDEFGHIJ.p8'
        NotaryApiKeyID = 'ABCDEFGHIJ'
        NotaryApiIssuerID = '11111111-2222-3333-4444-555555555555'
        ProvisioningProfilePath = '/Users/builder/signing/controller.provisionprofile'
    }

    $result = Invoke-MobileEgressDesktopBuild `
        -RepositoryRoot $fixtureRoot `
        -Version '1.1.0' `
        -SourceCommit $sourceCommit `
        -Config $config `
        -BuildWindows {
            param($Context)
            $events.Add('windows')
            $null = New-Item -ItemType Directory -Path (Split-Path -Parent $Context.ManifestPath) -Force
            [System.IO.File]::WriteAllText($Context.ManifestPath, $manifestContent, [System.Text.UTF8Encoding]::new($false))
        } `
        -CreateSourceBundle {
            param($Context)
            $events.Add('source-bundle')
            $transientPaths.Add($Context.LocalSourceBundlePath)
            [System.IO.File]::WriteAllText($Context.LocalSourceBundlePath, 'exact-source-bundle', [System.Text.UTF8Encoding]::new($false))
        } `
        -InvokeMacAction {
            param($Action, $Context)
            $events.Add($Action)
            Assert-Condition ($Context.SourceCommit -eq $sourceCommit) "Mac action $Action must remain bound to the Windows source commit."
            if ($Action -eq 'upload-source') {
                Assert-Condition (Test-Path -LiteralPath $Context.LocalSourceBundlePath -PathType Leaf) 'The exact local source bundle must exist when it is handed to the Mac.'
                Assert-Condition ($Context.RemoteSourceBundlePath -match '^/tmp/mobile-egress-desktop-source-b{40}-[0-9a-f]{32}\.bundle$') 'The source bundle must use a unique pre-checkout Mac temporary path.'
            }
            if ($Action -eq 'upload-manifest') {
                $actual = [System.IO.File]::ReadAllText($Context.ManifestPath)
                Assert-Condition ($actual -ceq $manifestContent) 'The exact generated Windows node manifest must be handed to the Mac.'
                Assert-Condition ($Context.ManifestSha256 -ceq $expectedManifestHash) 'The manifest handoff must carry its exact SHA-256.'
            }
            if ($Action -eq 'release') {
                Assert-Condition ($Context.NotaryApiKeyPath -eq $config.NotaryApiKeyPath) 'The Desktop flow must pass the notary API key path to the Mac release script.'
                Assert-Condition ($Context.NotaryApiKeyID -eq $config.NotaryApiKeyID) 'The Desktop flow must pass the notary API key ID to the Mac release script.'
                Assert-Condition ($Context.NotaryApiIssuerID -eq $config.NotaryApiIssuerID) 'The Desktop flow must pass the notary API issuer ID to the Mac release script.'
            }
            if ($Action -eq 'remote-hash') {
                return $expectedPkgHash
            }
            if ($Action -eq 'download-pkg') {
                $transientPaths.Add($Context.LocalPkgPath)
                [System.IO.File]::WriteAllText($Context.LocalPkgPath, 'mac-pkg-fixture', [System.Text.UTF8Encoding]::new($false))
            }
            if ($Action -eq 'download-record') {
                $transientPaths.Add($Context.LocalRecordPath)
                [System.IO.File]::WriteAllText($Context.LocalRecordPath, '{}', [System.Text.UTF8Encoding]::new($false))
            }
        } `
        -ValidateRecord {
            param($Context)
            $events.Add('validate-record')
            Assert-Condition ($Context.ArtifactSha256 -ceq $expectedPkgHash) 'Task 5 record validation must receive the retrieved PKG hash.'
            Assert-Condition ($Context.ManifestSha256 -ceq $expectedManifestHash) 'Task 5 record validation must receive the handed-off manifest hash.'
            Assert-Condition (-not (Test-Path -LiteralPath $expectedFinalPkg)) 'The PKG must not appear at its final path before Task 5 validation passes.'
            Assert-Condition (-not (Test-Path -LiteralPath $expectedFinalRecord)) 'The verification record must not appear at its final path before Task 5 validation passes.'
        }

    Assert-Condition (($events -join ',') -eq 'windows,source-bundle,upload-source,prepare,upload-manifest,release,remote-hash,download-pkg,download-record,validate-record') 'Desktop release order must be Windows first, transfer its exact source commit, then run the same-commit Mac build, retrieval, and Task 5 validation.'
    Assert-Condition ($result.ArtifactName -eq 'mobile-egress-macos-1.1.0-arm64.pkg') 'The Desktop flow must produce the canonical same-version arm64 PKG name.'
    Assert-Condition ($result.ArtifactSha256 -ceq $expectedPkgHash) 'The Desktop flow must return the verified transfer hash.'
    Assert-Condition ((Test-Path -LiteralPath $expectedFinalPkg -PathType Leaf) -and (Test-Path -LiteralPath $expectedFinalRecord -PathType Leaf)) 'Only the validated PKG and record may be promoted to final local paths.'
    foreach ($transientPath in $transientPaths) {
        Assert-Condition (-not (Test-Path -LiteralPath $transientPath)) "Transient release input must be cleaned: $transientPath"
    }

    $mismatchRoot = Join-Path $fixtureRoot 'mismatch'
    $mismatchValidationEvents = [System.Collections.Generic.List[string]]::new()
    $mismatchRejected = $false
    try {
        Invoke-MobileEgressDesktopBuild `
            -RepositoryRoot $mismatchRoot `
            -Version '1.1.0' `
            -SourceCommit $sourceCommit `
            -Config $config `
            -BuildWindows {
                param($Context)
                $null = New-Item -ItemType Directory -Path (Split-Path -Parent $Context.ManifestPath) -Force
                [System.IO.File]::WriteAllText($Context.ManifestPath, $manifestContent, [System.Text.UTF8Encoding]::new($false))
            } `
            -CreateSourceBundle {
                param($Context)
                [System.IO.File]::WriteAllText($Context.LocalSourceBundlePath, 'exact-source-bundle', [System.Text.UTF8Encoding]::new($false))
            } `
            -InvokeMacAction {
                param($Action, $Context)
                if ($Action -eq 'remote-hash') { return ('f' * 64) }
                if ($Action -eq 'download-pkg') { [System.IO.File]::WriteAllText($Context.LocalPkgPath, 'mac-pkg-fixture', [System.Text.UTF8Encoding]::new($false)) }
                if ($Action -eq 'download-record') { [System.IO.File]::WriteAllText($Context.LocalRecordPath, '{}', [System.Text.UTF8Encoding]::new($false)) }
            } `
            -ValidateRecord { $mismatchValidationEvents.Add('validate-record') }
    } catch {
        $mismatchRejected = $_.Exception.Message -match 'SHA-256.*does not match'
    }
    Assert-Condition $mismatchRejected 'A retrieved PKG whose local hash differs from the Mac hash must be rejected.'
    Assert-Condition ($mismatchValidationEvents.Count -eq 0) 'A transfer hash mismatch must stop before record validation.'
    Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $mismatchRoot 'windows-client\build\release\mobile-egress-macos-1.1.0-arm64.pkg'))) 'A rejected transfer must not leave a final-looking PKG.'
    Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $mismatchRoot 'windows-client\build\release\mobile-egress-macos-1.1.0-arm64.verification.json'))) 'A rejected transfer must not leave a final-looking verification record.'

    $verifierRecordPath = Join-Path $fixtureRoot 'task5-verification.json'
    $verifierRecord = [ordered]@{
        schemaVersion = 1
        releaseVersion = '1.1.0'
        sourceCommit = $sourceCommit
        nodeManifestSha256 = $expectedManifestHash
        artifactName = 'mobile-egress-macos-1.1.0-arm64.pkg'
        artifactSha256 = $expectedPkgHash
        architecture = 'arm64'
        minimumMacOS = '13.0'
        controllerBundleId = 'com.cbjjensen.mobile-egress.controller'
        relayBundleId = 'com.cbjjensen.mobile-egress.relay'
        applicationIdentity = $config.ApplicationIdentity
        installerIdentity = $config.InstallerIdentity
        hardenedRuntime = $true
        appSandbox = $false
        bundleLayout = @(
            'Contents/Info.plist',
            'Contents/embedded.provisionprofile',
            'Contents/MacOS/mobile-egress-windows',
            'Contents/Resources/iconfile.icns',
            'Contents/Resources/mobile-egress-relay',
            'Contents/Library/LaunchDaemons/com.cbjjensen.mobile-egress.relay.plist'
        )
        nestedRelaySignature = 'valid'
        appSignature = 'valid'
        packageSignature = 'valid'
        notarization = 'accepted'
        staple = 'valid'
        checks = [ordered]@{
            codesign = 'passed'
            pkgutil = 'passed'
            spctl = 'passed'
            stapler = 'passed'
        }
    }
    [System.IO.File]::WriteAllText($verifierRecordPath, ($verifierRecord | ConvertTo-Json -Depth 5 -Compress), [System.Text.UTF8Encoding]::new($false))
    Invoke-MobileEgressTask5RecordVerifier `
        -RepositoryRoot (Split-Path -Parent $PSScriptRoot) `
        -RecordPath $verifierRecordPath `
        -Version '1.1.0' `
        -SourceCommit $sourceCommit `
        -ManifestSha256 $expectedManifestHash `
        -ArtifactSha256 $expectedPkgHash `
        -ApplicationIdentity $config.ApplicationIdentity `
        -InstallerIdentity $config.InstallerIdentity
    $wrongCommitRejected = $false
    try {
        Invoke-MobileEgressTask5RecordVerifier `
            -RepositoryRoot (Split-Path -Parent $PSScriptRoot) `
            -RecordPath $verifierRecordPath `
            -Version '1.1.0' `
            -SourceCommit ('c' * 40) `
            -ManifestSha256 $expectedManifestHash `
            -ArtifactSha256 $expectedPkgHash `
            -ApplicationIdentity $config.ApplicationIdentity `
            -InstallerIdentity $config.InstallerIdentity
    } catch {
        $wrongCommitRejected = $_.Exception.Message -match 'Task 5 macOS verification-record validation failed'
    }
    Assert-Condition $wrongCommitRejected 'The Windows orchestrator must use Task 5 validation to reject a record from a different source commit.'

    $desktopScriptSource = Get-Content -Raw $releaseDesktopScript
    $macScriptSource = Get-Content -Raw (Join-Path (Split-Path -Parent $PSScriptRoot) 'scripts\release-macos.sh')
    Assert-Condition ($desktopScriptSource -match 'NotaryApiKeyPath') 'The Desktop release config must support a notary API key path.'
    Assert-Condition ($desktopScriptSource -match 'notary-api-key') 'The Desktop orchestrator must pass notary API-key arguments to the Mac script.'
    Assert-Condition ($macScriptSource -match '--notary-api-key') 'The Mac release script must accept a notary API key path.'
    Assert-Condition ($macScriptSource -match 'notarytool submit[^\r\n]+--key[^\r\n]+--key-id[^\r\n]+--issuer') 'The Mac release script must notarize with App Store Connect API-key credentials.'
} finally {
    if (Test-Path -LiteralPath $fixtureRoot -PathType Container) {
        $resolvedFixture = [System.IO.Path]::GetFullPath($fixtureRoot)
        $resolvedTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
        if ($resolvedFixture.StartsWith($resolvedTemp, [System.StringComparison]::OrdinalIgnoreCase) -and [System.IO.Path]::GetFileName($resolvedFixture).StartsWith('mobile-egress-desktop-release-test-', [System.StringComparison]::OrdinalIgnoreCase)) {
            Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
        }
    }
}

Write-Host 'Coupled Desktop release checks passed.'
exit 0
