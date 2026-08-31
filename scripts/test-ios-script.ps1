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

$scriptContent = Get-Content -Raw $iosTestScript
Assert-Condition ($scriptContent -match 'IOS_XCODE_STATUS=UNSUPPORTED_HOST') 'Unsupported hosts must emit the explicit Xcode status marker.'
Assert-Condition ($scriptContent -match 'IOS_PORTABLE_TEST_STATUS=PASSED') 'A supported portable-test host must report portable test success before the unsupported Xcode result.'
Assert-Condition ($scriptContent -match '(?m)^\s*exit 20\s*$') 'Unsupported hosts must return the documented exit code 20.'
Assert-Condition ($scriptContent -match '(?m)^\$isMacHost = \$IsMacOS -eq \$true$') 'The verifier must have an explicit macOS host check.'
Assert-Condition ($scriptContent -match '(?m)^\$isWindowsHost = \$env:OS -eq ''Windows_NT''$') 'The verifier must identify Windows without depending on a PowerShell Core-only automatic variable.'
Assert-Condition ($scriptContent -match 'swift test -Xswiftc -warnings-as-errors') 'The verifier must run warning-as-errors Swift tests through the Swift compiler.'
Assert-Condition ($scriptContent -match 'xcodebuild -list') 'The macOS branch must enumerate Xcode targets and schemes.'
Assert-Condition ($scriptContent -match 'CODE_SIGNING_ALLOWED=NO') 'The macOS build must be unsigned.'
Assert-Condition ($scriptContent -match '\[switch\]\$UseMacBuildServer') 'The Windows entry point must expose an explicit Mac build-server path.'
Assert-Condition ($scriptContent -match '\[switch\]\$SkipPortableTests') 'The build-server path must support a guarded continuation without repeating a separately recorded portable test run.'
Assert-Condition ($scriptContent -match 'SkipPortableTests -and -not \$UseMacBuildServer') 'Skipping portable tests must require the Mac build-server path.'
Assert-Condition ($scriptContent -match '\[ValidateSet\(''all'', ''warnings'', ''xcode'', ''test''\)\]') 'The build-server path must expose validated continuation phases.'
Assert-Condition ($scriptContent -match 'MacBuildServerStartAt -ne ''all'' -and -not \$UseMacBuildServer') 'A Mac continuation phase must require the build-server path.'
Assert-Condition ($scriptContent -match 'if \[ "\$phase" = "all" \]; then') 'The remote verifier must be able to skip an already-recorded normal Swift suite.'
Assert-Condition ($scriptContent -match 'if \[ "\$phase" != "test" \]; then') 'The remote verifier must be able to resume at the package-test phase.'
Assert-Condition ($scriptContent -match 'xcodebuild test -workspace \. -scheme MobileEgressCore -destination "platform=macOS"') 'The package tests must use the standalone package workspace and a model-independent macOS destination.'
Assert-Condition ($scriptContent -match 'destination "platform=macOS"\r?\n# Keep PowerShell''s trailing carriage return inside a Bash comment\.') 'The remote script must prevent PowerShell pipeline line endings from contaminating the final Bash argument.'
Assert-Condition ($scriptContent -match '\[string\]\$MacHost = ''10\.0\.0\.77''') 'The Mac build-server path must default to the documented verified SSH target.'
Assert-Condition ($scriptContent -match 'bundle create') 'The Mac build-server path must transfer an exact Git tree as a bundle.'
Assert-Condition ($scriptContent -match 'bundle create \$bundlePath --all') 'The Mac build-server path must create a non-empty full-history bundle before detached checkout verification.'
Assert-Condition ($scriptContent -match 'scp ') 'The Mac build-server path must transfer the bundle with the configured SSH key.'
Assert-Condition ($scriptContent -match 'IOS_XCODE_STATUS=PASSED') 'A successful Mac build-server run must report Xcode validation as passed.'

Write-Host 'iOS verification script checks passed.'
exit 0
