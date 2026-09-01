[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$generator = Join-Path $PSScriptRoot 'generate-brand-assets.ps1'
$fixtureRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-brand-assets-" + [guid]::NewGuid().ToString('N'))
$sourceDirectory = Join-Path $fixtureRoot 'assets\branding'
$sourcePath = Join-Path $sourceDirectory 'zfnf-logo-source.png'
$powerShellExecutable = Join-Path $PSHOME 'pwsh.exe'

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

try {
    $null = New-Item -ItemType Directory -Path $sourceDirectory
    Copy-Item -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'assets\branding\zfnf-logo-source.png') -Destination $sourcePath

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
