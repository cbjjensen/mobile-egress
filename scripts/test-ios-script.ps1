$ErrorActionPreference = 'Stop'

function Assert-Condition {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw "Assertion failed: $Message"
    }
}

function Invoke-ChildPowerShell {
    param(
        [string]$ScriptPath,
        [string[]]$Arguments = @()
    )

    $output = & pwsh -NoProfile -ExecutionPolicy Bypass -File $ScriptPath @Arguments *>&1 | Out-String
    return [pscustomobject]@{
        ExitCode = $LASTEXITCODE
        Output   = $output
    }
}

function New-IosVerificationFixture {
    param([string]$SourceScript)

    $fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("mobile-egress-ios-script-" + [guid]::NewGuid().ToString('N'))
    $scriptsDirectory = Join-Path $fixtureRoot 'scripts'
    $shimDirectory = Join-Path $fixtureRoot 'command-shims'
    $keyDirectory = Join-Path $fixtureRoot '.local\mac-build-server'
    $null = New-Item -ItemType Directory -Path $scriptsDirectory, $shimDirectory, $keyDirectory
    Copy-Item -LiteralPath $SourceScript -Destination (Join-Path $scriptsDirectory 'test-ios.ps1')
    Set-Content -LiteralPath (Join-Path $scriptsDirectory 'validate-mobile-feature-manifest.ps1') -Value "Write-Host 'fixture manifest passed'`nexit 0`n" -NoNewline
    Set-Content -LiteralPath (Join-Path $fixtureRoot '.gitignore') -Value ".local/`n" -NoNewline
    Set-Content -LiteralPath (Join-Path $keyDirectory 'id_ed25519') -Value 'fixture private key' -NoNewline

    Set-Content -LiteralPath (Join-Path $shimDirectory 'docker.cmd') -Value @'
@echo off
>> "%MOBILE_EGRESS_COMMAND_LOG%" echo docker %*
if "%1"=="version" if "%MOBILE_EGRESS_DOCKER_EXIT_CODE%"=="0" echo 27.0.0
exit /b %MOBILE_EGRESS_DOCKER_EXIT_CODE%
'@
    Set-Content -LiteralPath (Join-Path $shimDirectory 'scp.cmd') -Value @'
@echo off
>> "%MOBILE_EGRESS_COMMAND_LOG%" echo scp %*
exit /b %MOBILE_EGRESS_SCP_EXIT_CODE%
'@
    Set-Content -LiteralPath (Join-Path $shimDirectory 'ssh.cmd') -Value @'
@echo off
>> "%MOBILE_EGRESS_COMMAND_LOG%" echo ssh %*
echo %* | findstr /C:"rm -f --" > nul
if not errorlevel 1 exit /b %MOBILE_EGRESS_SSH_CLEANUP_EXIT_CODE%
more > nul
exit /b %MOBILE_EGRESS_SSH_PRIMARY_EXIT_CODE%
'@

    & git -C $fixtureRoot init -q
    & git -C $fixtureRoot config user.email 'ios-script-fixture@example.invalid'
    & git -C $fixtureRoot config user.name 'iOS Script Fixture'
    & git -C $fixtureRoot config core.autocrlf false
    & git -C $fixtureRoot add --all
    & git -C $fixtureRoot commit -q -m 'fixture'
    Assert-Condition ($LASTEXITCODE -eq 0) 'The isolated iOS verifier fixture must have a committed tree.'
    $commit = (& git -C $fixtureRoot rev-parse HEAD).Trim()

    return [pscustomobject]@{
        Root      = $fixtureRoot
        Script    = Join-Path $scriptsDirectory 'test-ios.ps1'
        ShimPath  = $shimDirectory
        KeyPath   = Join-Path $keyDirectory 'id_ed25519'
        Commit    = $commit
    }
}

