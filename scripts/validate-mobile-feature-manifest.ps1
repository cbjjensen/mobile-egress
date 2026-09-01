[CmdletBinding()]
param(
    [string]$RepositoryRoot = (Split-Path -Parent $PSScriptRoot),
    [string]$ManifestPath = ''
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($ManifestPath)) {
    $ManifestPath = Join-Path $RepositoryRoot 'docs/mobile-feature-manifest.json'
}

$allowedStatuses = @('implemented', 'native-equivalent')
$requiredPlatforms = @('android', 'ios')
$errors = [System.Collections.Generic.List[string]]::new()
$featureIdPattern = '^[a-z0-9]+(?:[.-][a-z0-9]+)*$'

function Add-ManifestError {
    param([string]$Message)
    $errors.Add($Message)
}

function Test-JsonProperty {
    param(
        [object]$Object,
        [string]$Name
    )

    if ($null -eq $Object) {
        return $false
    }

    return $null -ne $Object.PSObject.Properties[$Name]
}

function Get-JsonProperty {
    param(
        [object]$Object,
        [string]$Name
    )

    if (-not (Test-JsonProperty -Object $Object -Name $Name)) {
        return $null
    }

    return $Object.PSObject.Properties[$Name].Value
}

function Test-JsonObject {
    param([object]$Value)

    return $Value -is [pscustomobject]
}

function Test-SchemaVersion {
    param([object]$Value)

    return ($Value -is [int] -or $Value -is [long]) -and [int64]$Value -eq 1
}

function Test-StringValue {
    param([object]$Value)

    return $Value -is [string] -and -not [string]::IsNullOrWhiteSpace($Value)
}

function Test-UnexpectedProperties {
    param(
        [object]$Object,
        [string]$Context,
        [string[]]$AllowedProperties
    )

    if (-not (Test-JsonObject -Value $Object)) {
        return
    }

    foreach ($property in $Object.PSObject.Properties.Name) {
        if ($AllowedProperties -notcontains $property) {
            Add-ManifestError "$Context has unexpected property: $property"
        }
    }
}

function Test-TrackedEvidencePath {
    param(
        [string]$RepositoryRoot,
        [string]$RelativePath
    )

    if ([string]::IsNullOrWhiteSpace($RelativePath)) {
        return $false
    }
    if ([System.IO.Path]::IsPathRooted($RelativePath)) {
        return $false
    }

    $fullPath = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot $RelativePath))
    $fullRoot = [System.IO.Path]::GetFullPath($RepositoryRoot)
    if (-not $fullPath.StartsWith($fullRoot, [StringComparison]::OrdinalIgnoreCase)) {
        return $false
    }
    if (-not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
        return $false
    }

    & git -C $RepositoryRoot ls-files --error-unmatch -- $RelativePath *> $null
    return $LASTEXITCODE -eq 0
}

function Test-EvidenceList {
    param(
        [object]$Entry,
        [string]$FeatureId,
        [string]$Platform,
        [string]$PropertyName,
        [string]$RepositoryRoot
    )

    if (-not (Test-JsonProperty -Object $Entry -Name $PropertyName)) {
        Add-ManifestError "$FeatureId/$Platform missing $PropertyName"
        return
    }

    $value = $Entry.PSObject.Properties[$PropertyName].Value
    if ($value -isnot [System.Array]) {
        Add-ManifestError "$FeatureId/$Platform $PropertyName must be an array"
        return
    }

    if ($value.Count -eq 0) {
        Add-ManifestError "$FeatureId/$Platform missing $PropertyName"
        return
    }

    foreach ($path in $value) {
        if ($path -isnot [string] -or [string]::IsNullOrWhiteSpace($path)) {
            Add-ManifestError "$FeatureId/$Platform $PropertyName contains an empty evidence path"
            continue
        }
        if (-not (Test-TrackedEvidencePath -RepositoryRoot $RepositoryRoot -RelativePath $path)) {
            Add-ManifestError "$FeatureId/$Platform has untracked evidence: $path"
        }
    }
}

try {
    $resolvedRoot = (Resolve-Path -LiteralPath $RepositoryRoot -ErrorAction Stop).Path
    $resolvedManifest = (Resolve-Path -LiteralPath $ManifestPath -ErrorAction Stop).Path
    $manifest = Get-Content -Raw -LiteralPath $resolvedManifest | ConvertFrom-Json
} catch {
    Write-Host "ERROR: unable to read mobile feature manifest: $($_.Exception.Message)"
    exit 1
}

if (-not (Test-JsonObject -Value $manifest)) {
    Add-ManifestError 'root must be an object'
} else {
    Test-UnexpectedProperties -Object $manifest -Context 'root' -AllowedProperties @('$schema', 'schemaVersion', 'features')
}

