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

$identity = Get-WindowsReleaseSigningIdentity
try {
    Assert-Condition ($identity.Certificate.Thumbprint -eq '85F220C1BF05A5D3A86B5DD408787EC1B122ECB7') 'Release signing must discover the established tracked certificate without a thumbprint argument.'
    Assert-Condition ($identity.CertificateSha256 -eq '9FE214C350D7CE04C8EE7F71E169281B50FF0B2A7C5669A348AC10616FB7061F') 'Release signing must preserve the tracked SHA-256 certificate identity.'

    $manifestJSON = New-NodeReleaseManifestJson `
        -ReleaseVersion '1.2.3' `
        -ClientSHA256 '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' `
        -Identity $identity
    $manifest = $manifestJSON | ConvertFrom-Json
    $trackedCertificateBase64 = [Convert]::ToBase64String($identity.PublicCertificate.RawData)
    Assert-Condition ($manifest.version -eq 2) 'The embedded node-release manifest must use version 2.'
    Assert-Condition ($manifest.client.signerThumbprint -ceq $identity.Thumbprint.ToLowerInvariant()) 'Manifest v2 must carry the tracked certificate SHA-1 thumbprint.'
    Assert-Condition ($manifest.client.signerCertificateSha256 -ceq $identity.CertificateSha256.ToLowerInvariant()) 'Manifest v2 must carry the tracked certificate SHA-256 fingerprint in lowercase.'
    Assert-Condition ($manifest.client.signerCertificateBase64 -ceq $trackedCertificateBase64) 'Manifest v2 must embed the tracked CER bytes, not a separately downloaded certificate.'
    Assert-Condition ([Convert]::FromBase64String($manifest.client.signerCertificateBase64).Length -le 16384) 'Manifest v2 certificate DER must stay within the Go validation bound.'
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
    Write-ReleaseUtf8NoBomFile -Path $temporaryManifest -Content '{"version":2}'
    $manifestBytes = [System.IO.File]::ReadAllBytes($temporaryManifest)
    Assert-Condition ($manifestBytes.Length -gt 0) 'The release manifest writer must persist content.'
    Assert-Condition (-not ($manifestBytes.Length -ge 3 -and $manifestBytes[0] -eq 0xEF -and $manifestBytes[1] -eq 0xBB -and $manifestBytes[2] -eq 0xBF)) 'The release manifest must be UTF-8 without a BOM in Windows PowerShell and pwsh.'
} finally {
    Remove-Item -LiteralPath $temporaryManifest -Force -ErrorAction SilentlyContinue
}

Write-Host 'Windows release identity checks passed.'