function Invoke-IosVerificationFixture {
    param(
        [pscustomobject]$Fixture,
        [string[]]$Arguments,
        [int]$DockerExitCode,
        [int]$ScpExitCode = 0,
        [int]$SshPrimaryExitCode = 0,
        [int]$SshCleanupExitCode = 0
    )

    $commandLog = Join-Path ([IO.Path]::GetTempPath()) ("mobile-egress-ios-commands-" + [guid]::NewGuid().ToString('N') + '.log')
    $originalPath = $env:Path
    $originalCommandLog = $env:MOBILE_EGRESS_COMMAND_LOG
    $originalDockerExitCode = $env:MOBILE_EGRESS_DOCKER_EXIT_CODE
    $originalScpExitCode = $env:MOBILE_EGRESS_SCP_EXIT_CODE
    $originalSshPrimaryExitCode = $env:MOBILE_EGRESS_SSH_PRIMARY_EXIT_CODE
    $originalSshCleanupExitCode = $env:MOBILE_EGRESS_SSH_CLEANUP_EXIT_CODE
    try {
        $env:Path = "$($Fixture.ShimPath);$originalPath"
        $env:MOBILE_EGRESS_COMMAND_LOG = $commandLog
        $env:MOBILE_EGRESS_DOCKER_EXIT_CODE = [string]$DockerExitCode
        $env:MOBILE_EGRESS_SCP_EXIT_CODE = [string]$ScpExitCode
        $env:MOBILE_EGRESS_SSH_PRIMARY_EXIT_CODE = [string]$SshPrimaryExitCode
        $env:MOBILE_EGRESS_SSH_CLEANUP_EXIT_CODE = [string]$SshCleanupExitCode
        $result = Invoke-ChildPowerShell -ScriptPath $Fixture.Script -Arguments $Arguments
        $commands = if (Test-Path -LiteralPath $commandLog) {
            Get-Content -LiteralPath $commandLog
        } else {
            @()
        }
        return [pscustomobject]@{
            ExitCode = $result.ExitCode
            Output   = $result.Output
            Commands = @($commands)
        }
    } finally {
        $env:Path = $originalPath
        $env:MOBILE_EGRESS_COMMAND_LOG = $originalCommandLog
        $env:MOBILE_EGRESS_DOCKER_EXIT_CODE = $originalDockerExitCode
        $env:MOBILE_EGRESS_SCP_EXIT_CODE = $originalScpExitCode
        $env:MOBILE_EGRESS_SSH_PRIMARY_EXIT_CODE = $originalSshPrimaryExitCode
        $env:MOBILE_EGRESS_SSH_CLEANUP_EXIT_CODE = $originalSshCleanupExitCode
        if (Test-Path -LiteralPath $commandLog -PathType Leaf) {
            Remove-Item -LiteralPath $commandLog -Force
        }
    }
}

function ConvertTo-WslPath {
    param([string]$WindowsPath)

    $wslPath = (& wsl.exe --exec wslpath -a $WindowsPath).Trim()
    Assert-Condition ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($wslPath)) "Unable to translate fixture path for WSL: $WindowsPath"
    return $wslPath
}

