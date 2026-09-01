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

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$validator = Join-Path $PSScriptRoot 'validate-mobile-feature-manifest.ps1'
Assert-Condition (Test-Path -LiteralPath $validator -PathType Leaf) 'The mobile feature manifest validator must exist.'

function New-TestFeature {
    return [ordered]@{
        id = 'agent.enrollment'
        title = 'Strict mobile Agent enrollment'
        platforms = [ordered]@{
            android = [ordered]@{
                status = 'implemented'
                sourceEvidence = @('android/app/src/main/java/com/example/Enrollment.kt')
                testEvidence = @('android/app/src/test/java/com/example/EnrollmentTest.kt')
            }
            ios = [ordered]@{
                status = 'native-equivalent'
                nativeEquivalenceNotes = 'Uses Secure Enclave and shared Keychain storage in place of Android Keystore while preserving non-exportable P-256 enrollment identity.'
                sourceEvidence = @('ios/Sources/MobileEgressCore/Enrollment/EnrollmentRepository.swift')
                testEvidence = @('ios/Tests/MobileEgressCoreTests/EnrollmentTests.swift')
            }
        }
    }
}

function Invoke-ManifestValidatorFixture {
    param(
        [scriptblock]$MutateManifest = {},
        [scriptblock]$MutateFiles = {}
    )

    $fixtureRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-manifest-test-" + [guid]::NewGuid().ToString('N'))
    try {
        $null = New-Item -ItemType Directory -Path $fixtureRoot
        & git -C $fixtureRoot init -q
        if ($LASTEXITCODE -ne 0) {
            throw 'Fixture git repository initialization failed.'
        }

        $trackedPaths = @(
            'android/app/src/main/java/com/example/Enrollment.kt',
            'android/app/src/test/java/com/example/EnrollmentTest.kt',
            'ios/Sources/MobileEgressCore/Enrollment/EnrollmentRepository.swift',
            'ios/Tests/MobileEgressCoreTests/EnrollmentTests.swift'
        )
        foreach ($relativePath in $trackedPaths) {
            $absolutePath = Join-Path $fixtureRoot $relativePath
            $parent = Split-Path -Parent $absolutePath
            if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
                $null = New-Item -ItemType Directory -Path $parent
            }
            Set-Content -LiteralPath $absolutePath -Encoding Ascii -Value 'tracked evidence fixture'
        }

        & git -C $fixtureRoot add .
        if ($LASTEXITCODE -ne 0) {
            throw 'Fixture evidence staging failed.'
        }

        $manifest = [ordered]@{
            schemaVersion = 1
            features = @((New-TestFeature))
        }
        & $MutateManifest $manifest $fixtureRoot
        & $MutateFiles $fixtureRoot

        $manifestPath = Join-Path $fixtureRoot 'docs/mobile-feature-manifest.json'
        $manifestDirectory = Split-Path -Parent $manifestPath
        if (-not (Test-Path -LiteralPath $manifestDirectory -PathType Container)) {
            $null = New-Item -ItemType Directory -Path $manifestDirectory
        }
        $manifest | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $manifestPath -Encoding Ascii
        & git -C $fixtureRoot add docs/mobile-feature-manifest.json
        if ($LASTEXITCODE -ne 0) {
            throw 'Fixture manifest staging failed.'
        }

        $output = & $validator -RepositoryRoot $fixtureRoot -ManifestPath $manifestPath *>&1 | Out-String
        return [pscustomobject]@{
            ExitCode = $LASTEXITCODE
            Output = $output
        }
    } finally {
        if (Test-Path -LiteralPath $fixtureRoot) {
            $resolvedFixture = [System.IO.Path]::GetFullPath($fixtureRoot)
            $resolvedTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
            if ($resolvedFixture.StartsWith($resolvedTemp, [StringComparison]::OrdinalIgnoreCase) -and [System.IO.Path]::GetFileName($resolvedFixture).StartsWith('mobile-egress-manifest-test-', [StringComparison]::OrdinalIgnoreCase)) {
                Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
            }
        }
    }
}

$valid = Invoke-ManifestValidatorFixture
Assert-Condition ($valid.ExitCode -eq 0) 'A schema-v1 manifest with tracked Android and iOS evidence must pass.'
Assert-Condition ($valid.Output -match 'Mobile feature manifest validation passed') 'A valid manifest must report success.'

$duplicate = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $duplicateFeature = New-TestFeature
    $duplicateFeature.title = 'Duplicate ID fixture'
    $Manifest.features += $duplicateFeature
}
Assert-Condition ($duplicate.ExitCode -eq 1) 'Duplicate feature IDs must fail validation.'
Assert-Condition ($duplicate.Output -match 'duplicate feature id: agent\.enrollment') 'Duplicate feature diagnostics must name the duplicate ID.'

