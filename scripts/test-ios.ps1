param(
    [switch]$UseMacBuildServer,
    [string]$MacHost = '10.0.0.77',
    [string]$MacUser = 'diana',
    [string]$SshKeyPath = ''
)

$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$iosDirectory = Join-Path $repositoryRoot 'ios'
$projectPath = Join-Path $iosDirectory 'MobileEgressAgent.xcodeproj'
$isMacHost = $IsMacOS -eq $true
$isWindowsHost = $env:OS -eq 'Windows_NT'
if ([string]::IsNullOrWhiteSpace($SshKeyPath)) {
    $SshKeyPath = Join-Path $repositoryRoot '.local/mac-build-server/id_ed25519'
}

function Invoke-RequiredCommand {
    param(
        [string]$Name,
        [scriptblock]$Command
    )

    Write-Host "==> $Name"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE."
    }
}

function Invoke-PortableSwiftTests {
    $dockerVersion = & docker version --format '{{.Server.Version}}' 2>$null
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($dockerVersion)) {
        Write-Host 'IOS_PORTABLE_TEST_STATUS=UNAVAILABLE'
        throw 'Docker Engine is required on Windows to run the portable Swift tests.'
    }

    $mount = "type=bind,source=$repositoryRoot,target=/workspace"
    Invoke-RequiredCommand -Name 'Portable Swift package tests' -Command {
        docker run --rm --mount $mount --workdir /workspace/ios swift:6.0 swift test --scratch-path /tmp/mobile-egress-swift-build
    }
    Invoke-RequiredCommand -Name 'Portable Swift package tests with warnings as errors' -Command {
        docker run --rm --mount $mount --workdir /workspace/ios swift:6.0 swift test -Xswiftc -warnings-as-errors --scratch-path /tmp/mobile-egress-swift-build-warnings
    }
    Write-Host 'IOS_PORTABLE_TEST_STATUS=PASSED'
}

function Assert-MacBuildServerKey {
    param([string]$Path)

    $resolvedKey = (Resolve-Path -LiteralPath $Path -ErrorAction Stop).Path
    $keyRepositoryRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $resolvedKey))
    $expectedKey = Join-Path $keyRepositoryRoot '.local/mac-build-server/id_ed25519'
    if (-not [string]::Equals($resolvedKey, $expectedKey, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Mac build-server SSH key must use the ignored .local/mac-build-server/id_ed25519 path in its checkout.'
    }

    & git -C $keyRepositoryRoot check-ignore -q -- .local/mac-build-server/id_ed25519
    if ($LASTEXITCODE -ne 0) {
        throw 'Mac build-server SSH private key is not ignored.'
    }
    $trackedKey = & git -C $keyRepositoryRoot ls-files -- .local/mac-build-server/id_ed25519
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to verify Mac build-server SSH key tracking state.'
    }
    if (-not [string]::IsNullOrWhiteSpace(($trackedKey | Out-String))) {
        throw 'Mac build-server SSH private key must not be tracked.'
    }

    return $resolvedKey
}

function Assert-ExactCommittedTree {
    param([string]$ExpectedCommit = '')

    $pendingChanges = & git -C $repositoryRoot status --porcelain
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to inspect the source-tree state for Mac build-server verification.'
    }
    if (-not [string]::IsNullOrWhiteSpace(($pendingChanges | Out-String))) {
        throw 'Commit or discard local changes before Mac build-server verification so every phase tests one exact tree.'
    }
    $commit = (& git -C $repositoryRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-f]{40}$') {
        throw 'Unable to resolve the exact source commit for Mac build-server verification.'
    }
    if (-not [string]::IsNullOrWhiteSpace($ExpectedCommit) -and $commit -ne $ExpectedCommit) {
        throw 'Source HEAD changed after portable tests; refusing to combine results from different commits.'
    }

    return $commit
}

