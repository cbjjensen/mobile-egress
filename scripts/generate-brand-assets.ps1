[CmdletBinding()]
param(
    [string]$RepositoryRoot = '',
    [switch]$Check
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($RepositoryRoot)) {
    $RepositoryRoot = Split-Path -Parent $PSScriptRoot
}
$repositoryRoot = [System.IO.Path]::GetFullPath($RepositoryRoot)
$sourcePath = Join-Path $repositoryRoot 'assets\branding\zfnf-logo-source.png'

Add-Type -AssemblyName System.Drawing

function New-DirectoryForFile {
    param([Parameter(Mandatory)][string]$Path)

    $directory = Split-Path -Parent $Path
    if (-not (Test-Path -LiteralPath $directory -PathType Container)) {
        $null = New-Item -ItemType Directory -Path $directory
    }
}

function New-LogoBitmap {
    param(
        [Parameter(Mandatory)][System.Drawing.Image]$Source,
        [Parameter(Mandatory)][int]$Size,
        [Parameter(Mandatory)][bool]$Transparent,
        [double]$Scale = 1.0,
        [bool]$OpaqueRGB = $false
    )

    $pixelFormat = if ($OpaqueRGB) {
        [System.Drawing.Imaging.PixelFormat]::Format24bppRgb
    } else {
        [System.Drawing.Imaging.PixelFormat]::Format32bppArgb
    }
    $bitmap = [System.Drawing.Bitmap]::new(
        $Size,
        $Size,
        $pixelFormat
    )
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    try {
        $background = if ($Transparent) {
            [System.Drawing.Color]::Transparent
        } else {
            [System.Drawing.Color]::Black
        }
        $graphics.Clear($background)
        $graphics.CompositingMode = [System.Drawing.Drawing2D.CompositingMode]::SourceOver
        $graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
        $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality

        $renderSize = [int][math]::Round($Size * $Scale)
        $offset = [int][math]::Floor(($Size - $renderSize) / 2)
        $graphics.DrawImage($Source, $offset, $offset, $renderSize, $renderSize)
    } finally {
        $graphics.Dispose()
    }

    $rectangle = [System.Drawing.Rectangle]::new(0, 0, $Size, $Size)
    $data = $bitmap.LockBits(
        $rectangle,
        [System.Drawing.Imaging.ImageLockMode]::ReadWrite,
        $pixelFormat
    )
    try {
        $bytes = [byte[]]::new([math]::Abs($data.Stride) * $data.Height)
        [System.Runtime.InteropServices.Marshal]::Copy($data.Scan0, $bytes, 0, $bytes.Length)
        $bytesPerPixel = if ($OpaqueRGB) { 3 } else { 4 }
        for ($index = 0; $index -le $bytes.Length - $bytesPerPixel; $index += $bytesPerPixel) {
            $luminance = [math]::Max($bytes[$index], [math]::Max($bytes[$index + 1], $bytes[$index + 2]))
            $coverage = if ($luminance -le 24) {
                0
            } elseif ($luminance -ge 180) {
                255
            } else {
                [int][math]::Round(($luminance - 24) * 255 / 156)
            }

            if ($Transparent) {
                $bytes[$index] = 255
                $bytes[$index + 1] = 255
                $bytes[$index + 2] = 255
                $bytes[$index + 3] = $coverage
            } elseif ($OpaqueRGB) {
                $bytes[$index] = $coverage
                $bytes[$index + 1] = $coverage
                $bytes[$index + 2] = $coverage
            } else {
                $bytes[$index] = $coverage
                $bytes[$index + 1] = $coverage
                $bytes[$index + 2] = $coverage
                $bytes[$index + 3] = 255
            }
        }
        [System.Runtime.InteropServices.Marshal]::Copy($bytes, 0, $data.Scan0, $bytes.Length)
    } finally {
        $bitmap.UnlockBits($data)
    }

    return $bitmap
}