function Invoke-RemoteScriptScenario {
    param(
        [string]$RemoteScript,
        [ValidateSet(
            'success',
            'known-retry',
            'known-retry-fails',
            'signature-no-condition',
            'unrelated-failure',
            'mixed-test-failure',
            'swift-compile-failure',
            'swift-warnings-failure',
            'project-list-failure',
            'unsigned-build-failure',
            'workspace-list-failure'
        )]
        [string]$Scenario
    )

    $fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("mobile-egress-remote-script-" + [guid]::NewGuid().ToString('N'))
    $repository = Join-Path $fixtureRoot 'repository'
    $iosDirectory = Join-Path $repository 'ios'
    $shimDirectory = Join-Path $fixtureRoot 'shims'
    $bundlePath = Join-Path $fixtureRoot 'source.bundle'
    $commandLog = Join-Path $fixtureRoot 'remote-commands.log'
    $finalCount = Join-Path $fixtureRoot 'final-count.txt'
    $null = New-Item -ItemType Directory -Path $iosDirectory, $shimDirectory
    Set-Content -LiteralPath (Join-Path $iosDirectory 'README.md') -Value "fixture`n" -NoNewline
    Set-Content -LiteralPath (Join-Path $shimDirectory 'swift') -Value @'
#!/usr/bin/env bash
printf 'swift %s\n' "$*" >> "$MOBILE_EGRESS_REMOTE_COMMAND_LOG"
if [[ "$MOBILE_EGRESS_REMOTE_SCENARIO" == 'swift-compile-failure' && "$*" == 'test' ]]; then
    printf 'com.apple.testmanagerd.control connection invalidated during Swift compilation\n' >&2
    exit 70
fi
if [[ "$MOBILE_EGRESS_REMOTE_SCENARIO" == 'swift-warnings-failure' && "$*" == 'test -Xswiftc -warnings-as-errors' ]]; then
    printf 'com.apple.testmanagerd.control connection unavailable during warning-strict compilation\n' >&2
    exit 71
fi
exit 0
'@
    Set-Content -LiteralPath (Join-Path $shimDirectory 'git') -Value @'
#!/usr/bin/env bash
if [[ "$1" == 'clone' && "$2" == '--no-checkout' ]]; then
    mkdir -p "$4/ios"
    exit 0
fi
if [[ "$1" == '-C' && "$3" == 'checkout' && "$4" == '--detach' ]]; then
    printf '%s\n' "$5" > "$2/.fixture-commit"
    exit 0
fi
if [[ "$1" == '-C' && "$3" == 'rev-parse' && "$4" == 'HEAD' ]]; then
    cat "$2/.fixture-commit"
    exit 0
fi
printf 'Unexpected fixture git command: %s\n' "$*" >&2
exit 90
'@
    Set-Content -LiteralPath (Join-Path $shimDirectory 'xcodebuild') -Value @'
#!/usr/bin/env bash
printf 'xcodebuild %s\n' "$*" >> "$MOBILE_EGRESS_REMOTE_COMMAND_LOG"
if [[ "$MOBILE_EGRESS_REMOTE_SCENARIO" == 'project-list-failure' && "$*" == '-list -project MobileEgressAgent.xcodeproj' ]]; then
    printf 'com.apple.testmanagerd.control connection invalidated during project listing\n' >&2
    exit 72
fi
if [[ "$MOBILE_EGRESS_REMOTE_SCENARIO" == 'unsigned-build-failure' && "$*" == '-project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build' ]]; then
    printf 'com.apple.testmanagerd.control connection unavailable during unsigned build\n' >&2
    exit 73
fi
if [[ "$MOBILE_EGRESS_REMOTE_SCENARIO" == 'workspace-list-failure' && "$*" == '-list -workspace .' ]]; then
    printf 'com.apple.testmanagerd.control connection invalidated during workspace listing\n' >&2
    exit 74
fi
if [[ "$*" != 'test -workspace . -scheme MobileEgressCore -destination platform=macOS' ]]; then
    printf 'xcodebuild fixture success: %s\n' "$*"
    exit 0
fi

attempt=0
if [[ -f "$MOBILE_EGRESS_FINAL_COUNT" ]]; then
    attempt="$(<"$MOBILE_EGRESS_FINAL_COUNT")"
fi
attempt=$((attempt + 1))
printf '%s\n' "$attempt" > "$MOBILE_EGRESS_FINAL_COUNT"

case "$MOBILE_EGRESS_XCODE_SCENARIO" in
    success)
        printf 'FINAL_XCODE_SUCCESS_ATTEMPT_%s\n' "$attempt"
        exit 0
        ;;
    known-retry)
        if [[ "$attempt" -eq 1 ]]; then
            printf 'com.apple.testmanagerd.control connection invalidated\n' >&2
            exit 65
        fi
        printf 'FINAL_XCODE_RETRY_SUCCESS\n'
        exit 0
        ;;
    known-retry-fails)
        if [[ "$attempt" -eq 1 ]]; then
            printf 'com.apple.testmanagerd.control connection unavailable\n' >&2
        else
            printf 'FINAL_XCODE_RETRY_FAILED\n' >&2
        fi
        exit 65
        ;;
    signature-no-condition)
        printf 'com.apple.testmanagerd.control request rejected\n' >&2
        exit 65
        ;;
    unrelated-failure)
        printf 'XCTAssertEqual failed: expected values to match\n' >&2
        exit 65
        ;;
    mixed-test-failure)
        printf 'com.apple.testmanagerd.control connection invalidated\n' >&2
        printf "Test Case '-[MobileEgressCoreTests.ExampleTests testExample]' failed (0.001 seconds).\n" >&2
        printf 'Executed 1 test, with 1 failure (0 unexpected)\n' >&2
        exit 65
        ;;
