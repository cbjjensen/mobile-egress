param(
    [switch]$InitializeIdentity,
    [switch]$RestoreExistingIdentity,
    [switch]$PwshPfxCompatibility
)

$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$setupScript = Join-Path $PSScriptRoot 'setup-windows-signing.ps1'
$signingRoot = Join-Path $repositoryRoot 'windows-signing'
$pfxPath = Join-Path $signingRoot 'mobile-egress-code-signing.pfx'
$propertiesPath = Join-Path $signingRoot 'signing.properties'
$certificatePath = Join-Path $signingRoot 'mobile-egress-code-signing.cer'
$recordPath = Join-Path $signingRoot 'release-signing-certificate.txt'
$expectedSubject = 'CN=Mobile Egress Local Publisher'
$codeSigningOid = '1.3.6.1.5.5.7.3.3'

function Assert-Condition {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw "Assertion failed: $Message"
    }
}

function Invoke-Setup {
    param(
        [string]$Mode,
        [string]$PowerShellCommand = 'powershell'
    )

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $PowerShellCommand -NoProfile -File $setupScript $Mode *>&1 | Out-String
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    [pscustomobject]@{
        ExitCode = $exitCode
        Output = $output
    }
}

function Get-Sha256Fingerprint {
    param([System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)

    $hash = [System.Security.Cryptography.SHA256]::Create().ComputeHash($Certificate.RawData)
    try {
        return (($hash | ForEach-Object { $_.ToString('X2') }) -join ':')
    } finally {
        [System.Array]::Clear($hash, 0, $hash.Length)
    }
}

function Assert-PrivateFileAcl {
    param([string]$Path)

    $acl = Get-Acl -LiteralPath $Path
    Assert-Condition $acl.AreAccessRulesProtected "$Path must not inherit access rules."

    $currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $allowedSids = @($currentUserSid, 'S-1-5-18', 'S-1-5-32-544')
    $actualSids = @($acl.Access | ForEach-Object {
        $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value
    } | Sort-Object -Unique)

    foreach ($requiredSid in $allowedSids) {
        Assert-Condition ($actualSids -contains $requiredSid) "$Path must grant access to $requiredSid."
    }
    foreach ($actualSid in $actualSids) {
        Assert-Condition ($allowedSids -contains $actualSid) "$Path grants access to unexpected identity $actualSid."
    }
}

Assert-Condition (Test-Path -LiteralPath $setupScript -PathType Leaf) 'The Windows signing setup script is missing.'

if ($PwshPfxCompatibility) {
    $pwshValidation = Invoke-Setup -Mode '-ValidateOnly' -PowerShellCommand 'pwsh'
    Assert-Condition ($pwshValidation.Output -notmatch '(?i)immutable|equivalent constructor') 'pwsh must load the encrypted recovery PFX without using the removed mutable X509Certificate import API.'
    Assert-Condition (
        $pwshValidation.ExitCode -eq 0 -or ($pwshValidation.ExitCode -eq 10 -and $pwshValidation.Output -match 'CurrentUser\\Root'),
        "pwsh validation did not reach certificate-store validation: $($pwshValidation.Output)"
    )
    Assert-Condition ($pwshValidation.Output -notmatch '(?i)pfxPassword|password\s*=') 'pwsh validation output exposed a private property.'
    Write-Host 'pwsh PFX compatibility check passed.'
    exit 0
}

if ($InitializeIdentity) {
    $initializeResult = Invoke-Setup -Mode '-Initialize'
    Assert-Condition ($initializeResult.ExitCode -eq 0) "Initialization failed: $($initializeResult.Output)"
    Assert-Condition ($initializeResult.Output -notmatch '(?i)pfxPassword|password\s*=') 'Initialization output exposed a private property.'
}

if ($RestoreExistingIdentity) {
    $restoreResult = Invoke-Setup -Mode '-Restore'
    Assert-Condition ($restoreResult.ExitCode -eq 0) "Restoring the established identity failed: $($restoreResult.Output)"
    Assert-Condition ($restoreResult.Output -notmatch '(?i)pfxPassword|password\s*=') 'Restore output exposed a private property.'
}

$validationResult = Invoke-Setup -Mode '-ValidateOnly'
Assert-Condition ($validationResult.ExitCode -eq 0) "Validation failed: $($validationResult.Output)"
Assert-Condition ($validationResult.Output -notmatch '(?i)pfxPassword|password\s*=') 'Validation output exposed a private property.'

foreach ($requiredPath in @($pfxPath, $propertiesPath, $certificatePath, $recordPath)) {
    Assert-Condition (Test-Path -LiteralPath $requiredPath -PathType Leaf) "Required signing identity file is missing: $requiredPath"
}

Assert-PrivateFileAcl -Path $pfxPath
Assert-PrivateFileAcl -Path $propertiesPath

$certificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($certificatePath)
try {
    Assert-Condition ($certificate.Subject -eq $expectedSubject) 'The public certificate subject is incorrect.'
    Assert-Condition ($certificate.Issuer -eq $expectedSubject) 'The publisher certificate must be self-signed.'
    Assert-Condition ($certificate.SignatureAlgorithm.Value -eq '1.2.840.113549.1.1.11') 'The certificate signature algorithm must be SHA-256 with RSA.'
    Assert-Condition ($certificate.NotAfter.ToUniversalTime() -gt [DateTime]::UtcNow.AddYears(9)) 'The certificate must retain approximately ten years of validity at initialization.'

    $rsa = [System.Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPublicKey($certificate)
    try {
        Assert-Condition ($rsa.KeySize -eq 4096) 'The publisher key must be RSA-4096.'
    } finally {
        if ($null -ne $rsa) { $rsa.Dispose() }
    }

    $ekuOids = @($certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.37' } | ForEach-Object {
        ([System.Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]$_).EnhancedKeyUsages | ForEach-Object { $_.Value }
    })
    Assert-Condition ($ekuOids.Count -eq 1 -and $ekuOids[0] -eq $codeSigningOid) 'The publisher certificate must contain only the Code Signing EKU.'

    $basicConstraints = $certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.19' } | Select-Object -First 1
    Assert-Condition ($null -ne $basicConstraints) 'The publisher certificate must contain Basic Constraints.'
    Assert-Condition (-not ([System.Security.Cryptography.X509Certificates.X509BasicConstraintsExtension]$basicConstraints).CertificateAuthority) 'The publisher certificate must have CA=false.'

    $privateCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$($certificate.Thumbprint)" -ErrorAction SilentlyContinue
    Assert-Condition ($null -ne $privateCertificate -and $privateCertificate.HasPrivateKey) 'CurrentUser\My must contain the exact publisher certificate with its private key.'

    foreach ($storeName in @('Root', 'TrustedPublisher')) {
        $trustedCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\$storeName\$($certificate.Thumbprint)" -ErrorAction SilentlyContinue
        Assert-Condition ($null -ne $trustedCertificate) "CurrentUser\$storeName must trust the exact publisher certificate."
    }

    $record = Get-Content -Raw -LiteralPath $recordPath
    $expectedSha256 = Get-Sha256Fingerprint -Certificate $certificate
    $expectedExpiry = $certificate.NotAfter.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    Assert-Condition ($record -match "(?m)^Subject: $([regex]::Escape($expectedSubject))`r?$") 'The public identity record subject drifted.'
    Assert-Condition ($record -match "(?m)^SHA-1 thumbprint: $($certificate.Thumbprint)`r?$") 'The public identity record SHA-1 thumbprint drifted.'
    Assert-Condition ($record -match "(?m)^SHA-256 fingerprint: $([regex]::Escape($expectedSha256))`r?$") 'The public identity record SHA-256 fingerprint drifted.'
    Assert-Condition ($record -match "(?m)^Expires: $([regex]::Escape($expectedExpiry))`r?$") 'The public identity record expiry drifted.'
} finally {
    $certificate.Dispose()
}

Push-Location $repositoryRoot
try {
    foreach ($privateRelativePath in @('windows-signing/mobile-egress-code-signing.pfx', 'windows-signing/signing.properties')) {
        & git check-ignore -q -- $privateRelativePath
        Assert-Condition ($LASTEXITCODE -eq 0) "$privateRelativePath must remain ignored."
        $trackedPath = & git ls-files -- $privateRelativePath
        Assert-Condition ([string]::IsNullOrWhiteSpace($trackedPath)) "$privateRelativePath must never be tracked."
    }
} finally {
    Pop-Location
}

$duplicateResult = Invoke-Setup -Mode '-Initialize'
Assert-Condition ($duplicateResult.ExitCode -ne 0) 'Duplicate initialization must be refused.'
Assert-Condition ($duplicateResult.Output -match '(?i)already exists|restore') 'Duplicate initialization must explain how to reuse the existing identity.'
Assert-Condition ($duplicateResult.Output -notmatch '(?i)pfxPassword|password\s*=') 'Duplicate-initialization output exposed a private property.'

Write-Host 'Windows signing identity checks passed.'
exit 0
