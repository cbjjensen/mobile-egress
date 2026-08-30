[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$')]
    [string]$ReleaseVersion,
    [ValidatePattern('^[0-9A-Fa-f]{40}$')]
    [string]$CodeSigningThumbprint,
    [switch]$Installer
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$windowsRoot = Join-Path $repositoryRoot 'windows-client'
$binRoot = Join-Path $windowsRoot 'build\bin'
$serviceBinRoot = Join-Path $windowsRoot 'build\service-bin'
$packageRoot = Join-Path $windowsRoot "build\release\mobile-egress-windows-$ReleaseVersion"
$zipPath = "$packageRoot.zip"
$timestampServer = 'http://timestamp.digicert.com'

function Get-CertificateSha256 {
    param([System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)

    $hasher = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($hasher.ComputeHash($Certificate.RawData))).Replace('-', '')
    } finally {
        $hasher.Dispose()
    }
}

function Get-WindowsReleaseSigningIdentity {
    param([string]$CodeSigningThumbprint)

    $setupScript = Join-Path $PSScriptRoot 'setup-windows-signing.ps1'
    & $setupScript -ValidateOnly *> $null
    if ($LASTEXITCODE -ne 0) {
        throw 'The established Windows publisher identity did not validate. Restore it; never initialize a replacement to fix a release.'
    }

    $publicCertificatePath = Join-Path $repositoryRoot 'windows-signing\mobile-egress-code-signing.cer'
    $publicCertificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($publicCertificatePath)
    try {
        $trackedThumbprint = $publicCertificate.Thumbprint.ToUpperInvariant()
        if (-not [string]::IsNullOrWhiteSpace($CodeSigningThumbprint) -and $CodeSigningThumbprint.ToUpperInvariant() -ne $trackedThumbprint) {
            throw "The supplied code-signing thumbprint does not match the tracked publisher identity ($trackedThumbprint)."
        }

        $privateCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$trackedThumbprint" -ErrorAction SilentlyContinue
        if ($null -eq $privateCertificate -or -not $privateCertificate.HasPrivateKey) {
            throw 'The established Windows publisher private key is unavailable. Use setup-windows-signing.ps1 -Restore; never create a replacement.'
        }
        if ([Convert]::ToBase64String($privateCertificate.RawData) -cne [Convert]::ToBase64String($publicCertificate.RawData)) {
            throw 'The CurrentUser signing certificate does not exactly match the tracked public certificate.'
        }

        return [pscustomobject]@{
            Certificate         = $privateCertificate
            PublicCertificate   = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($publicCertificate.RawData)
            Thumbprint          = $trackedThumbprint
            CertificateSha256   = Get-CertificateSha256 -Certificate $publicCertificate
            Fingerprint         = ((Get-CertificateSha256 -Certificate $publicCertificate) -replace '(..)(?!$)', '$1:')
            CertificateBase64   = [Convert]::ToBase64String($publicCertificate.RawData)
        }
    } finally {
        $publicCertificate.Dispose()
    }
}

function Assert-WindowsReleaseSignature {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [Parameter(Mandatory)]
        [pscustomobject]$Identity
    )

    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
        throw "Authenticode status is not Valid for $(Split-Path -Leaf $Path): $($signature.Status)."
    }
    if ($null -eq $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint.ToUpperInvariant() -ne $Identity.Thumbprint) {
        throw "Authenticode signer thumbprint does not match the tracked publisher for $(Split-Path -Leaf $Path)."
    }
    if ((Get-CertificateSha256 -Certificate $signature.SignerCertificate) -ne $Identity.CertificateSha256) {
        throw "Authenticode signer SHA-256 identity does not match the tracked publisher for $(Split-Path -Leaf $Path)."
    }
    if ([Convert]::ToBase64String($signature.SignerCertificate.RawData) -cne [Convert]::ToBase64String($Identity.PublicCertificate.RawData)) {
        throw "Authenticode signer certificate bytes do not match the tracked publisher for $(Split-Path -Leaf $Path)."
    }
    if ($null -eq $signature.TimeStamperCertificate) {
        throw "Authenticode timestamp is missing for $(Split-Path -Leaf $Path)."
    }
}

function Set-WindowsReleaseSignature {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [Parameter(Mandatory)]
        [pscustomobject]$Identity
    )

    $null = Set-AuthenticodeSignature `
        -LiteralPath $Path `
        -Certificate $Identity.Certificate `
        -HashAlgorithm SHA256 `
        -TimestampServer $timestampServer
    Assert-WindowsReleaseSignature -Path $Path -Identity $Identity
}

function Write-ReleaseUtf8NoBomFile {
    param(
        [Parameter(Mandatory)]
        [string]$Path,
        [Parameter(Mandatory)]
        [string]$Content
    )

    [System.IO.File]::WriteAllText($Path, $Content, [System.Text.UTF8Encoding]::new($false))
}

if ($MyInvocation.InvocationName -eq '.') {
    return
}

if ((Test-Path -LiteralPath $packageRoot) -or (Test-Path -LiteralPath $zipPath)) {
    throw "Release output already exists for $ReleaseVersion. Use a new release version or remove the old output explicitly."
}

