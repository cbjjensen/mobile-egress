$ErrorActionPreference = 'Stop'

$setupScript = Join-Path $PSScriptRoot 'setup-windows-signing.ps1'
. $setupScript

if (-not (Get-Command Assert-CertificateShape -CommandType Function -ErrorAction SilentlyContinue)) {
    throw 'Assertion failed: Windows signing validators were not loaded for isolated behavioral tests.'
}

$failures = [System.Collections.Generic.List[string]]::new()
$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-signing-review-" + [guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $temporaryRoot
$createdCertificates = [System.Collections.Generic.List[System.Security.Cryptography.X509Certificates.X509Certificate2]]::new()

function Add-Failure {
    param([string]$Message)

    $failures.Add($Message)
    Write-Host "RED: $Message"
}

function Remove-TestCertificate {
    param([System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)

    $store = [System.Security.Cryptography.X509Certificates.X509Store]::new(
        'My',
        [System.Security.Cryptography.X509Certificates.StoreLocation]::CurrentUser
    )
    $store.Open([System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
    try {
        $matches = $store.Certificates.Find(
            [System.Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,
            $Certificate.Thumbprint,
            $false
        )
        foreach ($match in $matches) {
            $store.Remove($match)
        }
    } finally {
        $store.Close()
        $store.Dispose()
        $Certificate.Dispose()
    }
}

function Set-TestFileAcl {
    param(
        [string]$Path,
        [string]$DiscretionaryAcl
    )

    $currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $securityDescriptor = "O:${currentUserSid}G:${currentUserSid}D:P$DiscretionaryAcl"
    $acl = [System.Security.AccessControl.FileSecurity]::new()
    $acl.SetSecurityDescriptorSddlForm($securityDescriptor)
    Set-Acl -LiteralPath $Path -AclObject $acl
}

try {
    # A matching certificate-only PKCS#12 must never qualify as recovery material.
    $publicCertificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($certificatePath)
    try {
        $publicOnlyCollection = [System.Security.Cryptography.X509Certificates.X509Certificate2Collection]::new()
        $null = $publicOnlyCollection.Add($publicCertificate)
        $publicOnlyPfxPath = Join-Path $temporaryRoot 'public-only.pfx'
        $publicOnlyPasswordValue = 'non-secret-test-password'
        [System.IO.File]::WriteAllBytes(
            $publicOnlyPfxPath,
            $publicOnlyCollection.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Pkcs12, $publicOnlyPasswordValue)
        )
        $publicOnlyPassword = ConvertTo-SecureString -String $publicOnlyPasswordValue -AsPlainText -Force
        $originalPfxPath = $pfxPath
        $pfxPath = $publicOnlyPfxPath
        try {
            $loaded = Get-PfxIdentityCertificate -Password $publicOnlyPassword
            try {
                Add-Failure 'A matching public-only PFX was accepted without a private key.'
            } finally {
                $loaded.Dispose()
            }
        } catch {
            if ($_.Exception.Message -notmatch '(?i)private key') {
                Add-Failure "Public-only PFX failed for the wrong reason: $($_.Exception.Message)"
            }
        } finally {
            $pfxPath = $originalPfxPath
            $publicOnlyPassword.Dispose()
            $publicOnlyPasswordValue = $null
        }
    } finally {
        $publicCertificate.Dispose()
    }

    # Total validity must remain the ten-year calendar policy, not merely unexpired.
    $shortValidityCertificate = New-SelfSignedCertificate `
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
        -NotAfter (Get-Date).AddYears(5) `
        -TextExtension @(
            '2.5.29.19={critical}{text}ca=false',
            '2.5.29.37={critical}{text}1.3.6.1.5.5.7.3.3'
        )
    $createdCertificates.Add($shortValidityCertificate)
    try {
        Assert-CertificateShape -Certificate $shortValidityCertificate
        Add-Failure 'A five-year publisher certificate was accepted by the ten-year policy.'
    } catch {
        if ($_.Exception.Message -notmatch '(?i)ten-year|validity') {
            Add-Failure "Short-validity certificate failed for the wrong reason: $($_.Exception.Message)"
        }
    }

    # Subject==Issuer is insufficient: the certificate must verify with its own public key.
    $differentSigner = New-SelfSignedCertificate `
        -Type Custom `
        -Subject $expectedSubject `
        -CertStoreLocation 'Cert:\CurrentUser\My' `
        -Provider 'Microsoft Enhanced RSA and AES Cryptographic Provider' `
        -KeyAlgorithm RSA `
        -KeyLength 4096 `
        -HashAlgorithm SHA256 `
        -KeyExportPolicy Exportable `
        -KeySpec Signature `
        -KeyUsage CertSign `
        -NotAfter (Get-Date).AddYears(11) `
        -TextExtension '2.5.29.19={critical}{text}ca=true'
    $createdCertificates.Add($differentSigner)
    $sameDnDifferentKeyCertificate = New-SelfSignedCertificate `
        -Type Custom `
        -Subject $expectedSubject `
        -Signer $differentSigner `
        -CertStoreLocation 'Cert:\CurrentUser\My' `
        -Provider 'Microsoft Enhanced RSA and AES Cryptographic Provider' `
        -KeyAlgorithm RSA `
        -KeyLength 4096 `
        -HashAlgorithm SHA256 `
        -KeyExportPolicy Exportable `
        -KeySpec Signature `
        -KeyUsage DigitalSignature `
        -NotBefore (Get-Date) `
        -NotAfter (Get-Date).AddYears(10) `
        -TextExtension @(
            '2.5.29.19={critical}{text}ca=false',
            '2.5.29.37={critical}{text}1.3.6.1.5.5.7.3.3'
        )
    $createdCertificates.Add($sameDnDifferentKeyCertificate)
    try {
        Assert-CertificateShape -Certificate $sameDnDifferentKeyCertificate
        Add-Failure 'A same-DN certificate signed by a different key was accepted as self-signed.'
    } catch {
        if ($_.Exception.Message -notmatch '(?i)self-signature|self-signed') {
            Add-Failure "Same-DN different-key certificate failed for the wrong reason: $($_.Exception.Message)"
        }
    }

    # The ACL must be an exact three-principal, FullControl, Allow-only descriptor.
    $currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $aclCases = @(
        [pscustomobject]@{
            Name = 'wrong rights'
            Sddl = "(A;;FR;;;${currentUserSid})(A;;FA;;;S-1-5-18)(A;;FA;;;S-1-5-32-544)"
        },
        [pscustomobject]@{
            Name = 'deny rule'
            Sddl = "(D;;FR;;;${currentUserSid})(A;;FA;;;${currentUserSid})(A;;FA;;;S-1-5-18)(A;;FA;;;S-1-5-32-544)"
        }
    )
    foreach ($aclCase in $aclCases) {
        $aclPath = Join-Path $temporaryRoot ("acl-" + $aclCase.Name.Replace(' ', '-') + '.tmp')
        [System.IO.File]::WriteAllText($aclPath, 'non-secret test data')
        Set-TestFileAcl -Path $aclPath -DiscretionaryAcl $aclCase.Sddl
        try {
            Assert-PrivateFileAcl -Path $aclPath
            Add-Failure "An ACL with $($aclCase.Name) was accepted."
        } catch {
            if ($_.Exception.Message -notmatch '(?i)ACL|access|principal|rights|allow|deny|duplicate') {
                Add-Failure "The $($aclCase.Name) ACL failed for the wrong reason: $($_.Exception.Message)"
            }
        }
    }

    $duplicateAclDirectory = Join-Path $temporaryRoot 'acl-duplicate-principal'
    $null = New-Item -ItemType Directory -Path $duplicateAclDirectory
    $duplicateDirectoryAcl = [System.Security.AccessControl.DirectorySecurity]::new()
    $duplicateDirectoryAcl.SetSecurityDescriptorSddlForm(
        "O:${currentUserSid}G:${currentUserSid}D:P(A;;FA;;;${currentUserSid})(A;CI;FA;;;${currentUserSid})(A;;FA;;;S-1-5-18)(A;;FA;;;S-1-5-32-544)"
    )
    Set-Acl -LiteralPath $duplicateAclDirectory -AclObject $duplicateDirectoryAcl
    try {
        Assert-PrivateFileAcl -Path $duplicateAclDirectory
        Add-Failure 'An ACL with a duplicate principal was accepted.'
    } catch {
        if ($_.Exception.Message -notmatch '(?i)ACL|access|principal|rights|allow|deny|duplicate') {
            Add-Failure "The duplicate-principal ACL failed for the wrong reason: $($_.Exception.Message)"
        }
    }

    # A controlled failure after directory preparation must leave any private write protected.
    $protectedDirectory = Join-Path $temporaryRoot 'protected-initialization'
    try {
        Initialize-PrivateSigningDirectory -Path $protectedDirectory
        $controlledFailurePath = Join-Path $protectedDirectory 'controlled-failure.secret'
        [System.IO.File]::WriteAllText($controlledFailurePath, 'non-secret controlled failure fixture')
        throw 'controlled initialization failure'
    } catch {
        if ($_.Exception.Message -eq 'controlled initialization failure') {
            $acl = Get-Acl -LiteralPath $controlledFailurePath
            $actualSids = @($acl.Access | ForEach-Object {
                $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value
            } | Sort-Object -Unique)
            $allowedSids = @($currentUserSid, 'S-1-5-18', 'S-1-5-32-544')
            if (@($actualSids | Where-Object { $allowedSids -notcontains $_ }).Count -gt 0) {
                Add-Failure 'A controlled initialization failure left a private write accessible to an unexpected principal.'
            }
        } else {
            Add-Failure "Private-directory preparation failed before the controlled failure: $($_.Exception.Message)"
        }
    }

    # Git command failures must fail closed rather than looking like empty/untracked output.
    $originalRepositoryRoot = $repositoryRoot
    $repositoryRoot = $temporaryRoot
    function git {
        if ($args[0] -eq 'check-ignore') {
            $global:LASTEXITCODE = 0
            return
        }
        if ($args[0] -eq 'ls-files') {
            $global:LASTEXITCODE = 128
            return
        }
        $global:LASTEXITCODE = 128
    }
    try {
        Assert-GitSecretSafety
        Add-Failure 'A failing git ls-files command was treated as empty untracked output.'
    } catch {
        if ($_.Exception.Message -notmatch '(?i)Git.*failed|ls-files') {
            Add-Failure "Git failure was rejected for the wrong reason: $($_.Exception.Message)"
        }
    } finally {
        Remove-Item Function:\git -ErrorAction SilentlyContinue
        $repositoryRoot = $originalRepositoryRoot
    }
} finally {
    foreach ($createdCertificate in $createdCertificates) {
        Remove-TestCertificate -Certificate $createdCertificate
    }
    if (Test-Path -LiteralPath $temporaryRoot) {
        [System.IO.Directory]::Delete($temporaryRoot, $true)
    }
}

if ($failures.Count -gt 0) {
    throw "Assertion failed: $($failures.Count) Windows signing security review checks failed."
}

Write-Host 'Windows signing security review checks passed.'
exit 0