esac
'@
    foreach ($bashShimName in 'git', 'swift', 'xcodebuild') {
        $bashShimPath = Join-Path $shimDirectory $bashShimName
        $bashShimContent = (Get-Content -LiteralPath $bashShimPath -Raw).Replace("`r`n", "`n")
        Set-Content -LiteralPath $bashShimPath -Value $bashShimContent -NoNewline
    }

    try {
        & git -C $repository init -q
        & git -C $repository config user.email 'remote-script-fixture@example.invalid'
        & git -C $repository config user.name 'Remote Script Fixture'
        & git -C $repository config core.autocrlf false
        & git -C $repository add --all
        & git -C $repository commit -q -m 'fixture'
        $commit = (& git -C $repository rev-parse HEAD).Trim()
        & git -C $repository bundle create $bundlePath --all
        Assert-Condition ($LASTEXITCODE -eq 0) 'The remote-script scenario must create an exact Git bundle.'

        $shimWslPath = ConvertTo-WslPath -WindowsPath $shimDirectory
        $bundleWslPath = ConvertTo-WslPath -WindowsPath $bundlePath
        $commandLogWslPath = ConvertTo-WslPath -WindowsPath $commandLog
        $finalCountWslPath = ConvertTo-WslPath -WindowsPath $finalCount
        $output = $RemoteScript | & wsl.exe --exec bash -c @'
chmod +x "$1/git" "$1/swift" "$1/xcodebuild"
PATH="$1:$PATH" MOBILE_EGRESS_REMOTE_COMMAND_LOG="$2" MOBILE_EGRESS_FINAL_COUNT="$3" MOBILE_EGRESS_XCODE_SCENARIO="$4" MOBILE_EGRESS_REMOTE_SCENARIO="$4" bash -s -- "$5" "$6"
'@ fixture $shimWslPath $commandLogWslPath $finalCountWslPath $Scenario $bundleWslPath $commit *>&1 | Out-String
        $exitCode = $LASTEXITCODE
        $attempts = if (Test-Path -LiteralPath $finalCount -PathType Leaf) {
            [int](Get-Content -LiteralPath $finalCount -Raw)
        } else {
            0
        }
        $commands = if (Test-Path -LiteralPath $commandLog -PathType Leaf) {
            @(Get-Content -LiteralPath $commandLog)
        } else {
            @()
        }
        return [pscustomobject]@{
            ExitCode = $exitCode
            Output   = $output
            Attempts = $attempts
            Commands = @($commands)
        }
    } finally {
        $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
        $resolvedFixtureRoot = [IO.Path]::GetFullPath($fixtureRoot)
        Assert-Condition ($resolvedFixtureRoot.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) 'Refusing to clean a remote-script fixture outside the temporary directory.'
        if (Test-Path -LiteralPath $fixtureRoot -PathType Container) {
            Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
        }
    }
}

$iosTestScript = Join-Path $PSScriptRoot 'test-ios.ps1'
Assert-Condition (Test-Path -LiteralPath $iosTestScript -PathType Leaf) 'The iOS verification entry point must exist.'

