[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')]
    [string]$ReleaseVersion,
    [switch]$Publish
)

$ErrorActionPreference = 'Stop'
throw 'Windows desktop releases are coupled with macOS. Use scripts\release-desktop.ps1 with the same -ReleaseVersion and optional -Publish.'
