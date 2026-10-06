[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$generator = Join-Path $PSScriptRoot 'generate-brand-assets.ps1'
$fixtureRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-brand-assets-" + [guid]::NewGuid().ToString('N'))
$sourceDirectory = Join-Path $fixtureRoot 'assets\branding'
$sourcePath = Join-Path $sourceDirectory 'zfnf-logo-source.png'
$powerShellExecutable = Join-Path $PSHOME 'pwsh.exe'
Add-Type -AssemblyName System.Drawing

function Assert-Condition {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw "Assertion failed: $Message"
    }
}

function Invoke-Generator {
    param([switch]$Check)

    $arguments = @('-NoProfile', '-File', $generator, '-RepositoryRoot', $fixtureRoot)
    if ($Check) {
        $arguments += '-Check'
    }
    $output = & $powerShellExecutable @arguments *>&1 | Out-String
    return [pscustomobject]@{
        ExitCode = $LASTEXITCODE
        Output = $output
    }
}

function Get-PngDimension {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][int]$Offset
    )

    $bytes = [System.IO.File]::ReadAllBytes($Path)
    return [System.Net.IPAddress]::NetworkToHostOrder([BitConverter]::ToInt32($bytes, $Offset))
}

function Assert-SixStoneColors {
    param([string]$Path)

    $bitmap = [System.Drawing.Bitmap]::new($Path)
    $found = [bool[]]::new(6)
    try {
        for ($y = 0; $y -lt $bitmap.Height; $y += 4) {
            for ($x = 0; $x -lt $bitmap.Width; $x += 4) {
                $pixel = $bitmap.GetPixel($x, $y)
                if ($pixel.A -lt 64 -or $pixel.GetSaturation() -lt 0.6) { continue }
                $hue = $pixel.GetHue()
                if ($hue -ge 260 -and $hue -le 310) { $found[0] = $true }
                if ($hue -ge 205 -and $hue -le 250) { $found[1] = $true }
                if ($hue -ge 95 -and $hue -le 155) { $found[2] = $true }
                if ($hue -ge 45 -and $hue -le 70) { $found[3] = $true }
                if ($hue -ge 15 -and $hue -lt 45) { $found[4] = $true }
                if ($hue -le 15 -or $hue -ge 345) { $found[5] = $true }
            }
        }
        Assert-Condition (-not ($found -contains $false)) "All six stone colors must survive in $Path."
    } finally {
        $bitmap.Dispose()
    }
}

function Assert-TransparentMask {
    param([string]$Path, [switch]$Monochrome, [switch]$AdaptiveSafeRegion)

    $bitmap = [System.Drawing.Bitmap]::new($Path)
    try {
        Assert-Condition ($bitmap.GetPixel(0, 0).A -eq 0) "The black matte must be transparent in $Path."
        $center = ($bitmap.Width - 1) / 2.0
        $safeRadius = $bitmap.Width * 33 / 108
        $visible = 0
        for ($y = 0; $y -lt $bitmap.Height; $y++) {
            for ($x = 0; $x -lt $bitmap.Width; $x++) {
                $pixel = $bitmap.GetPixel($x, $y)
                if ($pixel.A -eq 0) { continue }
                $visible++
                if ($Monochrome) {
                    Assert-Condition ($pixel.R -eq 255 -and $pixel.G -eq 255 -and $pixel.B -eq 255) "System masks must contain only white and alpha: $Path."
                }
                if ($AdaptiveSafeRegion -and $pixel.A -gt 24) {
                    $radiusSquared = ($x - $center) * ($x - $center) + ($y - $center) * ($y - $center)
                    Assert-Condition ($radiusSquared -le $safeRadius * $safeRadius) "Artwork leaves the circular Android adaptive safe region in $Path at ($x, $y)."
                }
            }
        }
        Assert-Condition ($visible -gt 0) "The generated mask must not be empty: $Path."
    } finally {
        $bitmap.Dispose()
    }
}

