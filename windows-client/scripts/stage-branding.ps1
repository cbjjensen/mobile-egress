$ErrorActionPreference = 'Stop'

$clientRoot = Split-Path -Parent $PSScriptRoot
$logoPng = Join-Path $clientRoot 'frontend\public\zfnf-logo.png'
$logoIco = Join-Path $clientRoot 'internal\desktop\zfnf-logo.ico'
$buildRoot = Join-Path $clientRoot 'build'
$windowsBuildRoot = Join-Path $buildRoot 'windows'

if (-not (Test-Path -LiteralPath $logoPng -PathType Leaf)) {
    throw "Missing Windows logo PNG: $logoPng"
}
if (-not (Test-Path -LiteralPath $logoIco -PathType Leaf)) {
    throw "Missing Windows logo ICO: $logoIco"
}

$pngBytes = [IO.File]::ReadAllBytes($logoPng)
$pngSignature = [byte[]](137, 80, 78, 71, 13, 10, 26, 10)
if ($pngBytes.Length -lt $pngSignature.Length) {
    throw "Windows logo PNG is invalid: $logoPng"
}
for ($index = 0; $index -lt $pngSignature.Length; $index++) {
    if ($pngBytes[$index] -ne $pngSignature[$index]) {
        throw "Windows logo PNG is invalid: $logoPng"
    }
}

$icoBytes = [IO.File]::ReadAllBytes($logoIco)
if ($icoBytes.Length -lt 6 -or
    [BitConverter]::ToUInt16($icoBytes, 0) -ne 0 -or
    [BitConverter]::ToUInt16($icoBytes, 2) -ne 1 -or
    [BitConverter]::ToUInt16($icoBytes, 4) -lt 5) {
    throw "Windows logo ICO must contain at least five icon sizes: $logoIco"
}

$null = New-Item -ItemType Directory -Force -Path $windowsBuildRoot
Copy-Item -LiteralPath $logoPng -Destination (Join-Path $buildRoot 'appicon.png') -Force
Copy-Item -LiteralPath $logoIco -Destination (Join-Path $windowsBuildRoot 'icon.ico') -Force

Write-Host 'Staged ZFNF branding for the Windows build.'