$unsupported = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.features[0].platforms.android.status = 'planned'
}
Assert-Condition ($unsupported.ExitCode -eq 1) 'Unsupported platform statuses must fail validation.'
Assert-Condition ($unsupported.Output -match 'unsupported status') 'Unsupported status diagnostics must be explicit.'

$missingEvidence = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.features[0].platforms.ios.testEvidence = @()
}
Assert-Condition ($missingEvidence.ExitCode -eq 1) 'Missing source or test evidence must fail validation.'
Assert-Condition ($missingEvidence.Output -match 'missing testEvidence') 'Missing evidence diagnostics must name the empty evidence field.'

$untrackedEvidence = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.features[0].platforms.android.sourceEvidence = @('android/app/src/main/java/com/example/Untracked.kt')
} -MutateFiles {
    param($FixtureRoot)
    $untrackedPath = Join-Path $FixtureRoot 'android/app/src/main/java/com/example/Untracked.kt'
    Set-Content -LiteralPath $untrackedPath -Encoding Ascii -Value 'untracked fixture'
}
Assert-Condition ($untrackedEvidence.ExitCode -eq 1) 'Untracked evidence paths must fail validation.'
Assert-Condition ($untrackedEvidence.Output -match 'untracked evidence') 'Untracked evidence diagnostics must be explicit.'

$invalidNativeEquivalence = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.features[0].platforms.ios.nativeEquivalenceNotes = '   '
}
Assert-Condition ($invalidNativeEquivalence.ExitCode -eq 1) 'Native-equivalent platform entries must explain the native equivalence.'
Assert-Condition ($invalidNativeEquivalence.Output -match 'native-equivalent status requires nativeEquivalenceNotes') 'Invalid native-equivalence diagnostics must be explicit.'

$singlePlatform = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.features[0].platforms.Remove('ios')
}
Assert-Condition ($singlePlatform.ExitCode -eq 1) 'Single-platform feature entries must fail validation.'
Assert-Condition ($singlePlatform.Output -match 'missing platform: ios') 'Single-platform diagnostics must name the missing platform.'

$scalarFeatures = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest['features'] = 'agent.enrollment'
}
Assert-Condition ($scalarFeatures.ExitCode -eq 1) 'The features collection must fail validation when it is not an array.'
Assert-Condition ($scalarFeatures.Output -match 'features must be an array') 'Scalar feature diagnostics must reject coercion into a one-item collection.'

$scalarEvidence = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.features[0].platforms.android.sourceEvidence = 'android/app/src/main/java/com/example/Enrollment.kt'
}
Assert-Condition ($scalarEvidence.ExitCode -eq 1) 'Evidence fields must fail validation when they are not arrays.'
Assert-Condition ($scalarEvidence.Output -match 'agent\.enrollment/android sourceEvidence must be an array') 'Scalar evidence diagnostics must name the platform evidence field.'

$invalidIdPattern = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.features[0].id = 'Agent Enrollment'
}
Assert-Condition ($invalidIdPattern.ExitCode -eq 1) 'Feature IDs outside the lowercase schema pattern must fail validation.'
Assert-Condition ($invalidIdPattern.Output -match 'feature id does not match schema pattern: Agent Enrollment') 'Invalid ID diagnostics must include the rejected ID.'

$unexpectedProperties = Invoke-ManifestValidatorFixture -MutateManifest {
    param($Manifest)
    $Manifest.Add('unexpectedRoot', $true)
    $Manifest.features[0].Add('unexpectedFeature', $true)
    $Manifest.features[0].platforms.Add('windows', [ordered]@{ status = 'implemented'; sourceEvidence = @('windows.cs'); testEvidence = @('windows.test.cs') })
    $Manifest.features[0].platforms.android.Add('unexpectedEntry', $true)
}
Assert-Condition ($unexpectedProperties.ExitCode -eq 1) 'Unexpected root, feature, platforms, and platform-entry properties must fail validation.'
Assert-Condition ($unexpectedProperties.Output -match 'root has unexpected property: unexpectedRoot') 'Unexpected root property diagnostics must name the property.'
Assert-Condition ($unexpectedProperties.Output -match 'agent\.enrollment has unexpected property: unexpectedFeature') 'Unexpected feature property diagnostics must name the property.'
Assert-Condition ($unexpectedProperties.Output -match 'agent\.enrollment platforms has unexpected property: windows') 'Unexpected platforms property diagnostics must name the property.'
Assert-Condition ($unexpectedProperties.Output -match 'agent\.enrollment/android has unexpected property: unexpectedEntry') 'Unexpected platform-entry property diagnostics must name the property.'

$actualManifestOutput = & $validator -RepositoryRoot $repositoryRoot *>&1 | Out-String
Assert-Condition ($LASTEXITCODE -eq 0) 'The checked-in mobile feature manifest must pass validation.'
Assert-Condition ($actualManifestOutput -match 'Mobile feature manifest validation passed') 'The checked-in manifest must report validation success.'

Write-Host 'Mobile feature manifest validator checks passed.'
exit 0
