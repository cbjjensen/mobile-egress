[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('^[A-Za-z0-9-]{1,100}$')][string]$AppId,
    [Parameter(Mandatory)][ValidatePattern('^[A-Za-z0-9-]{1,100}$')][string]$GroupId,
    [Parameter(Mandatory)][ValidatePattern('^[A-Za-z0-9-]{1,100}$')][string]$BuildId,
    [Parameter(Mandatory)][ValidatePattern('^2\.[0-9]+(?:\.[0-9]+)?$')][string]$ExpectedVersion,
    [Parameter(Mandatory)][ValidatePattern('^[0-9]+(?:\.[0-9]+){0,2}$')][string]$ExpectedBuildNumber,
    [switch]$Apply
)

$ErrorActionPreference = 'Stop'
$repository = Split-Path -Parent $PSScriptRoot
$selection = @{ appId=$AppId; groupId=$GroupId; buildId=$BuildId; version=$ExpectedVersion; buildNumber=$ExpectedBuildNumber }
function Assert-IgnoredPublisherFile([string]$privatePath) {
    git -C $repository check-ignore -q -- $privatePath
    if ($LASTEXITCODE -ne 0) { throw 'Mac publisher configuration and SSH key must be ignored.' }
    $tracked = git -C $repository ls-files -- $privatePath
    if ($LASTEXITCODE -ne 0 -or $tracked) { throw 'Mac publisher configuration and SSH key must remain untracked.' }
}
Assert-IgnoredPublisherFile '.local/mac-build-server/release-desktop.psd1'
. (Join-Path $PSScriptRoot 'release-desktop.ps1')
$config = Get-MobileEgressDesktopConfig -RepositoryRoot $repository -ClientOnly
Assert-IgnoredPublisherFile $config.SshKeyPath
$inputConfig = @{ selection=$selection; apply=$Apply.IsPresent; keyPath=$config.NotaryApiKeyPath; keyId=$config.NotaryApiKeyID; issuer=$config.NotaryApiIssuerID } | ConvertTo-Json -Depth 4 -Compress
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($inputConfig))
$source = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'ios-external-auto-notify.py'))
$remoteScript = "import json,base64`nCONFIG=json.loads(base64.b64decode('$encoded'))`n" + $source
Invoke-MobileEgressDesktopSsh -Context $config -Command '/usr/bin/python3 -' -StandardInputText $remoteScript.Replace("`r", '') -Description 'Verify explicit iOS external-release automatic notifications'
