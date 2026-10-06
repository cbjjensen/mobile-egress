#Requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$PlanPath,
    [switch]$DryRun,
    [switch]$Publish,
    [switch]$Pilot,
    [string]$ConfigPath = (Join-Path $env:USERPROFILE '.order-tracker/desktop-downloads.json')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($Publish -and $DryRun) { throw 'Choose DryRun or Publish.' }
if ($Publish -and -not $Pilot) { throw 'Publication requires explicit -Publish -Pilot.' }
$resolvedPlan = (Resolve-Path -LiteralPath $PlanPath).Path
$publisher = Join-Path $PSScriptRoot 'mobile-egress-downloads.mjs'

# Dry-run never opens the shared credential file or contacts GitHub/R2.
if (-not $Publish) {
    & node $publisher --plan $resolvedPlan --dry-run
    if ($LASTEXITCODE -ne 0) { throw 'Mobile Egress local download validation failed.' }
    return
}

if (-not (Test-Path -LiteralPath $ConfigPath -PathType Leaf)) {
    throw 'Existing Order Tracker R2 configuration is missing. Restore it or provide ConfigPath; do not create another bucket.'
}
try { $config = Get-Content -LiteralPath $ConfigPath -Raw | ConvertFrom-Json -AsHashtable }
catch { throw 'Existing R2 configuration must be valid JSON. Its contents are not logged.' }
$values = @{
    R2_ACCOUNT_ID = $config['r2AccountId']
    R2_BUCKET = $config['r2Bucket']
    R2_PUBLIC_BASE_URL = $config['r2PublicBaseUrl']
    AWS_ACCESS_KEY_ID = $config['awsAccessKeyId']
    AWS_SECRET_ACCESS_KEY = $config['awsSecretAccessKey']
}
foreach ($name in @('AWS_ACCESS_KEY_ID', 'AWS_SECRET_ACCESS_KEY')) {
    if (-not $values[$name]) { $values[$name] = [Environment]::GetEnvironmentVariable($name) }
}
foreach ($name in $values.Keys) {
    if ([string]::IsNullOrWhiteSpace([string]$values[$name])) { throw "Required R2 setting is missing: $name" }
}
$previous = @{}
foreach ($name in $values.Keys) { $previous[$name] = [Environment]::GetEnvironmentVariable($name) }
try {
    foreach ($name in $values.Keys) { [Environment]::SetEnvironmentVariable($name, [string]$values[$name], 'Process') }
    & node $publisher --plan $resolvedPlan --publish --pilot
    if ($LASTEXITCODE -ne 0) { throw 'Mobile Egress R2 publication stopped; inspect the reported object/evidence before retrying.' }
} finally {
    foreach ($name in $previous.Keys) { [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process') }
}
