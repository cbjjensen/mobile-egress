param(
    [string]$KeyPath = ".\.local\mac-build-server\id_ed25519",
    [string]$Comment = "raidmax-fix-to-mac",
    [switch]$Force
)

$ErrorActionPreference = "Stop"

$resolvedKeyPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($KeyPath)
$keyDirectory = Split-Path -Parent $resolvedKeyPath
$publicKeyPath = "$resolvedKeyPath.pub"
$repoRoot = git rev-parse --show-toplevel
if ($LASTEXITCODE -ne 0) {
    throw "This script must be run from inside the Mobile Egress Git repository."
}
$repoRootPath = [System.IO.Path]::GetFullPath($repoRoot.Trim())
$relativeKeyPath = [System.IO.Path]::GetRelativePath($repoRootPath, $resolvedKeyPath).Replace('\', '/')

New-Item -ItemType Directory -Force -Path $keyDirectory | Out-Null

if ((Test-Path -LiteralPath $resolvedKeyPath) -and -not $Force) {
    Write-Host "Using existing key: $resolvedKeyPath"
} else {
    if ((Test-Path -LiteralPath $resolvedKeyPath) -and $Force) {
        Remove-Item -LiteralPath $resolvedKeyPath -Force
    }
    if ((Test-Path -LiteralPath $publicKeyPath) -and $Force) {
        Remove-Item -LiteralPath $publicKeyPath -Force
    }

    ssh-keygen -t ed25519 -C $Comment -f $resolvedKeyPath -N ""
    if ($LASTEXITCODE -ne 0) {
        throw "ssh-keygen failed with exit code $LASTEXITCODE."
    }
}

git check-ignore -q -- $relativeKeyPath
if ($LASTEXITCODE -ne 0) {
    throw "Private key is not ignored by Git: $resolvedKeyPath"
}

$tracked = git ls-files -- $relativeKeyPath
if ($tracked) {
    throw "Private key is already tracked by Git: $resolvedKeyPath"
}

$publicKey = Get-Content -LiteralPath $publicKeyPath -Raw
$escapedPublicKey = $publicKey.Trim().Replace("'", "'\''")

Write-Host ""
Write-Host "Public key:"
Write-Host $publicKey.Trim()
Write-Host ""
Write-Host "Run this on the Mac as diana to authorize this Windows checkout:"
Write-Host "mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && grep -qxF '$escapedPublicKey' ~/.ssh/authorized_keys || echo '$escapedPublicKey' >> ~/.ssh/authorized_keys"
Write-Host ""
Write-Host "Then verify from Windows:"
Write-Host "ssh -i $resolvedKeyPath diana@10.0.0.77 hostname"
