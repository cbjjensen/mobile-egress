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

    $value = Get-JsonProperty -Object $Entry -Name $PropertyName
    if ($null -eq $value -or @($value).Count -eq 0) {
        Add-ManifestError "$FeatureId/$Platform missing $PropertyName"
        return
    }

    foreach ($path in @($value)) {
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

if ((Get-JsonProperty -Object $manifest -Name 'schemaVersion') -ne 1) {
    Add-ManifestError 'schemaVersion must be 1'
}

$features = @(Get-JsonProperty -Object $manifest -Name 'features')
if ($features.Count -eq 0) {
    Add-ManifestError 'features must contain at least one entry'
}

$seenFeatureIds = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
foreach ($feature in $features) {
    $featureId = Get-JsonProperty -Object $feature -Name 'id'
    if ($featureId -isnot [string] -or [string]::IsNullOrWhiteSpace($featureId)) {
        Add-ManifestError 'feature is missing id'
        $featureId = '<missing-id>'
    } elseif (-not $seenFeatureIds.Add($featureId)) {
        Add-ManifestError "duplicate feature id: $featureId"
    }

    if ((Get-JsonProperty -Object $feature -Name 'title') -isnot [string] -or [string]::IsNullOrWhiteSpace((Get-JsonProperty -Object $feature -Name 'title'))) {
        Add-ManifestError "$featureId missing title"
    }

    $platforms = Get-JsonProperty -Object $feature -Name 'platforms'
    foreach ($platform in $requiredPlatforms) {
        if (-not (Test-JsonProperty -Object $platforms -Name $platform)) {
            Add-ManifestError "$featureId missing platform: $platform"
            continue
        }

        $entry = Get-JsonProperty -Object $platforms -Name $platform
        $status = Get-JsonProperty -Object $entry -Name 'status'
        if ($allowedStatuses -notcontains $status) {
            Add-ManifestError "$featureId/$platform has unsupported status: $status"
        }

        $nativeEquivalenceNotes = Get-JsonProperty -Object $entry -Name 'nativeEquivalenceNotes'
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