$scriptContent = (Get-Content -Raw $iosTestScript).Replace("`r`n", "`n")
$scriptCommand = Get-Command $iosTestScript
Assert-Condition ($scriptContent -match 'validate-mobile-feature-manifest\.ps1') 'The iOS component gate must validate the mobile feature parity manifest.'
Assert-Condition (-not $scriptCommand.Parameters.ContainsKey('SkipPortableTests')) 'The verifier must reject portable-test continuation controls.'
Assert-Condition (-not $scriptCommand.Parameters.ContainsKey('MacBuildServerStartAt')) 'The verifier must reject remote-phase continuation controls.'
Assert-Condition ($scriptContent -match 'IOS_XCODE_STATUS=UNSUPPORTED_HOST') 'Unsupported hosts must emit the explicit Xcode status marker.'
Assert-Condition ($scriptContent -match 'IOS_PORTABLE_TEST_STATUS=PASSED') 'A supported portable-test host must report portable test success before the unsupported Xcode result.'
Assert-Condition ($scriptContent -match '(?m)^\s*exit 20\s*$') 'Unsupported hosts must return the documented exit code 20.'
Assert-Condition ($scriptContent -match '(?m)^\$isMacHost = \$IsMacOS -eq \$true$') 'The verifier must have an explicit macOS host check.'
Assert-Condition ($scriptContent -match '(?m)^\$isWindowsHost = \$env:OS -eq ''Windows_NT''$') 'The verifier must identify Windows without depending on a PowerShell Core-only automatic variable.'
Assert-Condition ($scriptContent -match 'swift test -Xswiftc -warnings-as-errors') 'The verifier must run warning-as-errors Swift tests through the Swift compiler.'
Assert-Condition ($scriptContent -match 'xcodebuild -list') 'The macOS branch must enumerate Xcode targets and schemes.'
Assert-Condition ($scriptContent -match 'CODE_SIGNING_ALLOWED=NO') 'The macOS build must be unsigned.'
Assert-Condition ($scriptContent -match '\[switch\]\$UseMacBuildServer') 'The Windows entry point must expose an explicit Mac build-server path.'
Assert-Condition ($scriptContent -notmatch 'SkipPortableTests|MacBuildServerStartAt') 'Retired continuation controls must not remain in the verifier implementation.'

$remoteMatch = [regex]::Match(
    $scriptContent,
    '(?s)\$remoteScript = @''\r?\n(?<body>.*?)\r?\n''@'
)
Assert-Condition $remoteMatch.Success 'The build-server verifier must define one inspectable remote script.'
$remoteScript = $remoteMatch.Groups['body'].Value
$normalizationToken = '$remoteScript = $remoteScript.Replace("`r`n", "`n")'
$normalizationIndex = $scriptContent.IndexOf(
    $normalizationToken,
    $remoteMatch.Index + $remoteMatch.Length,
    [StringComparison]::Ordinal
)
$remoteInvocationIndex = $scriptContent.IndexOf(
    '$remoteScript | & ssh',
    $remoteMatch.Index + $remoteMatch.Length,
    [StringComparison]::Ordinal
)
Assert-Condition (
    $normalizationIndex -ge 0 -and $normalizationIndex -lt $remoteInvocationIndex
) 'The verifier must normalize CRLF checkout line endings before sending the remote Bash script.'
$requiredRemoteCommands = @(
    'swift test',
    'swift test -Xswiftc -warnings-as-errors',
    'xcodebuild -list -project MobileEgressAgent.xcodeproj',
    'xcodebuild -project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build',
    'xcodebuild -list -workspace .',
    'xcodebuild test -workspace . -scheme MobileEgressCore -destination "platform=macOS"'
)
$previousCommandIndex = -1
foreach ($requiredCommand in $requiredRemoteCommands) {
    $commandIndex = $remoteScript.IndexOf(
        $requiredCommand,
        $previousCommandIndex + 1,
        [StringComparison]::Ordinal
    )
    Assert-Condition ($commandIndex -gt $previousCommandIndex) "Missing or out-of-order unconditional remote command: $requiredCommand"
    $previousCommandIndex = $commandIndex
}
Assert-Condition ($scriptContent -match '\[string\]\$MacHost = ''10\.0\.0\.77''') 'The Mac build-server path must default to the documented verified SSH target.'
Assert-Condition ($scriptContent -match 'bundle create') 'The Mac build-server path must transfer an exact Git tree as a bundle.'
Assert-Condition ($scriptContent -match 'bundle create \$bundlePath --all') 'The Mac build-server path must create a non-empty full-history bundle before detached checkout verification.'
Assert-Condition ($scriptContent -match '(?m)^\s*& scp (?=.*-o StrictHostKeyChecking=yes)(?=.*-o IdentitiesOnly=yes).*$') 'The bundle transfer must require the known host and configured identity.'
Assert-Condition ($scriptContent -match '(?m)^\s*\$remoteScript \| & ssh (?=.*-o StrictHostKeyChecking=yes)(?=.*-o IdentitiesOnly=yes).*$') 'The remote verifier must require the known host and configured identity.'

