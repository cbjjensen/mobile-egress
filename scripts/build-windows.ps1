[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$')]
    [string]$ReleaseVersion,
    [Parameter(Mandatory)]
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
$versionLdflags = "-X main.version=$ReleaseVersion"

if ((Test-Path -LiteralPath $packageRoot) -or (Test-Path -LiteralPath $zipPath)) {
    throw "Release output already exists for $ReleaseVersion. Use a new release version or remove the old output explicitly."
}

& (Join-Path $PSScriptRoot 'preflight.ps1') -Components Go, Node, WebView2
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$signTool = Get-ChildItem -Path 'C:\Program Files (x86)\Windows Kits\10\bin' -Filter signtool.exe -Recurse -File -ErrorAction SilentlyContinue |
    Sort-Object FullName -Descending |
    Select-Object -First 1
if ($null -eq $signTool) {
    throw 'signtool.exe is required. Install the Windows SDK signing tools.'
}

$normalizedThumbprint = $CodeSigningThumbprint.ToUpperInvariant()
$signingCertificate = Get-ChildItem -Path 'Cert:\CurrentUser\My', 'Cert:\LocalMachine\My' -ErrorAction SilentlyContinue |
    Where-Object { $_.Thumbprint -eq $normalizedThumbprint } |
    Select-Object -First 1
if ($null -eq $signingCertificate) {
    throw "Code-signing certificate $normalizedThumbprint was not found in the current-user or local-machine certificate store."
}
if (-not $signingCertificate.HasPrivateKey) {
    throw 'The selected code-signing certificate does not have an accessible private key.'
}
if ($signingCertificate.Subject -notlike '*Mobile Egress*') {
    throw "The selected certificate subject must contain 'Mobile Egress' because release identity policy enforces that name."
}
$codeSigningEku = $signingCertificate.EnhancedKeyUsageList | Where-Object { $_.ObjectId.Value -eq '1.3.6.1.5.5.7.3.3' }
if ($null -eq $codeSigningEku) {
    throw 'The selected certificate is not valid for code signing.'
}

$artifacts = @(
    @{ Name = 'mobile-egress-relay.exe'; Package = './relay/cmd/relay' },
    @{ Name = 'mobile-egress-admin.exe'; Package = './windows-client/cmd/mobile-egress-admin' },
    @{ Name = 'mobile-egress-client.exe'; Package = './windows-client/cmd/mobile-egress-client' }
)
New-Item -ItemType Directory -Force -Path $serviceBinRoot | Out-Null
foreach ($artifact in $artifacts) {
    $output = Join-Path $serviceBinRoot $artifact.Name
    go build -trimpath -ldflags $versionLdflags -o $output $artifact.Package
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

$stagedExecutables = @(
    (Join-Path $serviceBinRoot 'mobile-egress-relay.exe'),
    (Join-Path $serviceBinRoot 'mobile-egress-admin.exe'),
    (Join-Path $serviceBinRoot 'mobile-egress-client.exe')
)
foreach ($executable in $stagedExecutables) {
    & $signTool.FullName sign /sha1 $CodeSigningThumbprint /fd SHA256 /tr 'http://timestamp.digicert.com' /td SHA256 $executable
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    & $signTool.FullName verify /pa /all $executable
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

$clientPath = Join-Path $serviceBinRoot 'mobile-egress-client.exe'
$clientDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $clientPath).Hash.ToLowerInvariant()
$manifest = [ordered]@{
    version = 1
    client = [ordered]@{
        version = $ReleaseVersion
        url = "https://github.com/cbjjensen/mobile-egress/releases/download/v$ReleaseVersion/mobile-egress-client.exe"
        sha256 = $clientDigest
        signerThumbprint = $normalizedThumbprint.ToLowerInvariant()
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

foreach ($executable in $stagedExecutables) {
    Copy-Item -Force -LiteralPath $executable -Destination $binRoot
}
$serviceExecutables = @(
    (Join-Path $binRoot 'mobile-egress-relay.exe'),
    (Join-Path $binRoot 'mobile-egress-admin.exe'),
    (Join-Path $binRoot 'mobile-egress-client.exe')
)
$clientPath = Join-Path $binRoot 'mobile-egress-client.exe'
$manifestPath = Join-Path $binRoot 'release-manifest.json'
$manifestJSON | Set-Content -LiteralPath $manifestPath -Encoding utf8NoBOM

$controllerExecutable = Join-Path $binRoot 'mobile-egress-windows.exe'
& $signTool.FullName sign /sha1 $CodeSigningThumbprint /fd SHA256 /tr 'http://timestamp.digicert.com' /td SHA256 $controllerExecutable
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $signTool.FullName verify /pa /all $controllerExecutable
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$executables = @($controllerExecutable) + $serviceExecutables

New-Item -ItemType Directory -Force -Path $packageRoot | Out-Null
Copy-Item -Force -LiteralPath ($executables + $manifestPath) -Destination $packageRoot
Compress-Archive -Force -Path (Join-Path $packageRoot '*') -DestinationPath $zipPath

if ($Installer) {
    Write-Warning 'The self-contained release is the signed ZIP package. The legacy -Installer switch is retained only for command compatibility and does not create a partial NSIS package.'
}

Write-Host "Signed controller package: $zipPath"
Write-Host "Signed headless Client release: $clientPath"
Write-Host "Client SHA-256: $clientDigest"
