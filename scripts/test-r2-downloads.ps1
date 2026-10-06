#Requires -Version 7.0
param([switch]$IsolatedTestProcess)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not $IsolatedTestProcess) {
    & (Get-Process -Id $PID).Path -NoProfile -File $PSCommandPath -IsolatedTestProcess
    if ($LASTEXITCODE -ne 0) { throw 'Isolated R2 wrapper tests failed.' }
    return
}

$directory = Join-Path ([IO.Path]::GetTempPath()) ('mobile-egress-r2-wrapper-test-' + [guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $directory
$plan = Join-Path $directory 'plan.json'
$config = Join-Path $directory 'config.json'
Set-Content -LiteralPath $plan -Value '{}' -Encoding utf8
$wrapper = Join-Path $PSScriptRoot 'publish-local-mobile-egress-downloads.ps1'

# Replace only the external Node process in this isolated test. No R2/GitHub calls or real secrets.
$global:R2WrapperFixture = @{ Calls = @(); FailNode = $false }
function node {
    param([Parameter(ValueFromRemainingArguments)][string[]]$Arguments)
    $global:R2WrapperFixture.Calls += ,$Arguments
    if ($Arguments -contains '--publish') {
        if ($env:AWS_ACCESS_KEY_ID -ne 'fixture-key' -or $env:AWS_SECRET_ACCESS_KEY -ne 'fixture-secret') { throw 'Fixture environment was not forwarded.' }
        if ($env:R2_BUCKET -ne 'order-tracker-downloads') { throw 'Fixture bucket was not forwarded.' }
    }
    $global:LASTEXITCODE = if ($global:R2WrapperFixture.FailNode) { 7 } else { 0 }
}
try {
    & $wrapper -PlanPath $plan -ConfigPath (Join-Path $directory 'missing-config.json') -DryRun
    if ($global:R2WrapperFixture.Calls.Count -ne 1 -or $global:R2WrapperFixture.Calls[0] -notcontains '--dry-run') { throw 'Dry-run did not avoid the credential file.' }
    try { & $wrapper -PlanPath $plan -Publish; throw 'Missing pilot confirmation was accepted.' }
    catch { if ($_.Exception.Message -notmatch 'explicit -Publish -Pilot') { throw } }
    if ($global:R2WrapperFixture.Calls.Count -ne 1) { throw 'Unconfirmed publish reached Node.' }

    # Session credentials intentionally supply fields omitted from the fixture config.
    $env:AWS_ACCESS_KEY_ID = 'fixture-key'
    $env:AWS_SECRET_ACCESS_KEY = 'fixture-secret'
    $env:R2_ACCOUNT_ID = 'before-account'
    $env:R2_BUCKET = 'before-bucket'
    $env:R2_PUBLIC_BASE_URL = 'before-origin'
    @{ r2AccountId = ('a' * 32); r2Bucket = 'order-tracker-downloads'; r2PublicBaseUrl = 'https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev' } |
        ConvertTo-Json | Set-Content -LiteralPath $config -Encoding utf8
    $global:R2WrapperFixture.FailNode = $true
    try { & $wrapper -PlanPath $plan -ConfigPath $config -Publish -Pilot; throw 'Failed child process was accepted.' }
    catch { if ($_.Exception.Message -notmatch 'publication stopped') { throw } }
    if ($env:R2_ACCOUNT_ID -ne 'before-account' -or $env:R2_BUCKET -ne 'before-bucket' -or $env:R2_PUBLIC_BASE_URL -ne 'before-origin' -or $env:AWS_ACCESS_KEY_ID -ne 'fixture-key' -or $env:AWS_SECRET_ACCESS_KEY -ne 'fixture-secret') { throw 'Wrapper failed to restore environment after failure.' }
    $global:LASTEXITCODE = 0
    'PASS: R2 wrapper offline dry-run, publish confirmation, credential fallback and environment restoration.'
} finally {
    # Only this newly created test directory is removed; no release staging is changed.
    if (([IO.Path]::GetFullPath($directory)).StartsWith([IO.Path]::GetFullPath([IO.Path]::GetTempPath())) -and (Split-Path $directory -Leaf).StartsWith('mobile-egress-r2-wrapper-test-')) {
        Remove-Item -LiteralPath $directory -Recurse -Force
    }
}