Assert-Condition ($scriptContent -match 'Assert-ExactCommittedTree') 'Build-server verification must bind portable and remote phases to one committed HEAD.'

$mixedTestFailure = Invoke-RemoteScriptScenario -RemoteScript $remoteScript -Scenario 'mixed-test-failure'
Assert-Condition ($mixedTestFailure.ExitCode -ne 0) 'A mixed XCTest assertion and testmanagerd invalidation must fail.'
Assert-Condition ($mixedTestFailure.Attempts -eq 1) 'A recognizable XCTest assertion/failure summary must prevent a testmanagerd retry.'
Assert-Condition ($mixedTestFailure.Output -notmatch 'retrying final Xcode package tests') 'Mixed assertion output must not emit a retry marker.'

$verificationFixture = New-IosVerificationFixture -SourceScript $iosTestScript
try {
    $macResult = Invoke-IosVerificationFixture -Fixture $verificationFixture -Arguments @(
        '-UseMacBuildServer',
        '-SshKeyPath',
        $verificationFixture.KeyPath
    ) -DockerExitCode 99
    Assert-Condition ($macResult.ExitCode -eq 0) "Mac build-server mode must succeed without Docker. Output: $($macResult.Output)"
    Assert-Condition (-not ($macResult.Commands -match '^docker ')) 'Mac build-server mode must not invoke Docker.'
    Assert-Condition (($macResult.Commands -match '^scp ').Count -eq 1) 'Mac build-server mode must transfer one exact Git bundle.'
    $macSshCommands = @($macResult.Commands -match '^ssh ')
    Assert-Condition ($macSshCommands.Count -eq 2) 'Mac build-server mode must launch verification and then best-effort remote bundle cleanup.'
    Assert-Condition ($macSshCommands[1] -match ([regex]::Escape("rm -f -- '/tmp/mobile-egress-ios-$($verificationFixture.Commit).bundle'"))) 'Mac build-server cleanup must target only the exact transferred remote bundle.'

    $defaultWindowsResult = Invoke-IosVerificationFixture -Fixture $verificationFixture -Arguments @() -DockerExitCode 1
    Assert-Condition ($defaultWindowsResult.ExitCode -ne 0) 'Default Windows mode must fail when Docker Engine is unavailable.'
    Assert-Condition (($defaultWindowsResult.Commands -match '^docker ').Count -eq 1) 'Default Windows mode must validate Docker Engine availability.'
    Assert-Condition ($defaultWindowsResult.Output -match 'Docker Engine is required on Windows') 'Default Windows Docker failure must explain the prerequisite.'
    Assert-Condition (-not ($defaultWindowsResult.Commands -match '^(ssh|scp) ')) 'Default Windows mode must not contact the Mac build server.'

    $defaultWindowsSuccess = Invoke-IosVerificationFixture -Fixture $verificationFixture -Arguments @() -DockerExitCode 0
    $successfulDockerCommands = @($defaultWindowsSuccess.Commands -match '^docker ')
    Assert-Condition ($defaultWindowsSuccess.ExitCode -eq 20) 'Default Windows mode must return unsupported-Xcode exit 20 after portable suites pass.'
    Assert-Condition ($successfulDockerCommands.Count -eq 3) 'Default Windows mode must check Docker and run both portable Swift suites.'
    Assert-Condition ($successfulDockerCommands[0] -match '^docker version ') 'Default Windows mode must validate Docker before portable suites.'
    Assert-Condition ($successfulDockerCommands[1] -match 'swift test --scratch-path /tmp/mobile-egress-swift-build$') 'Default Windows mode must run the portable Swift suite first.'
    Assert-Condition ($successfulDockerCommands[2] -match 'swift test -Xswiftc -warnings-as-errors --scratch-path /tmp/mobile-egress-swift-build-warnings$') 'Default Windows mode must run warning-strict portable Swift second.'
    $portablePassedIndex = $defaultWindowsSuccess.Output.IndexOf('IOS_PORTABLE_TEST_STATUS=PASSED', [StringComparison]::Ordinal)
    $unsupportedIndex = $defaultWindowsSuccess.Output.IndexOf('IOS_XCODE_STATUS=UNSUPPORTED_HOST', [StringComparison]::Ordinal)
    Assert-Condition ($portablePassedIndex -ge 0 -and $portablePassedIndex -lt $unsupportedIndex) 'Portable success must be reported before unsupported Xcode status.'
    Assert-Condition (-not ($defaultWindowsSuccess.Commands -match '^(ssh|scp) ')) 'Successful default Windows portable mode must not contact the Mac.'

    $preScriptSshFailure = Invoke-IosVerificationFixture -Fixture $verificationFixture -Arguments @(
        '-UseMacBuildServer',
        '-SshKeyPath',
        $verificationFixture.KeyPath
    ) -DockerExitCode 99 -SshPrimaryExitCode 73 -SshCleanupExitCode 91
    $failedSshCommands = @($preScriptSshFailure.Commands -match '^ssh ')
    Assert-Condition ($preScriptSshFailure.ExitCode -ne 0) 'A pre-script SSH launch failure must fail Mac verification.'
    Assert-Condition ($preScriptSshFailure.Output -match 'Mac iOS verification failed with exit code 73') 'Best-effort cleanup must preserve the original SSH launch failure.'
    Assert-Condition ($preScriptSshFailure.Output -notmatch 'exit code 91') 'Best-effort cleanup failure must not replace the original SSH failure.'
    Assert-Condition ($failedSshCommands.Count -eq 2) 'A successful scp followed by SSH launch failure must attempt exact remote bundle cleanup once.'
    Assert-Condition ($failedSshCommands[1] -match '-o BatchMode=yes') 'Remote bundle cleanup must remain noninteractive.'
    Assert-Condition ($failedSshCommands[1] -match ([regex]::Escape("rm -f -- '/tmp/mobile-egress-ios-$($verificationFixture.Commit).bundle'"))) 'Failure cleanup must remove only the exact validated remote bundle path.'
} finally {
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    $resolvedFixtureRoot = [IO.Path]::GetFullPath($verificationFixture.Root)
    Assert-Condition ($resolvedFixtureRoot.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) 'Refusing to clean an iOS verifier fixture outside the temporary directory.'
    if (Test-Path -LiteralPath $verificationFixture.Root -PathType Container) {
        Remove-Item -LiteralPath $verificationFixture.Root -Recurse -Force
    }
}

