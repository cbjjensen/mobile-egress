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
Assert-Condition ($remoteScript -notmatch '(?m)^\s*if\s+\[') 'Every remote verification phase must run unconditionally.'
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
Assert-Condition ($remoteScript -match 'destination "platform=macOS"\r?\n# Keep PowerShell''s trailing carriage return inside a Bash comment\.') 'The remote script must prevent PowerShell pipeline line endings from contaminating the final Bash argument.'
Assert-Condition ($scriptContent -match '\[string\]\$MacHost = ''10\.0\.0\.77''') 'The Mac build-server path must default to the documented verified SSH target.'
Assert-Condition ($scriptContent -match 'bundle create') 'The Mac build-server path must transfer an exact Git tree as a bundle.'
Assert-Condition ($scriptContent -match 'bundle create \$bundlePath --all') 'The Mac build-server path must create a non-empty full-history bundle before detached checkout verification.'
Assert-Condition ($scriptContent -match '(?m)^\s*& scp (?=.*-o StrictHostKeyChecking=yes)(?=.*-o IdentitiesOnly=yes).*$') 'The bundle transfer must require the known host and configured identity.'
Assert-Condition ($scriptContent -match '(?m)^\s*\$remoteScript \| & ssh (?=.*-o StrictHostKeyChecking=yes)(?=.*-o IdentitiesOnly=yes).*$') 'The remote verifier must require the known host and configured identity.'

$portableInvocationIndex = $scriptContent.LastIndexOf('Invoke-PortableSwiftTests', [StringComparison]::Ordinal)
$macInvocationIndex = $scriptContent.LastIndexOf('Invoke-MacBuildServerVerification', [StringComparison]::Ordinal)
$passIndex = $scriptContent.IndexOf("Write-Host 'IOS_XCODE_STATUS=PASSED'", $macInvocationIndex, [StringComparison]::Ordinal)
Assert-Condition ($portableInvocationIndex -ge 0 -and $portableInvocationIndex -lt $macInvocationIndex) 'Portable suites must run before Mac build-server verification.'
Assert-Condition ($macInvocationIndex -ge 0 -and $macInvocationIndex -lt $passIndex) 'The Mac build-server verification must finish before PASS is emitted.'
Assert-Condition ($scriptContent -match 'Assert-ExactCommittedTree') 'Build-server verification must bind portable and remote phases to one committed HEAD.'

Write-Host 'iOS verification script checks passed.'
exit 0