function Save-LogoPng {
    param(
        [Parameter(Mandatory)][System.Drawing.Image]$Source,
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][int]$Size,
        [Parameter(Mandatory)][bool]$Transparent,
        [double]$Scale = 1.0,
        [bool]$OpaqueRGB = $false
    )

    New-DirectoryForFile -Path $Path
    $bitmap = New-LogoBitmap -Source $Source -Size $Size -Transparent $Transparent -Scale $Scale -OpaqueRGB $OpaqueRGB
    try {
        $bitmap.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)
    } finally {
        $bitmap.Dispose()
    }
}

function Save-LogoIco {
    param(
        [Parameter(Mandatory)][System.Drawing.Image]$Source,
        [Parameter(Mandatory)][string]$Path
    )

    $sizes = @(16, 20, 24, 32, 40, 48, 64, 128, 256)
    $frames = [System.Collections.Generic.List[byte[]]]::new()
    foreach ($size in $sizes) {
        $bitmap = New-LogoBitmap -Source $Source -Size $size -Transparent $false
        $stream = [System.IO.MemoryStream]::new()
        try {
            $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
            $frames.Add($stream.ToArray())
        } finally {
            $stream.Dispose()
            $bitmap.Dispose()
        }
    }

    New-DirectoryForFile -Path $Path
    $file = [System.IO.File]::Create($Path)
    $writer = [System.IO.BinaryWriter]::new($file)
    try {
        $writer.Write([uint16]0)
        $writer.Write([uint16]1)
        $writer.Write([uint16]$frames.Count)
        $offset = 6 + (16 * $frames.Count)
        for ($index = 0; $index -lt $frames.Count; $index++) {
            $size = $sizes[$index]
            $dimension = if ($size -eq 256) { 0 } else { $size }
            $writer.Write([byte]$dimension)
            $writer.Write([byte]$dimension)
            $writer.Write([byte]0)
            $writer.Write([byte]0)
            $writer.Write([uint16]1)
            $writer.Write([uint16]32)
            $writer.Write([uint32]$frames[$index].Length)
            $writer.Write([uint32]$offset)
            $offset += $frames[$index].Length
        }
        foreach ($frame in $frames) {
            $writer.Write($frame)
        }
    } finally {
        $writer.Dispose()
        $file.Dispose()
    }
}

function Write-BigEndianUInt32 {
    param(
        [Parameter(Mandatory)][System.IO.BinaryWriter]$Writer,
        [Parameter(Mandatory)][uint32]$Value
    )

    $bytes = [BitConverter]::GetBytes($Value)
    if ([BitConverter]::IsLittleEndian) {
        [Array]::Reverse($bytes)
    }
    $Writer.Write($bytes)
}

function Save-LogoIcns {
    param(
        [Parameter(Mandatory)][System.Drawing.Image]$Source,
        [Parameter(Mandatory)][string]$Path
    )

    $representations = @(
        @{ Type = 'icp4'; Size = 16 },
        @{ Type = 'icp5'; Size = 32 },
        @{ Type = 'icp6'; Size = 64 },
        @{ Type = 'ic07'; Size = 128 },
        @{ Type = 'ic08'; Size = 256 },
        @{ Type = 'ic09'; Size = 512 },
        @{ Type = 'ic10'; Size = 1024 }
    )
    $frames = @()
    foreach ($representation in $representations) {
        $bitmap = New-LogoBitmap -Source $Source -Size $representation.Size -Transparent $false
        $stream = [System.IO.MemoryStream]::new()
        try {
            $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
            $frames += [pscustomobject]@{
                Type = $representation.Type
                Data = $stream.ToArray()
            }
        } finally {
            $stream.Dispose()
            $bitmap.Dispose()
        }
    }

    $length = 8
    foreach ($frame in $frames) {
        $length += 8 + $frame.Data.Length
    }

    New-DirectoryForFile -Path $Path
    $file = [System.IO.File]::Create($Path)
    $writer = [System.IO.BinaryWriter]::new($file)
    try {
        $writer.Write([Text.Encoding]::ASCII.GetBytes('icns'))
        Write-BigEndianUInt32 -Writer $writer -Value $length
        foreach ($frame in $frames) {
            $writer.Write([Text.Encoding]::ASCII.GetBytes($frame.Type))
            Write-BigEndianUInt32 -Writer $writer -Value (8 + $frame.Data.Length)
            $writer.Write($frame.Data)
        }
    } finally {
        $writer.Dispose()
        $file.Dispose()
    }
}

