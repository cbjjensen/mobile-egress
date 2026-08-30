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

$buildScript = Join-Path $PSScriptRoot 'build-windows.ps1'
. $buildScript -ReleaseVersion '0.0.0-test'

Assert-Condition ((Get-Content -Raw -LiteralPath $buildScript).Contains('Setup SHA-256:')) 'Release output must print the final signed setup artifact SHA-256 for separate sharing.'

$identity = Get-WindowsReleaseSigningIdentity
try {
    Assert-Condition ($identity.Certificate.Thumbprint -eq '85F220C1BF05A5D3A86B5DD408787EC1B122ECB7') 'Release signing must discover the established tracked certificate without a thumbprint argument.'
    Assert-Condition ($identity.CertificateSha256 -eq '9FE214C350D7CE04C8EE7F71E169281B50FF0B2A7C5669A348AC10616FB7061F') 'Release signing must preserve the tracked SHA-256 certificate identity.'
} finally {
    $identity.Certificate.Dispose()
    $identity.PublicCertificate.Dispose()
}

$mismatchRejected = $false
try {
    $null = Get-WindowsReleaseSigningIdentity -CodeSigningThumbprint ('0' * 40)
} catch {
    $mismatchRejected = $_.Exception.Message -match 'does not match the tracked'
}
Assert-Condition $mismatchRejected 'The compatibility thumbprint must be optional but reject any value other than the tracked identity.'

$temporaryManifest = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-release-manifest-" + [guid]::NewGuid().ToString('N') + '.json')
try {
    Write-ReleaseUtf8NoBomFile -Path $temporaryManifest -Content '{"version":1}'
    $manifestBytes = [System.IO.File]::ReadAllBytes($temporaryManifest)
    Assert-Condition ($manifestBytes.Length -gt 0) 'The release manifest writer must persist content.'
    Assert-Condition (-not ($manifestBytes.Length -ge 3 -and $manifestBytes[0] -eq 0xEF -and $manifestBytes[1] -eq 0xBB -and $manifestBytes[2] -eq 0xBF)) 'The release manifest must be UTF-8 without a BOM in Windows PowerShell and pwsh.'
} finally {
    Remove-Item -LiteralPath $temporaryManifest -Force -ErrorAction SilentlyContinue
}

Write-Host 'Windows release identity checks passed.'