if (Test-JsonProperty -Object $manifest -Name '$schema') {
    $schemaReference = Get-JsonProperty -Object $manifest -Name '$schema'
    if (-not (Test-StringValue -Value $schemaReference)) {
        Add-ManifestError '$schema must be a non-empty string'
    }
}

$schemaVersion = Get-JsonProperty -Object $manifest -Name 'schemaVersion'
if (-not (Test-SchemaVersion -Value $schemaVersion)) {
    Add-ManifestError 'schemaVersion must be 1'
}

if (-not (Test-JsonProperty -Object $manifest -Name 'features')) {
    Add-ManifestError 'root missing features'
    $features = @()
    $featuresValue = $null
} else {
    $featuresValue = $manifest.PSObject.Properties['features'].Value
    if ($featuresValue -isnot [System.Array]) {
        Add-ManifestError 'features must be an array'
        $features = @()
    } else {
        $features = $featuresValue
    }
}

if ($features.Count -eq 0 -and $null -ne $featuresValue -and $featuresValue -is [System.Array]) {
    Add-ManifestError 'features must contain at least one entry'
}

$seenFeatureIds = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
foreach ($feature in $features) {
    if (-not (Test-JsonObject -Value $feature)) {
        Add-ManifestError 'feature entry must be an object'
        continue
    }

    $featureId = Get-JsonProperty -Object $feature -Name 'id'
    if (-not (Test-StringValue -Value $featureId)) {
        Add-ManifestError 'feature is missing id'
        $featureId = '<missing-id>'
    } elseif ($featureId -notmatch $featureIdPattern) {
        Add-ManifestError "feature id does not match schema pattern: $featureId"
        $null = $seenFeatureIds.Add($featureId)
    } elseif (-not $seenFeatureIds.Add($featureId)) {
        Add-ManifestError "duplicate feature id: $featureId"
    }
    Test-UnexpectedProperties -Object $feature -Context $featureId -AllowedProperties @('id', 'title', 'platforms')

    if (-not (Test-StringValue -Value (Get-JsonProperty -Object $feature -Name 'title'))) {
        Add-ManifestError "$featureId missing title"
    }

    $platforms = Get-JsonProperty -Object $feature -Name 'platforms'
    if ($null -eq $platforms) {
        Add-ManifestError "$featureId missing platforms"
        continue
    }
    if (-not (Test-JsonObject -Value $platforms)) {
        Add-ManifestError "$featureId platforms must be an object"
        continue
    }
    Test-UnexpectedProperties -Object $platforms -Context "$featureId platforms" -AllowedProperties $requiredPlatforms

    foreach ($platform in $requiredPlatforms) {
        if (-not (Test-JsonProperty -Object $platforms -Name $platform)) {
            Add-ManifestError "$featureId missing platform: $platform"
            continue
        }

        $entry = Get-JsonProperty -Object $platforms -Name $platform
        if (-not (Test-JsonObject -Value $entry)) {
            Add-ManifestError "$featureId/$platform must be an object"
            continue
        }
        Test-UnexpectedProperties -Object $entry -Context "$featureId/$platform" -AllowedProperties @('status', 'nativeEquivalenceNotes', 'sourceEvidence', 'testEvidence')

        $status = Get-JsonProperty -Object $entry -Name 'status'
        if ($allowedStatuses -notcontains $status) {
            Add-ManifestError "$featureId/$platform has unsupported status: $status"
        }

        $hasNativeEquivalenceNotes = Test-JsonProperty -Object $entry -Name 'nativeEquivalenceNotes'
        $nativeEquivalenceNotes = Get-JsonProperty -Object $entry -Name 'nativeEquivalenceNotes'
        if ($hasNativeEquivalenceNotes -and $nativeEquivalenceNotes -isnot [string]) {
            Add-ManifestError "$featureId/$platform nativeEquivalenceNotes must be a string"
        }
        if ($status -eq 'native-equivalent' -and ($nativeEquivalenceNotes -isnot [string] -or [string]::IsNullOrWhiteSpace($nativeEquivalenceNotes))) {
            Add-ManifestError "$featureId/$platform native-equivalent status requires nativeEquivalenceNotes"
        }
        if ($status -eq 'implemented' -and $nativeEquivalenceNotes -is [string] -and -not [string]::IsNullOrWhiteSpace($nativeEquivalenceNotes)) {
            Add-ManifestError "$featureId/$platform implemented status must not use nativeEquivalenceNotes"
        }

        Test-EvidenceList -Entry $entry -FeatureId $featureId -Platform $platform -PropertyName 'sourceEvidence' -RepositoryRoot $resolvedRoot
        Test-EvidenceList -Entry $entry -FeatureId $featureId -Platform $platform -PropertyName 'testEvidence' -RepositoryRoot $resolvedRoot
    }
}

foreach ($message in $errors) {
    Write-Host "ERROR: $message"
}

if ($errors.Count -gt 0) {
    exit 1
}

Write-Host 'Mobile feature manifest validation passed.'
exit 0