if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
    throw "Canonical logo source is missing: $sourcePath"
}

$outputRelativePaths = @(
    'assets\branding\zfnf-logo.png',
    'android\app\src\main\res\drawable-xxxhdpi\ic_mobile_egress_foreground.png',
    'android\app\src\main\res\drawable-xxxhdpi\ic_mobile_egress_notification.png',
    'windows-client\frontend\public\zfnf-logo.png',
    'windows-client\internal\desktop\zfnf-logo.ico',
    'ios\Assets\AppAssets.xcassets\AppIcon.appiconset\MobileEgressAppIcon.png',
    'ios\Assets\AppAssets.xcassets\ZFNFHeader.imageset\ZFNFHeader.png',
    'windows-client\internal\desktop\zfnf-menu-bar.png',
    'windows-client\macos\appicon.icns'
)
$generationRoot = $repositoryRoot
$checkRoot = $null
if ($Check) {
    $checkRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-brand-check-" + [guid]::NewGuid().ToString('N'))
    $generationRoot = $checkRoot
}

$source = [System.Drawing.Image]::FromFile($sourcePath)
try {
    Save-LogoPng -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[0]) -Size 1024 -Transparent $false
    Save-LogoPng -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[1]) -Size 432 -Transparent $true -Scale 0.88
    Save-LogoPng -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[2]) -Size 96 -Transparent $true -Scale 0.9
    Save-LogoPng -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[3]) -Size 512 -Transparent $false
    Save-LogoIco -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[4])
    Save-LogoPng -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[5]) -Size 1024 -Transparent $false -Scale 0.84 -OpaqueRGB $true
    Save-LogoPng -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[6]) -Size 256 -Transparent $true -Scale 0.9
    Save-LogoPng -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[7]) -Size 36 -Transparent $true -Scale 0.9
    Save-LogoIcns -Source $source -Path (Join-Path $generationRoot $outputRelativePaths[8])
} finally {
    $source.Dispose()
}

if ($Check) {
    try {
        foreach ($relativePath in $outputRelativePaths) {
            $trackedPath = Join-Path $repositoryRoot $relativePath
            $generatedPath = Join-Path $generationRoot $relativePath
            if (-not (Test-Path -LiteralPath $trackedPath -PathType Leaf)) {
                throw "Generated brand asset is missing: $relativePath"
            }
            $trackedHash = (Get-FileHash -LiteralPath $trackedPath -Algorithm SHA256).Hash
            $generatedHash = (Get-FileHash -LiteralPath $generatedPath -Algorithm SHA256).Hash
            if ($trackedHash -ne $generatedHash) {
                throw "Generated brand asset is stale: $relativePath"
            }
        }
    } finally {
        if ($null -ne $checkRoot -and (Test-Path -LiteralPath $checkRoot -PathType Container)) {
            $resolvedCheckRoot = (Resolve-Path -LiteralPath $checkRoot).Path
            $temporaryRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
            if (-not $resolvedCheckRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase) -or
                (Split-Path -Leaf $resolvedCheckRoot) -notmatch '^mobile-egress-brand-check-[0-9a-f]{32}$') {
                throw "Refusing to remove unexpected brand-check path: $resolvedCheckRoot"
            }
            Remove-Item -LiteralPath $resolvedCheckRoot -Recurse -Force
        }
    }
    Write-Host 'Generated Android, Windows, iOS, and macOS branding assets are current.'
    exit 0
}

Write-Host 'Generated Android, Windows, iOS, and macOS branding assets from assets\branding\zfnf-logo-source.png.'
