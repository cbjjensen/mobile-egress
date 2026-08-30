param([string]$SignedSetupPath = '')

$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$quickStartPath = Join-Path $repositoryRoot 'windows-client\README.md'
$buildScriptPath = Join-Path $repositoryRoot 'scripts\build-windows.ps1'
$quickStart = Get-Content -Raw -LiteralPath $quickStartPath
$buildScript = Get-Content -Raw -LiteralPath $buildScriptPath

$orderedTokens = @(
    '$expectedCertificateSha256',
    '$expectedSetupSha256',
    '[IO.File]::Open(',
    'Get-AuthenticodeSignature -LiteralPath $setupPath',
    '$signature.SignerCertificate.Thumbprint.ToUpperInvariant()',
    '$sha256.ComputeHash($stream)',
    '.ToLowerInvariant()',
    '$setupDigest -ne $expectedSetupSha256',
    'Start-Process -FilePath $setupPath',
    "'--verified-setup-sha256', `$setupDigest",
    '$stream.Dispose()'
)

$lastIndex = -1
foreach ($token in $orderedTokens) {
    $index = $quickStart.IndexOf($token, [StringComparison]::Ordinal)
    if ($index -le $lastIndex) {
        throw "Trusted verifier documentation is missing or out of order: $token"
    }
    $lastIndex = $index
}

foreach ($status in @('NotTrusted', 'Valid', 'HashMismatch', 'NotSigned', 'UnknownError')) {
    if (-not $quickStart.Contains($status)) {
        throw "Trusted verifier documentation does not define status handling for $status."
    }
}

if (-not $quickStart.Contains('85F220C1BF05A5D3A86B5DD408787EC1B122ECB7') -or
    -not $quickStart.Contains('9FE214C350D7CE04C8EE7F71E169281B50FF0B2A7C5669A348AC10616FB7061F') -or
    -not $quickStart.Contains('[IO.FileShare]::Read') -or
    -not $quickStart.Contains('-Wait') -or
    -not $buildScript.Contains('Setup SHA-256:')) {
    throw 'Trusted verifier wait lifetime or setup artifact digest output is missing.'
}

if ($SignedSetupPath) {
    $resolvedSetupPath = (Resolve-Path -LiteralPath $SignedSetupPath).Path
    $expectedSetupDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $resolvedSetupPath).Hash.ToLowerInvariant()
    $script:observedVerifierLaunch = $false
    function Start-Process {
        param([string]$FilePath, [object[]]$ArgumentList, [switch]$Wait)
        if ($FilePath -cne $resolvedSetupPath -or -not $Wait -or $ArgumentList.Count -ne 2 -or
            $ArgumentList[0] -cne '--verified-setup-sha256' -or $ArgumentList[1] -cne $expectedSetupDigest) {
            throw 'Trusted verifier did not bind the exact held path/digest to process launch.'
        }
        $writer = $null
        try {
            $writer = [IO.File]::Open($FilePath, [IO.FileMode]::Open, [IO.FileAccess]::Write, [IO.FileShare]::Read)
            throw 'Setup mutation was possible while the trusted verifier launched it.'
        } catch [IO.IOException] {
            $script:observedVerifierLaunch = $true
        } finally {
            if ($null -ne $writer) { $writer.Dispose() }
        }
    }

    $stream = [IO.File]::Open($resolvedSetupPath, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $signature = Get-AuthenticodeSignature -LiteralPath $resolvedSetupPath
        if ([string]$signature.Status -ne 'Valid' -or
            $signature.SignerCertificate.Thumbprint.ToUpperInvariant() -ne '85F220C1BF05A5D3A86B5DD408787EC1B122ECB7') {
            throw 'Signed verifier fixture does not have the exact valid signer.'
        }
        $certificateHasher = [Security.Cryptography.SHA256]::Create()
        try { $certificateSha256 = ([BitConverter]::ToString($certificateHasher.ComputeHash($signature.SignerCertificate.RawData))).Replace('-', '') } finally { $certificateHasher.Dispose() }
        if ($certificateSha256 -ne '9FE214C350D7CE04C8EE7F71E169281B50FF0B2A7C5669A348AC10616FB7061F') {
            throw 'Signed verifier fixture certificate SHA-256 differs.'
        }
        $stream.Position = 0
        $setupHasher = [Security.Cryptography.SHA256]::Create()
        try { $heldDigest = ([BitConverter]::ToString($setupHasher.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() } finally { $setupHasher.Dispose() }
        Start-Process -FilePath $resolvedSetupPath -ArgumentList @('--verified-setup-sha256', $heldDigest) -Wait
    } finally {
        $stream.Dispose()
        Remove-Item Function:\Start-Process -ErrorAction SilentlyContinue
    }
    if (-not $script:observedVerifierLaunch) {
        throw 'Trusted verifier launch was not observed while the mutation lock was held.'
    }
}

Write-Host 'Windows trusted verifier documentation checks passed.'