$knownRetry = Invoke-RemoteScriptScenario -RemoteScript $remoteScript -Scenario 'known-retry'
Assert-Condition ($knownRetry.ExitCode -eq 0) "Known testmanagerd invalidation must recover after one retry. Output: $($knownRetry.Output)"
Assert-Condition ($knownRetry.Attempts -eq 2) 'Known testmanagerd invalidation must launch final Xcode package tests exactly twice.'
Assert-Condition ($knownRetry.Output -match 'com\.apple\.testmanagerd\.control connection invalidated') 'The first invalidation output must remain visible for diagnosis.'
Assert-Condition ($knownRetry.Output -match 'FINAL_XCODE_RETRY_SUCCESS') 'The retry output must remain visible for diagnosis.'

$success = Invoke-RemoteScriptScenario -RemoteScript $remoteScript -Scenario 'success'
Assert-Condition ($success.ExitCode -eq 0) 'Successful final Xcode package tests must pass.'
Assert-Condition ($success.Attempts -eq 1) 'Successful final Xcode package tests must not retry.'

$signatureNoCondition = Invoke-RemoteScriptScenario -RemoteScript $remoteScript -Scenario 'signature-no-condition'
Assert-Condition ($signatureNoCondition.ExitCode -ne 0) 'A testmanagerd name without invalidation or unavailability must fail.'
Assert-Condition ($signatureNoCondition.Attempts -eq 1) 'A testmanagerd name without invalidation or unavailability must not retry.'

