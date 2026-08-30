param(
    [switch]$Initialize,
    [switch]$ValidateOnly,
    [switch]$Restore
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$signingRoot = Join-Path $repositoryRoot 'windows-signing'
$pfxPath = Join-Path $signingRoot 'mobile-egress-code-signing.pfx'
$propertiesPath = Join-Path $signingRoot 'signing.properties'
$certificatePath = Join-Path $signingRoot 'mobile-egress-code-signing.cer'
$recordPath = Join-Path $signingRoot 'release-signing-certificate.txt'
$expectedSubject = 'CN=Mobile Egress Local Publisher'
$codeSigningOid = '1.3.6.1.5.5.7.3.3'
$privateRelativePaths = @(
    'windows-signing/mobile-egress-code-signing.pfx',
    'windows-signing/signing.properties'
)

if (-not ('MobileEgressCertificateSignature' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class MobileEgressCertificateSignature
{
    [DllImport("crypt32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool CryptVerifyCertificateSignatureEx(
        IntPtr cryptProvider,
        uint encodingType,
        uint subjectType,
        IntPtr subject,
        uint issuerType,
        IntPtr issuer,
        uint flags,
        IntPtr extra
    );
}
'@
}

function Stop-SigningSetup {
    param([string]$Message)

    throw $Message
}

function Get-Sha256Fingerprint {
    param([System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)

    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $hash = $sha256.ComputeHash($Certificate.RawData)
        try {
            return (($hash | ForEach-Object { $_.ToString('X2') }) -join ':')
        } finally {
            [System.Array]::Clear($hash, 0, $hash.Length)
        }
    } finally {
        $sha256.Dispose()
    }
}

function Get-PublicIdentityRecord {
    param([System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)

    @(
        "Subject: $($Certificate.Subject)"
        "SHA-1 thumbprint: $($Certificate.Thumbprint)"
        "SHA-256 fingerprint: $(Get-Sha256Fingerprint -Certificate $Certificate)"
        "Expires: $($Certificate.NotAfter.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
    ) -join [Environment]::NewLine
}

function Get-RecoveryPassword {
    if (-not (Test-Path -LiteralPath $propertiesPath -PathType Leaf)) {
        Stop-SigningSetup 'The private recovery password file is missing. Restore both private recovery files before retrying.'
    }

    $passwordLine = Get-Content -LiteralPath $propertiesPath | Where-Object { $_ -match '^pfxPassword=' } | Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($passwordLine)) {
        Stop-SigningSetup 'The private recovery password file is malformed. Restore the original restricted file.'
    }

    $passwordValue = $passwordLine.Substring('pfxPassword='.Length)
    if ([string]::IsNullOrWhiteSpace($passwordValue)) {
        Stop-SigningSetup 'The private recovery password file is malformed. Restore the original restricted file.'
    }

    try {
        ConvertTo-SecureString -String $passwordValue -AsPlainText -Force
    } finally {
        $passwordValue = $null
        $passwordLine = $null
    }
}

function Get-PfxIdentityCertificate {
    param([securestring]$Password)

    $certificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new(
        $pfxPath,
        $Password,
        [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::DefaultKeySet
    )
    if (-not $certificate.HasPrivateKey) {
        $certificate.Dispose()
        Stop-SigningSetup 'The Windows publisher recovery PFX does not contain a private key.'
    }
    return $certificate
}

function Set-PrivateFileAcl {
    param([string]$Path)

    $currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $allowedSids = @(
        $currentUserSid,
        [System.Security.Principal.SecurityIdentifier]::new('S-1-5-18'),
        [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
    )
    $acl = [System.Security.AccessControl.FileSecurity]::new()
    $acl.SetOwner($currentUserSid)
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in $allowedSids) {
        $rule = [System.Security.AccessControl.FileSystemAccessRule]::new(
            $sid,
            [System.Security.AccessControl.FileSystemRights]::FullControl,
            [System.Security.AccessControl.AccessControlType]::Allow
        )
        $null = $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Assert-PrivateDirectoryAcl {
    param([string]$Path)

    $acl = Get-Acl -LiteralPath $Path
    $currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $allowedSids = @($currentUserSid, 'S-1-5-18', 'S-1-5-32-544')
    if (-not $acl.AreAccessRulesProtected -or
        $acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Value -ne $currentUserSid) {
        Stop-SigningSetup "Private signing directory owner or ACL protection is incorrect: $Path"
    }

    $accessRules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    if ($accessRules.Count -ne 3) {
        Stop-SigningSetup "Private signing directory ACL must contain exactly three access rules: $Path"
    }
    $actualSids = [System.Collections.Generic.List[string]]::new()
    $expectedInheritance = [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
    foreach ($rule in $accessRules) {
        $sid = $rule.IdentityReference.Value
        if ($allowedSids -notcontains $sid -or $actualSids.Contains($sid)) {
            Stop-SigningSetup "Private signing directory ACL contains an unexpected or duplicate principal: $Path"
        }
        $actualSids.Add($sid)
        if ($rule.AccessControlType -ne [System.Security.AccessControl.AccessControlType]::Allow -or
            $rule.FileSystemRights -ne [System.Security.AccessControl.FileSystemRights]::FullControl -or
            $rule.IsInherited -or
            $rule.InheritanceFlags -ne $expectedInheritance -or
            $rule.PropagationFlags -ne [System.Security.AccessControl.PropagationFlags]::None) {
            Stop-SigningSetup "Private signing directory ACL rule semantics are incorrect: $Path"
        }
    }
    foreach ($requiredSid in $allowedSids) {
        if ($actualSids -notcontains $requiredSid) {
            Stop-SigningSetup "Private signing directory ACL is missing a required principal: $Path"
        }
    }
}

function Set-PrivateDirectoryAcl {
    param([string]$Path)

    $currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = [System.Security.AccessControl.DirectorySecurity]::new()
    $acl.SetOwner($currentUserSid)
    $acl.SetAccessRuleProtection($true, $false)
    $inheritance = [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
    foreach ($sid in @(
        $currentUserSid,
        [System.Security.Principal.SecurityIdentifier]::new('S-1-5-18'),
        [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
    )) {
        $rule = [System.Security.AccessControl.FileSystemAccessRule]::new(
            $sid,
            [System.Security.AccessControl.FileSystemRights]::FullControl,
            $inheritance,
            [System.Security.AccessControl.PropagationFlags]::None,
            [System.Security.AccessControl.AccessControlType]::Allow
        )
        $null = $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Initialize-PrivateSigningDirectory {
    param([string]$Path)

    if (Test-Path -LiteralPath $Path) {
        if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
            Stop-SigningSetup "The Windows signing path exists but is not a directory: $Path"
        }
        try {
            Assert-PrivateDirectoryAcl -Path $Path
            return
        } catch {
            Set-PrivateDirectoryAcl -Path $Path
        }
    } else {
        $null = New-Item -ItemType Directory -Path $Path
        Set-PrivateDirectoryAcl -Path $Path
    }
    Assert-PrivateDirectoryAcl -Path $Path
}

function Assert-PrivateFileAcl {
    param([string]$Path)

    $acl = Get-Acl -LiteralPath $Path
    if (-not $acl.AreAccessRulesProtected) {
        Stop-SigningSetup "Private signing file ACL inheritance is enabled: $Path"
    }

    $currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $allowedSids = @($currentUserSid, 'S-1-5-18', 'S-1-5-32-544')
    $ownerSid = $acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Value
    if ($ownerSid -ne $currentUserSid) {
        Stop-SigningSetup "Private signing file ACL owner is not the current user: $Path"
    }

    $accessRules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    if ($accessRules.Count -ne 3) {
        Stop-SigningSetup "Private signing file ACL must contain exactly three non-conflicting access rules: $Path"
    }

    $actualSids = [System.Collections.Generic.List[string]]::new()
    foreach ($rule in $accessRules) {
        $sid = $rule.IdentityReference.Value
        if ($allowedSids -notcontains $sid) {
            Stop-SigningSetup "Private signing file ACL grants an unexpected principal: $Path"
        }
        if ($actualSids.Contains($sid)) {
            Stop-SigningSetup "Private signing file ACL contains a duplicate principal: $Path"
        }
        $actualSids.Add($sid)
        if ($rule.AccessControlType -ne [System.Security.AccessControl.AccessControlType]::Allow) {
            Stop-SigningSetup "Private signing file ACL contains a deny rule: $Path"
        }
        if ($rule.FileSystemRights -ne [System.Security.AccessControl.FileSystemRights]::FullControl) {
            Stop-SigningSetup "Private signing file ACL does not grant exact FullControl rights: $Path"
        }
        if ($rule.IsInherited -or
            $rule.InheritanceFlags -ne [System.Security.AccessControl.InheritanceFlags]::None -or
            $rule.PropagationFlags -ne [System.Security.AccessControl.PropagationFlags]::None) {
            Stop-SigningSetup "Private signing file ACL contains inherited or propagating access: $Path"
        }
    }
    foreach ($requiredSid in $allowedSids) {
        if ($actualSids -notcontains $requiredSid) {
            Stop-SigningSetup "Private signing file ACL is missing a required principal: $Path"
        }
    }
}

function Assert-GitSecretSafety {
    Push-Location $repositoryRoot
    try {
        foreach ($relativePath in $privateRelativePaths) {
            & git check-ignore -q -- $relativePath
            $ignoreExitCode = $LASTEXITCODE
            if ($ignoreExitCode -gt 1) {
                Stop-SigningSetup "Git check-ignore failed while validating private signing material: $relativePath"
            }
            if ($ignoreExitCode -eq 1) {
                Stop-SigningSetup "Private signing file is not ignored by Git: $relativePath"
            }

            $trackedPath = & git ls-files -- $relativePath
            if ($LASTEXITCODE -ne 0) {
                Stop-SigningSetup "Git ls-files failed while validating private signing material: $relativePath"
            }
            if (-not [string]::IsNullOrWhiteSpace($trackedPath)) {
                Stop-SigningSetup "Private signing file is tracked by Git: $relativePath"
            }
        }
    } finally {
        Pop-Location
    }
}

function Assert-CertificateShape {
    param([System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)

    if ($Certificate.Subject -ne $expectedSubject -or $Certificate.Issuer -ne $expectedSubject) {
        Stop-SigningSetup 'The Windows publisher certificate subject or issuer does not match the established identity.'
    }
    $selfSignatureIsValid = [MobileEgressCertificateSignature]::CryptVerifyCertificateSignatureEx(
        [IntPtr]::Zero,
        0x00000001,
        2,
        $Certificate.Handle,
        2,
        $Certificate.Handle,
        0,
        [IntPtr]::Zero
    )
    if (-not $selfSignatureIsValid) {
        Stop-SigningSetup 'The Windows publisher certificate self-signature is invalid.'
    }
    if ($Certificate.NotAfter.ToUniversalTime() -le [DateTime]::UtcNow) {
        Stop-SigningSetup 'The Windows publisher certificate is expired. Stop releases and perform a reviewed publisher replacement.'
    }
    if ($Certificate.NotBefore.ToUniversalTime() -gt [DateTime]::UtcNow) {
        Stop-SigningSetup 'The Windows publisher certificate is not yet valid.'
    }
    $tenYearExpiry = $Certificate.NotBefore.ToUniversalTime().AddYears(10)
    $validityDrift = ($Certificate.NotAfter.ToUniversalTime() - $tenYearExpiry).Duration()
    if ($validityDrift -gt [TimeSpan]::FromMinutes(15)) {
        Stop-SigningSetup 'The Windows publisher certificate does not satisfy the ten-year validity policy.'
    }
    if ($Certificate.SignatureAlgorithm.Value -ne '1.2.840.113549.1.1.11') {
        Stop-SigningSetup 'The Windows publisher certificate is not signed with SHA-256 and RSA.'
    }

    $rsa = [System.Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPublicKey($Certificate)
    try {
        if ($null -eq $rsa -or $rsa.KeySize -ne 4096) {
            Stop-SigningSetup 'The Windows publisher certificate does not use an RSA-4096 key.'
        }
    } finally {
        if ($null -ne $rsa) { $rsa.Dispose() }
    }

    $ekuExtensions = @($Certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.37' })
    if ($ekuExtensions.Count -ne 1) {
        Stop-SigningSetup 'The Windows publisher certificate must contain exactly one EKU extension.'
    }
    $ekuOids = @(([System.Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]$ekuExtensions[0]).EnhancedKeyUsages | ForEach-Object { $_.Value })
    if ($ekuOids.Count -ne 1 -or $ekuOids[0] -ne $codeSigningOid) {
        Stop-SigningSetup 'The Windows publisher certificate must contain only the Code Signing EKU.'
    }

    $basicConstraints = @($Certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.19' })
    if ($basicConstraints.Count -ne 1 -or ([System.Security.Cryptography.X509Certificates.X509BasicConstraintsExtension]$basicConstraints[0]).CertificateAuthority) {
        Stop-SigningSetup 'The Windows publisher certificate must contain CA=false Basic Constraints.'
    }
}

function Get-TrackedCertificate {
    if (-not (Test-Path -LiteralPath $certificatePath -PathType Leaf)) {
        Stop-SigningSetup 'The tracked public Windows publisher certificate is missing.'
    }
    if (-not (Test-Path -LiteralPath $recordPath -PathType Leaf)) {
        Stop-SigningSetup 'The tracked public Windows publisher identity record is missing.'
    }

    $certificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($certificatePath)
    try {
        Assert-CertificateShape -Certificate $certificate
        $expectedRecord = Get-PublicIdentityRecord -Certificate $certificate
        $actualRecord = (Get-Content -Raw -LiteralPath $recordPath).TrimEnd("`r", "`n")
        if ($actualRecord -cne $expectedRecord) {
            Stop-SigningSetup 'The tracked public Windows publisher identity record does not match the public certificate.'
        }
        return [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($certificate.RawData)
    } finally {
        $certificate.Dispose()
    }
}

function Import-PublisherTrust {
    foreach ($storeName in @('Root', 'TrustedPublisher')) {
        $store = [System.Security.Cryptography.X509Certificates.X509Store]::new(
            $storeName,
            [System.Security.Cryptography.X509Certificates.StoreLocation]::CurrentUser
        )
        $store.Open([System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
        try {
            $existing = $store.Certificates.Find(
                [System.Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,
                $script:publisherCertificate.Thumbprint,
                $false
            )
            if ($existing.Count -eq 0) {
                $store.Add($script:publisherCertificate)
            }
        } finally {
            $store.Close()
            $store.Dispose()
        }
    }
}

function Assert-CompleteIdentity {
    Assert-GitSecretSafety
    if (-not (Test-Path -LiteralPath $signingRoot -PathType Container)) {
        Stop-SigningSetup 'The protected Windows signing directory is missing.'
    }
    Assert-PrivateDirectoryAcl -Path $signingRoot
    foreach ($privatePath in @($pfxPath, $propertiesPath)) {
        if (-not (Test-Path -LiteralPath $privatePath -PathType Leaf)) {
            Stop-SigningSetup 'Private Windows publisher recovery files are incomplete. Restore both original restricted files.'
        }
        Assert-PrivateFileAcl -Path $privatePath
    }

    $publicCertificate = Get-TrackedCertificate
    try {
        $password = Get-RecoveryPassword
        try {
            $pfxCertificate = Get-PfxIdentityCertificate -Password $password
        } catch {
            $password.Dispose()
            $password = $null
            Stop-SigningSetup "The encrypted Windows publisher recovery PFX could not be opened with the restricted recovery password ($($_.Exception.GetType().Name): $($_.Exception.Message))."
        }
        $password.Dispose()
        $password = $null
        try {
            if ([Convert]::ToBase64String($publicCertificate.RawData) -cne [Convert]::ToBase64String($pfxCertificate.RawData)) {
                Stop-SigningSetup 'The private recovery PFX does not match the tracked public Windows publisher identity.'
            }
        } finally {
            $pfxCertificate.Dispose()
        }

        $privateCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$($publicCertificate.Thumbprint)" -ErrorAction SilentlyContinue
        if ($null -eq $privateCertificate -or -not $privateCertificate.HasPrivateKey) {
            Stop-SigningSetup 'CurrentUser\My does not contain the established Windows publisher certificate with its private key. Use -Restore.'
        }
        Assert-CertificateShape -Certificate $privateCertificate

        foreach ($storeName in @('Root', 'TrustedPublisher')) {
            $trustedCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\$storeName\$($publicCertificate.Thumbprint)" -ErrorAction SilentlyContinue
            if ($null -eq $trustedCertificate) {
                Stop-SigningSetup "The established Windows publisher certificate is not trusted in CurrentUser\$storeName. Use -Restore."
            }
        }

        $chain = [System.Security.Cryptography.X509Certificates.X509Chain]::new()
        try {
            $chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
            $chain.ChainPolicy.VerificationFlags = [System.Security.Cryptography.X509Certificates.X509VerificationFlags]::NoFlag
            if (-not $chain.Build($publicCertificate)) {
                Stop-SigningSetup 'Windows chain validation does not trust the established publisher certificate.'
            }
        } finally {
            $chain.Dispose()
        }

        return $publicCertificate
    } catch {
        $publicCertificate.Dispose()
        throw
    }
}

function Invoke-Initialize {
    Assert-GitSecretSafety

    $existingFiles = @(@($pfxPath, $propertiesPath, $certificatePath, $recordPath) | Where-Object { Test-Path -LiteralPath $_ })
    $existingCertificates = @(@('My', 'Root', 'TrustedPublisher') | ForEach-Object {
        Get-ChildItem -LiteralPath "Cert:\CurrentUser\$_" | Where-Object { $_.Subject -eq $expectedSubject }
    })
    if ($existingFiles.Count -gt 0 -or @($existingCertificates).Count -gt 0) {
        Stop-SigningSetup 'The Mobile Egress Windows publisher identity already exists or is partially present. Reuse it with -ValidateOnly, or recover the established private files with -Restore; initialization will not replace it.'
    }

    Initialize-PrivateSigningDirectory -Path $signingRoot
    $notAfter = (Get-Date).AddYears(10)
    $certificate = New-SelfSignedCertificate `
        -Type Custom `
        -Subject $expectedSubject `
        -CertStoreLocation 'Cert:\CurrentUser\My' `
        -Provider 'Microsoft Enhanced RSA and AES Cryptographic Provider' `
        -KeyAlgorithm RSA `
        -KeyLength 4096 `
        -HashAlgorithm SHA256 `
        -KeyExportPolicy Exportable `
        -KeySpec Signature `
        -KeyUsage DigitalSignature `
        -NotAfter $notAfter `
        -TextExtension @(
            '2.5.29.19={critical}{text}ca=false',
            '2.5.29.37={critical}{text}1.3.6.1.5.5.7.3.3'
        )

    $passwordBytes = New-Object byte[] 48
    $random = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($passwordBytes)
        $passwordValue = [Convert]::ToBase64String($passwordBytes)
    } finally {
        $random.Dispose()
        [System.Array]::Clear($passwordBytes, 0, $passwordBytes.Length)
    }
    $securePassword = ConvertTo-SecureString -String $passwordValue -AsPlainText -Force

    try {
        $null = Export-PfxCertificate -Cert $certificate -FilePath $pfxPath -Password $securePassword -ChainOption EndEntityCertOnly -NoProperties
    } finally {
        $securePassword.Dispose()
        $securePassword = $null
    }
    [System.IO.File]::WriteAllText($propertiesPath, "pfxPassword=$passwordValue$([Environment]::NewLine)", [System.Text.UTF8Encoding]::new($false))
    $passwordValue = $null
    foreach ($privatePath in @($pfxPath, $propertiesPath)) {
        try {
            Assert-PrivateFileAcl -Path $privatePath
        } catch {
            Set-PrivateFileAcl -Path $privatePath
            Assert-PrivateFileAcl -Path $privatePath
        }
    }

    $null = Export-Certificate -Cert $certificate -FilePath $certificatePath -Type CERT
    [System.IO.File]::WriteAllText($recordPath, "$(Get-PublicIdentityRecord -Certificate $certificate)$([Environment]::NewLine)", [System.Text.UTF8Encoding]::new($false))

    $script:publisherCertificate = $certificate
    Import-PublisherTrust
    $validatedCertificate = Assert-CompleteIdentity
    try {
        Write-Host 'Initialized the reusable Mobile Egress Windows publisher identity.'
        Write-Host "Subject: $($validatedCertificate.Subject)"
        Write-Host "SHA-1 thumbprint: $($validatedCertificate.Thumbprint)"
        Write-Host "SHA-256 fingerprint: $(Get-Sha256Fingerprint -Certificate $validatedCertificate)"
        Write-Host "Expires: $($validatedCertificate.NotAfter.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
    } finally {
        $validatedCertificate.Dispose()
    }
}

function Invoke-Restore {
    Assert-GitSecretSafety
    foreach ($path in @($pfxPath, $propertiesPath, $certificatePath, $recordPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            Stop-SigningSetup 'Restore requires the original PFX, password file, public certificate, and public identity record.'
        }
    }

    Initialize-PrivateSigningDirectory -Path $signingRoot
    foreach ($privatePath in @($pfxPath, $propertiesPath)) {
        try {
            Assert-PrivateFileAcl -Path $privatePath
        } catch {
            Set-PrivateFileAcl -Path $privatePath
            Assert-PrivateFileAcl -Path $privatePath
        }
    }
    $publicCertificate = Get-TrackedCertificate
    $password = $null
    try {
        $password = Get-RecoveryPassword
        try {
            $pfxCertificate = Get-PfxIdentityCertificate -Password $password
        } catch {
            Stop-SigningSetup "Restore refused the recovery PFX because it could not be opened with the restricted recovery password ($($_.Exception.GetType().Name): $($_.Exception.Message))."
        }
        try {
            if ([Convert]::ToBase64String($publicCertificate.RawData) -cne [Convert]::ToBase64String($pfxCertificate.RawData)) {
                Stop-SigningSetup 'Restore refused the recovery PFX because it does not match the tracked public Windows publisher identity.'
            }
        } finally {
            $pfxCertificate.Dispose()
        }

        $subjectCollisions = Get-ChildItem -LiteralPath 'Cert:\CurrentUser\My' | Where-Object {
            $_.Subject -eq $expectedSubject -and $_.Thumbprint -ne $publicCertificate.Thumbprint
        }
        if (@($subjectCollisions).Count -gt 0) {
            Stop-SigningSetup 'Restore found a different CurrentUser publisher certificate with the reserved subject and will not replace it.'
        }

        $privateCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$($publicCertificate.Thumbprint)" -ErrorAction SilentlyContinue
        if ($null -eq $privateCertificate -or -not $privateCertificate.HasPrivateKey) {
            $null = Import-PfxCertificate -FilePath $pfxPath -Password $password -CertStoreLocation 'Cert:\CurrentUser\My' -Exportable
        }

        $script:publisherCertificate = $publicCertificate
        Import-PublisherTrust
    } finally {
        if ($null -ne $password) {
            $password.Dispose()
            $password = $null
        }
        $publicCertificate.Dispose()
    }

    $validatedCertificate = Assert-CompleteIdentity
    try {
        Write-Host 'Restored and validated the established Mobile Egress Windows publisher identity.'
        Write-Host "SHA-1 thumbprint: $($validatedCertificate.Thumbprint)"
        Write-Host "SHA-256 fingerprint: $(Get-Sha256Fingerprint -Certificate $validatedCertificate)"
    } finally {
        $validatedCertificate.Dispose()
    }
}

if ($MyInvocation.InvocationName -eq '.') {
    return
}

$modeCount = @($Initialize.IsPresent, $ValidateOnly.IsPresent, $Restore.IsPresent).Where({ $_ }).Count
if ($modeCount -ne 1) {
    Write-Error 'Choose exactly one mode: -Initialize, -ValidateOnly, or -Restore.'
    exit 2
}

try {
    if ($Initialize) {
        Invoke-Initialize
    } elseif ($Restore) {
        Invoke-Restore
    } else {
        $validatedCertificate = Assert-CompleteIdentity
        try {
            Write-Host 'The reusable Mobile Egress Windows publisher identity is valid.'
            Write-Host "SHA-1 thumbprint: $($validatedCertificate.Thumbprint)"
            Write-Host "SHA-256 fingerprint: $(Get-Sha256Fingerprint -Certificate $validatedCertificate)"
            Write-Host "Expires: $($validatedCertificate.NotAfter.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
        } finally {
            $validatedCertificate.Dispose()
        }
    }
    exit 0
} catch {
    Write-Error $_.Exception.Message
    exit 10
}
