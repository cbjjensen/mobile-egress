[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')]
    [string]$ReleaseVersion,
    [switch]$Publish
)

$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'release-all.ps1') -ReleaseVersion $ReleaseVersion -Components 'Windows' -Publish:$Publish