try {
    $null = New-Item -ItemType Directory -Path $sourceDirectory
    Copy-Item -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'assets\branding\zfnf-logo-source.png') -Destination $sourcePath
    Copy-Item -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'assets\branding\inevitable-mobile-relay-logo-source.png') -Destination (Join-Path $sourceDirectory 'inevitable-mobile-relay-logo-source.png')

    $generated = Invoke-Generator
    Assert-Condition ($generated.ExitCode -eq 0) "Brand generation failed: $($generated.Output)"

    $iconPath = Join-Path $fixtureRoot 'ios\Assets\AppAssets.xcassets\AppIcon.appiconset\MobileEgressAppIcon.png'
    $headerPath = Join-Path $fixtureRoot 'ios\Assets\AppAssets.xcassets\ZFNFHeader.imageset\ZFNFHeader.png'
    Assert-Condition (Test-Path -LiteralPath $iconPath -PathType Leaf) 'The iOS AppIcon must be generated.'
    Assert-Condition (Test-Path -LiteralPath $headerPath -PathType Leaf) 'The iOS header image must be generated.'
    Assert-Condition ((Get-PngDimension -Path $iconPath -Offset 16) -eq 1024) 'The iOS AppIcon width must be 1024 pixels.'
    Assert-Condition ((Get-PngDimension -Path $iconPath -Offset 20) -eq 1024) 'The iOS AppIcon height must be 1024 pixels.'
    Assert-Condition (([System.IO.File]::ReadAllBytes($iconPath))[25] -eq 2) 'The iOS AppIcon must be opaque RGB.'
    Assert-Condition ((Get-PngDimension -Path $headerPath -Offset 16) -eq 256) 'The iOS header width must be 256 pixels.'
    Assert-Condition ((Get-PngDimension -Path $headerPath -Offset 20) -eq 256) 'The iOS header height must be 256 pixels.'
    Assert-Condition (([System.IO.File]::ReadAllBytes($headerPath))[25] -eq 6) 'The iOS header must preserve transparency.'

    Assert-SixStoneColors -Path $iconPath
    Assert-SixStoneColors -Path $headerPath
    Assert-TransparentMask -Path $headerPath
    $foregroundPath = Join-Path $fixtureRoot 'android\app\src\main\res\drawable-xxxhdpi\ic_mobile_egress_foreground.png'
    $monochromePath = Join-Path $fixtureRoot 'android\app\src\main\res\drawable-xxxhdpi\ic_mobile_egress_monochrome.png'
    $notificationPath = Join-Path $fixtureRoot 'android\app\src\main\res\drawable-xxxhdpi\ic_mobile_egress_notification.png'
    Assert-SixStoneColors -Path $foregroundPath
    Assert-TransparentMask -Path $foregroundPath -AdaptiveSafeRegion
    Assert-TransparentMask -Path $monochromePath -Monochrome -AdaptiveSafeRegion
    Assert-TransparentMask -Path $notificationPath -Monochrome
    foreach ($legacyPath in @('assets\branding\zfnf-logo.png')) {
        $actualHash = (Get-FileHash -LiteralPath (Join-Path $fixtureRoot $legacyPath)).Hash
        $existingHash = (Get-FileHash -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) $legacyPath)).Hash
        Assert-Condition ($actualHash -eq $existingHash) "Broadcast adoption must preserve historical artwork: $legacyPath."
    }

    $desktopHeader = Join-Path $fixtureRoot 'windows-client\internal\clientapp\assets\brand-logo.png'
    Assert-Condition (Test-Path -LiteralPath $desktopHeader) 'The shared desktop header must be generated.'
    Assert-SixStoneColors -Path $desktopHeader
    Assert-TransparentMask -Path $desktopHeader
    $icoPath = Join-Path $fixtureRoot 'assets\branding\inevitable-mobile-relay.ico'
    $ico = [IO.File]::ReadAllBytes($icoPath)
    Assert-Condition ([BitConverter]::ToUInt16($ico, 2) -eq 1) 'Windows artwork must be an ICO.'
    Assert-Condition ([BitConverter]::ToUInt16($ico, 4) -eq 9) 'Windows artwork must include all nine shell/window sizes.'
    for ($frame = 0; $frame -lt 9; $frame++) {
        $entry = 6 + 16 * $frame
        $length = [BitConverter]::ToUInt32($ico, $entry + 8)
        $offset = [BitConverter]::ToUInt32($ico, $entry + 12)
        Assert-Condition ($offset + $length -le $ico.Length) 'ICO frames must fit within the file.'
        $framePath = Join-Path $fixtureRoot "ico-$frame.png"
        [IO.File]::WriteAllBytes($framePath, $ico[$offset..($offset + $length - 1)])
        if ($frame -eq 8) { Assert-SixStoneColors -Path $framePath }
    }
    $icns = [IO.File]::ReadAllBytes((Join-Path $fixtureRoot 'windows-client\macos\appicon.icns'))
    $largeMacFrame = $false
    for ($offset = 8; $offset -lt $icns.Length;) {
        $kind = [Text.Encoding]::ASCII.GetString($icns, $offset, 4)
        $length = [Net.IPAddress]::NetworkToHostOrder([BitConverter]::ToInt32($icns, $offset + 4))
        Assert-Condition ($length -gt 8 -and $offset + $length -le $icns.Length) 'ICNS frames must fit within the file.'
        if ($kind -eq 'ic10') {
            $framePath = Join-Path $fixtureRoot 'mac-1024.png'
            [IO.File]::WriteAllBytes($framePath, $icns[($offset + 8)..($offset + $length - 1)])
            Assert-SixStoneColors -Path $framePath
            $largeMacFrame = $true
        }
        $offset += $length
    }
    Assert-Condition $largeMacFrame 'The Mac icon must include full-color 1024-pixel artwork.'
    foreach ($command in @('mobile-egress-client-app', 'mobile-egress-setup')) {
        $objectPath = Join-Path $fixtureRoot "windows-client\cmd\$command\brand_windows_amd64.syso"
        Assert-Condition (Test-Path -LiteralPath $objectPath) 'Windows GUI and installer must both embed the icon.'
        $objectBytes = [IO.File]::ReadAllBytes($objectPath)
        Assert-Condition ([BitConverter]::ToUInt16($objectBytes, 0) -eq 0x8664) 'Windows icon object must target AMD64.'
    }

    $firstIconHash = (Get-FileHash -LiteralPath $iconPath -Algorithm SHA256).Hash
    $firstHeaderHash = (Get-FileHash -LiteralPath $headerPath -Algorithm SHA256).Hash
    $regenerated = Invoke-Generator
    Assert-Condition ($regenerated.ExitCode -eq 0) "Brand regeneration failed: $($regenerated.Output)"
    Assert-Condition ((Get-FileHash -LiteralPath $iconPath -Algorithm SHA256).Hash -eq $firstIconHash) 'The iOS AppIcon must be deterministic.'
    Assert-Condition ((Get-FileHash -LiteralPath $headerPath -Algorithm SHA256).Hash -eq $firstHeaderHash) 'The iOS header must be deterministic.'

    $cleanCheck = Invoke-Generator -Check
    Assert-Condition ($cleanCheck.ExitCode -eq 0) "Generated assets must pass the clean check: $($cleanCheck.Output)"

    Set-Content -LiteralPath $headerPath -Value 'stale-header'
    $staleCheck = Invoke-Generator -Check
    Assert-Condition ($staleCheck.ExitCode -ne 0) 'A stale iOS header must fail generator check mode.'
    Assert-Condition ($staleCheck.Output -match 'ZFNFHeader\.png') 'A stale iOS header diagnostic must name the failed output.'

    $restored = Invoke-Generator
    Assert-Condition ($restored.ExitCode -eq 0) "Brand fixture restoration failed: $($restored.Output)"
    Remove-Item -LiteralPath $iconPath -Force
    $missingCheck = Invoke-Generator -Check
    Assert-Condition ($missingCheck.ExitCode -ne 0) 'A missing iOS AppIcon must fail generator check mode.'
    Assert-Condition ($missingCheck.Output -match 'MobileEgressAppIcon\.png') 'A missing iOS AppIcon diagnostic must name the failed output.'
} finally {
    if (Test-Path -LiteralPath $fixtureRoot -PathType Container) {
        $resolvedFixture = (Resolve-Path -LiteralPath $fixtureRoot).Path
        $temporaryRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
        if (-not $resolvedFixture.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase) -or
            (Split-Path -Leaf $resolvedFixture) -notmatch '^mobile-egress-brand-assets-[0-9a-f]{32}$') {
            throw "Refusing to remove unexpected fixture path: $resolvedFixture"
        }
        Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
    }
}

Write-Host 'Brand asset generator checks passed.'
exit 0
