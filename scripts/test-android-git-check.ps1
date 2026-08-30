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

$releaseScript = Join-Path $PSScriptRoot 'release-android.ps1'
. $releaseScript

$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("mobile-egress-fake-git-" + [guid]::NewGuid().ToString('N'))
$originalPath = $env:PATH
try {
    $null = New-Item -ItemType Directory -Path $temporaryRoot
    $fakeGit = Join-Path $temporaryRoot 'git.cmd'
    Set-Content -LiteralPath $fakeGit -Encoding Ascii -Value @(
        '@echo off',
        'for %%A in (%*) do if "%%~A"=="android/mobile-egress-release.jks" exit /b 2',
        'if "%1"=="-C" shift',
        'if "%1"=="C:\fixture" shift',
        'if "%1"=="ls-files" (',
        '  echo error: pathspec did not match any tracked file 1>&2',
        '  exit /b 1',
        ')',
        'exit /b 2'
    )
    $env:PATH = "$temporaryRoot;$originalPath"

    $tracked = Test-RepositoryPathTracked -RepositoryRoot 'C:\fixture' -RelativePath 'android/keystore.properties'
    Assert-Condition (-not $tracked) 'An expected git ls-files exit 1 must mean untracked without terminating under ErrorActionPreference Stop.'

    $failedClosed = $false
    try {
        $null = Test-RepositoryPathTracked -RepositoryRoot 'C:\fixture' -RelativePath 'android/mobile-egress-release.jks'
    } catch {
        $failedClosed = $true
    }
    Assert-Condition $failedClosed 'Unexpected git failures must fail closed.'
} finally {
    $env:PATH = $originalPath
    if (Test-Path -LiteralPath $temporaryRoot) {
        Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
    }
}

Write-Host 'Android Git path check regression passed.'
exit 0