function Invoke-MacBuildServerVerification {
    param(
        [string]$HostName,
        [string]$UserName,
        [string]$KeyPath,
        [string]$Commit
    )

    if ($HostName -notmatch '^[A-Za-z0-9.-]+$' -or $UserName -notmatch '^[A-Za-z0-9._-]+$') {
        throw 'Mac build-server host and user contain unsupported characters.'
    }
    foreach ($commandName in 'ssh', 'scp') {
        if ($null -eq (Get-Command $commandName -ErrorAction SilentlyContinue)) {
            throw "Mac build-server verification requires $commandName on PATH."
        }
    }

    $verifiedCommit = Assert-ExactCommittedTree -ExpectedCommit $Commit

    $resolvedKey = Assert-MacBuildServerKey -Path $KeyPath
    $macTarget = "$UserName@$HostName"
    $bundlePath = Join-Path ([IO.Path]::GetTempPath()) "mobile-egress-ios-$verifiedCommit.bundle"
    $remoteBundlePath = "/tmp/mobile-egress-ios-$verifiedCommit.bundle"
    $remoteScript = @'
set -euo pipefail
bundle_path="$1"
commit="$2"
checkout="$(mktemp -d)"
cleanup() {
    rm -rf "$checkout" "$bundle_path"
}
trap cleanup EXIT
git clone --no-checkout "$bundle_path" "$checkout"
git -C "$checkout" checkout --detach "$commit"
test "$(git -C "$checkout" rev-parse HEAD)" = "$commit"
cd "$checkout/ios"
swift test
swift test -Xswiftc -warnings-as-errors
xcodebuild -list -project MobileEgressAgent.xcodeproj
xcodebuild -project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build
xcodebuild -list -workspace .
xcodebuild test -workspace . -scheme MobileEgressCore -destination "platform=macOS"
# Keep PowerShell's trailing carriage return inside a Bash comment.
'@

    try {
        & git -C $repositoryRoot bundle create $bundlePath --all
        if ($LASTEXITCODE -ne 0) {
            throw "Git bundle creation failed with exit code $LASTEXITCODE."
        }
        & scp -i $resolvedKey -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes $bundlePath "${macTarget}:$remoteBundlePath"
        if ($LASTEXITCODE -ne 0) {
            throw "Mac bundle transfer failed with exit code $LASTEXITCODE."
        }
        $remoteScript | & ssh -i $resolvedKey -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes $macTarget "bash -s -- '$remoteBundlePath' '$verifiedCommit'"
        if ($LASTEXITCODE -ne 0) {
            throw "Mac iOS verification failed with exit code $LASTEXITCODE."
        }
    } finally {
        if (Test-Path -LiteralPath $bundlePath -PathType Leaf) {
            Remove-Item -LiteralPath $bundlePath -Force
        }
    }
}

if ($isMacHost) {
    Push-Location $iosDirectory
    try {
        Invoke-RequiredCommand -Name 'Swift package tests' -Command { swift test }
        Invoke-RequiredCommand -Name 'Swift package tests with warnings as errors' -Command { swift test -Xswiftc -warnings-as-errors }
        Invoke-RequiredCommand -Name 'Xcode project targets and schemes' -Command { xcodebuild -list -project $projectPath }
        Invoke-RequiredCommand -Name 'Unsigned iPhoneOS app and extension build' -Command {
            xcodebuild -project $projectPath -scheme MobileEgressAgent -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build
        }

        Invoke-RequiredCommand -Name 'Standalone MobileEgressCore Xcode package schemes' -Command {
            xcodebuild -list -workspace .
        }
        Invoke-RequiredCommand -Name 'MobileEgressCore Xcode package tests' -Command {
            xcodebuild test -workspace . -scheme MobileEgressCore -destination 'platform=macOS'
        }
    } finally {
        Pop-Location
    }

    Write-Host 'IOS_XCODE_STATUS=PASSED'
    exit 0
}

if ($isWindowsHost) {
    $commit = ''
    if ($UseMacBuildServer) {
        $commit = Assert-ExactCommittedTree
    }
    Invoke-PortableSwiftTests
    if ($UseMacBuildServer) {
        Invoke-MacBuildServerVerification -HostName $MacHost -UserName $MacUser -KeyPath $SshKeyPath -Commit $commit
        Write-Host 'IOS_XCODE_STATUS=PASSED'
        exit 0
    }
    Write-Host 'IOS_XCODE_STATUS=UNSUPPORTED_HOST'
    exit 20
}

Write-Host 'IOS_PORTABLE_TEST_STATUS=NOT_RUN'
Write-Host 'IOS_XCODE_STATUS=UNSUPPORTED_HOST'
exit 20
