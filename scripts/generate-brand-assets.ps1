[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
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
        [double]$Scale = 1.0
    )

    $bitmap = [System.Drawing.Bitmap]::new(
        $Size,
        $Size,
        [System.Drawing.Imaging.PixelFormat]::Format32bppArgb
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
        [System.Drawing.Imaging.PixelFormat]::Format32bppArgb
    )
    try {
        $bytes = [byte[]]::new([math]::Abs($data.Stride) * $data.Height)
        [System.Runtime.InteropServices.Marshal]::Copy($data.Scan0, $bytes, 0, $bytes.Length)
        for ($index = 0; $index -lt $bytes.Length; $index += 4) {
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
        [double]$Scale = 1.0
    )

    New-DirectoryForFile -Path $Path
    $bitmap = New-LogoBitmap -Source $Source -Size $Size -Transparent $Transparent -Scale $Scale
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

if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
    throw "Canonical logo source is missing: $sourcePath"
}

$source = [System.Drawing.Image]::FromFile($sourcePath)
try {
    Save-LogoPng -Source $source -Path (Join-Path $repositoryRoot 'assets\branding\zfnf-logo.png') -Size 1024 -Transparent $false
    Save-LogoPng -Source $source -Path (Join-Path $repositoryRoot 'android\app\src\main\res\drawable-xxxhdpi\ic_mobile_egress_foreground.png') -Size 432 -Transparent $true -Scale 0.88
    Save-LogoPng -Source $source -Path (Join-Path $repositoryRoot 'android\app\src\main\res\drawable-xxxhdpi\ic_mobile_egress_notification.png') -Size 96 -Transparent $true -Scale 0.9
    Save-LogoPng -Source $source -Path (Join-Path $repositoryRoot 'windows-client\frontend\public\zfnf-logo.png') -Size 512 -Transparent $false
    Save-LogoIco -Source $source -Path (Join-Path $repositoryRoot 'windows-client\internal\desktop\zfnf-logo.ico')
} finally {
    $source.Dispose()
}

Write-Host 'Generated Android and Windows branding assets from assets\branding\zfnf-logo-source.png.'