& (Join-Path $PSScriptRoot 'preflight.ps1') -Components Go, Node, WebView2
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$identity = Get-WindowsReleaseSigningIdentity -CodeSigningThumbprint $CodeSigningThumbprint
try {
    $versionLdflags = "-X main.version=$ReleaseVersion"
    $artifacts = @(
        @{ Name = 'mobile-egress-relay.exe'; Package = './relay/cmd/relay'; Ldflags = $versionLdflags },
        @{ Name = 'mobile-egress-admin.exe'; Package = './windows-client/cmd/mobile-egress-admin'; Ldflags = $versionLdflags },
        @{ Name = 'mobile-egress-client.exe'; Package = './windows-client/cmd/mobile-egress-client'; Ldflags = $versionLdflags },
        @{
            Name = 'MobileEgressSetup.exe'
            Package = './windows-client/cmd/mobile-egress-setup'
            Ldflags = "-H windowsgui -X mobile-egress/windows-client/internal/setup.embeddedCertificateBase64=$($identity.CertificateBase64) -X mobile-egress/windows-client/internal/setup.embeddedCertificateFingerprint=$($identity.Fingerprint)"
        }
    )
    $null = New-Item -ItemType Directory -Force -Path $serviceBinRoot
    foreach ($artifact in $artifacts) {
        $output = Join-Path $serviceBinRoot $artifact.Name
        go build -trimpath -ldflags $artifact.Ldflags -o $output $artifact.Package
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        Set-WindowsReleaseSignature -Path $output -Identity $identity
    }

    $clientPath = Join-Path $serviceBinRoot 'mobile-egress-client.exe'
    $clientDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $clientPath).Hash.ToLowerInvariant()
    $manifest = [ordered]@{
        version = 1
        client = [ordered]@{
            version = $ReleaseVersion
            url = "https://github.com/cbjjensen/mobile-egress/releases/download/v$ReleaseVersion/mobile-egress-client.exe"
            sha256 = $clientDigest
            signerThumbprint = $identity.Thumbprint.ToLowerInvariant()
        }
    }
    $manifestJSON = $manifest | ConvertTo-Json -Compress -Depth 4
    $manifestBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($manifestJSON))

    Push-Location $windowsRoot
    try {
        $controllerLdflags = "-X mobile-egress/windows-client/internal/desktop.embeddedReleaseManifestBase64=$manifestBase64"
        go run github.com/wailsapp/wails/v2/cmd/wails@v2.14.0 build -clean -ldflags $controllerLdflags
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    } finally {
        Pop-Location
    }

    $controllerExecutable = Join-Path $binRoot 'mobile-egress-windows.exe'
    Set-WindowsReleaseSignature -Path $controllerExecutable -Identity $identity

    $stagedExecutables = @(
        (Join-Path $serviceBinRoot 'MobileEgressSetup.exe'),
        (Join-Path $serviceBinRoot 'mobile-egress-admin.exe'),
        (Join-Path $serviceBinRoot 'mobile-egress-relay.exe'),
        (Join-Path $serviceBinRoot 'mobile-egress-client.exe')
    )
    foreach ($executable in $stagedExecutables) {
        Copy-Item -Force -LiteralPath $executable -Destination $binRoot
    }

    $manifestPath = Join-Path $binRoot 'release-manifest.json'
    Write-ReleaseUtf8NoBomFile -Path $manifestPath -Content $manifestJSON
    $executables = @($controllerExecutable) + @($stagedExecutables | ForEach-Object { Join-Path $binRoot (Split-Path -Leaf $_) })
    foreach ($executable in $executables) {
        Assert-WindowsReleaseSignature -Path $executable -Identity $identity
    }
    $setupDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $binRoot 'MobileEgressSetup.exe')).Hash.ToLowerInvariant()

    $publicCertificatePath = Join-Path $repositoryRoot 'windows-signing\mobile-egress-code-signing.cer'
    $publicRecordPath = Join-Path $repositoryRoot 'windows-signing\release-signing-certificate.txt'
    $null = New-Item -ItemType Directory -Force -Path $packageRoot
    Copy-Item -Force -LiteralPath ($executables + $manifestPath + $publicCertificatePath + $publicRecordPath) -Destination $packageRoot
    Compress-Archive -Force -Path (Join-Path $packageRoot '*') -DestinationPath $zipPath

    if ($Installer) {
        Write-Warning 'The self-contained release is the signed ZIP package. The legacy -Installer switch is retained only for command compatibility and does not create a partial NSIS package.'
    }

    Write-Host "Signed guided setup package: $zipPath"
    Write-Host "Signed headless Client release: $(Join-Path $binRoot 'mobile-egress-client.exe')"
    Write-Host "Setup SHA-256: $setupDigest"
    Write-Host "Client SHA-256: $clientDigest"
    Write-Host "Publisher SHA-256 fingerprint: $($identity.Fingerprint)"
} finally {
    $identity.Certificate.Dispose()
    $identity.PublicCertificate.Dispose()
}
