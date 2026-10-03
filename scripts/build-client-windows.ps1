[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$')][string]$ReleaseVersion,
    [string]$ServicePath
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'build-windows.ps1') -ReleaseVersion $ReleaseVersion
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$windowsRoot = Join-Path $repositoryRoot 'windows-client'
$clientPackageRoot = Join-Path $windowsRoot "build\release\mobile-egress-client-windows-$ReleaseVersion"
$clientSetupPath = Join-Path $clientPackageRoot 'MobileEgressClientSetup.exe'
$clientPayloadPath = Join-Path $windowsRoot 'internal\setup\payload.zip'
if (Test-Path -LiteralPath $clientPackageRoot) { throw 'Client release output already exists; it must not be overwritten.' }
& (Join-Path $PSScriptRoot 'preflight.ps1') -Components Go
if ($LASTEXITCODE -ne 0) { throw 'Client build preflight failed.' }
$identity = Get-WindowsReleaseSigningIdentity
try {
    $null = New-Item -ItemType Directory -Path $clientPackageRoot
    $clientServicePath = Join-Path $clientPackageRoot 'mobile-egress-client.exe'
    $clientGUIPath = Join-Path $clientPackageRoot 'mobile-egress-client-app.exe'
    if ([string]::IsNullOrWhiteSpace($ServicePath)) {
        go build -trimpath -ldflags "-X main.version=$ReleaseVersion" -o $clientServicePath ./windows-client/cmd/mobile-egress-client
        if ($LASTEXITCODE -ne 0) { throw 'Client service build failed.' }
        Set-WindowsReleaseSignature -Path $clientServicePath -Identity $identity
    } else {
        Assert-WindowsReleaseSignature -Path $ServicePath -Identity $identity
        Copy-Item -LiteralPath $ServicePath -Destination $clientServicePath
    }
    go build -trimpath -tags production -ldflags "-H windowsgui -X main.version=$ReleaseVersion" -o $clientGUIPath ./windows-client/cmd/mobile-egress-client-app
    if ($LASTEXITCODE -ne 0) { throw 'Client graphical application build failed.' }
    Set-WindowsReleaseSignature -Path $clientGUIPath -Identity $identity
    $payloadSources = @($clientGUIPath, $clientServicePath, (Join-Path $repositoryRoot 'windows-signing\mobile-egress-code-signing.cer'), (Join-Path $repositoryRoot 'windows-signing\release-signing-certificate.txt'))
    Compress-Archive -LiteralPath $payloadSources -DestinationPath $clientPayloadPath -Force
    try {
        $flags = "-H windowsgui -X mobile-egress/windows-client/internal/setup.embeddedCertificateBase64=$($identity.CertificateBase64) -X mobile-egress/windows-client/internal/setup.embeddedCertificateFingerprint=$($identity.Fingerprint)"
        go build -trimpath -tags 'client_setup,setup_payload' -ldflags $flags -o $clientSetupPath ./windows-client/cmd/mobile-egress-setup
        if ($LASTEXITCODE -ne 0) { throw 'Self-contained Client setup build failed.' }
        Set-WindowsReleaseSignature -Path $clientSetupPath -Identity $identity
        Copy-Item -LiteralPath $clientPayloadPath -Destination (Join-Path $clientPackageRoot 'payload-verification.zip')
    } finally {
        Remove-Item -LiteralPath $clientPayloadPath -Force -ErrorAction SilentlyContinue
    }
    Write-Host "Signed Client installer: $clientSetupPath"
} finally {
    $identity.Certificate.Dispose()
    $identity.PublicCertificate.Dispose()
}