$unrelatedFailure = Invoke-RemoteScriptScenario -RemoteScript $remoteScript -Scenario 'unrelated-failure'
Assert-Condition ($unrelatedFailure.ExitCode -ne 0) 'An unrelated final Xcode package-test failure must fail.'
Assert-Condition ($unrelatedFailure.Attempts -eq 1) 'An unrelated final Xcode package-test failure must not retry.'

$failedRetry = Invoke-RemoteScriptScenario -RemoteScript $remoteScript -Scenario 'known-retry-fails'
Assert-Condition ($failedRetry.ExitCode -ne 0) 'A failed retry after known testmanagerd unavailability must fail verification.'
Assert-Condition ($failedRetry.Attempts -eq 2) 'A known testmanagerd retry must run once only even when the retry fails.'

$allRemoteCommands = @(
    'swift test',
    'swift test -Xswiftc -warnings-as-errors',
    'xcodebuild -list -project MobileEgressAgent.xcodeproj',
    'xcodebuild -project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build',
    'xcodebuild -list -workspace .',
    'xcodebuild test -workspace . -scheme MobileEgressCore -destination platform=macOS'
)
$earlyFailureCases = @(
    [pscustomobject]@{ Scenario = 'swift-compile-failure'; ExpectedCount = 1 },
    [pscustomobject]@{ Scenario = 'swift-warnings-failure'; ExpectedCount = 2 },
    [pscustomobject]@{ Scenario = 'project-list-failure'; ExpectedCount = 3 },
    [pscustomobject]@{ Scenario = 'unsigned-build-failure'; ExpectedCount = 4 },
    [pscustomobject]@{ Scenario = 'workspace-list-failure'; ExpectedCount = 5 }
)
foreach ($failureCase in $earlyFailureCases) {
    $phaseFailure = Invoke-RemoteScriptScenario -RemoteScript $remoteScript -Scenario $failureCase.Scenario
    $expectedCommands = $allRemoteCommands[0..($failureCase.ExpectedCount - 1)]
    Assert-Condition ($phaseFailure.ExitCode -ne 0) "$($failureCase.Scenario) must fail remote verification."
    Assert-Condition ($phaseFailure.Attempts -eq 0) "$($failureCase.Scenario) must stop before final Xcode package tests."
    Assert-Condition ($phaseFailure.Commands.Count -eq $failureCase.ExpectedCount) "$($failureCase.Scenario) must stop at its exact phase boundary."
    Assert-Condition (
        [string]::Join("`n", $phaseFailure.Commands) -eq [string]::Join("`n", $expectedCommands)
    ) "$($failureCase.Scenario) must preserve remote command order and stop immediately."
    Assert-Condition ($phaseFailure.Output -notmatch 'retrying final Xcode package tests') "$($failureCase.Scenario) must never retry."
}

Write-Host 'iOS verification script checks passed.'
exit 0
