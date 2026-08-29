[CmdletBinding()]
param(
    [ValidateSet('All', 'Go', 'Node', 'Docker', 'WebView2', 'Android')]
    [string[]]$Components = @('All'),
    [ValidateSet('None', 'Android')]
    [string]$SimulateMissing = 'None',
    [switch]$SimulateJavaHomeMismatch,
    [switch]$SimulateDockerUnavailable
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'operations-common.ps1')
$missing = [System.Collections.Generic.List[string]]::new()
$invalid = [System.Collections.Generic.List[string]]::new()

function Add-Missing {
    param([string]$Message)
    $missing.Add($Message)
}

function Add-Invalid {
    param([string]$Message)
    $invalid.Add($Message)
}

function Get-MajorVersion {
    param([string]$Value)
    $match = [regex]::Match($Value, '(?<major>\d+)')
    if (-not $match.Success) {
        return $null
    }

    return [int]$match.Groups['major'].Value
}

function Test-CommandVersion {
    param(
        [string]$Command,
        [string[]]$Arguments,
        [int]$MinimumVersion,
        [string]$Label,
        [string]$Remediation
    )

    $commandInfo = Get-Command $Command -ErrorAction SilentlyContinue
    if ($null -eq $commandInfo) {
        Add-Missing "$Label. $Remediation"
        return
    }

    $output = & $Command @Arguments 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) {
        Add-Invalid "$Label could not be validated. $Remediation"
        return
    }

    $major = Get-MajorVersion $output
    if ($null -eq $major) {
        Add-Invalid "$Label returned an unrecognized version. $Remediation"
        return
    }

    if ($major -lt $MinimumVersion) {
        Add-Invalid "$Label version $major is below the required $MinimumVersion. $Remediation"
        return
    }

    Write-Host "OK: $Label version $major"
}

function Test-Go {
    $go = Get-Command 'go' -ErrorAction SilentlyContinue
    if ($null -eq $go) {
        Add-Missing 'Go. Install Go 1.26 or later and ensure go.exe is on PATH.'
        return
    }

    $output = & go version 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) {
        Add-Invalid 'Go could not be validated. Install Go 1.26 or later and ensure go.exe is on PATH.'
        return
    }

    $match = [regex]::Match($output, 'go1\.(?<minor>\d+)')
    if (-not $match.Success) {
        Add-Invalid 'Go returned an unrecognized version. Install Go 1.26 or later and ensure go.exe is on PATH.'
        return
    }

    $minor = [int]$match.Groups['minor'].Value
    if ($minor -lt 26) {
        Add-Invalid "Go version 1.$minor is below the required 1.26. Install Go 1.26 or later and ensure go.exe is on PATH."
        return
    }

    Write-Host "OK: Go version $minor"
}

function Test-Node {
    Test-CommandVersion -Command 'node' -Arguments @('--version') -MinimumVersion 22 -Label 'Node.js' -Remediation 'Install Node.js 22 or later and ensure node.exe is on PATH.'
}

function Test-Jdk {
    if ($SimulateJavaHomeMismatch) {
        Add-Invalid 'JAVA_HOME points to JDK 8, below the required 17. Set JAVA_HOME to JDK 17 or later; PATH JDK 17 is ignored while JAVA_HOME is set.'
        return
    }

    $compilerSource = 'PATH'
    if (-not [string]::IsNullOrWhiteSpace($env:JAVA_HOME)) {
        $javacPath = Join-Path $env:JAVA_HOME 'bin\javac.exe'
        $compilerSource = 'JAVA_HOME'
        if (-not (Test-Path -LiteralPath $javacPath -PathType Leaf)) {
            Add-Invalid 'JAVA_HOME is set but bin\javac.exe is missing. Set JAVA_HOME to a JDK 17 or later directory; PATH is ignored while JAVA_HOME is set.'
            return
        }
    } else {
        $javac = Get-Command 'javac' -ErrorAction SilentlyContinue
        if ($null -eq $javac) {
            Add-Missing 'JDK 17 or later. Install a JDK (not only a JRE), set JAVA_HOME, and ensure javac.exe is on PATH.'
            return
        }

        $javacPath = $javac.Source
    }

    if ([string]::IsNullOrWhiteSpace($javacPath)) {
        Add-Missing 'JDK 17 or later. Install a JDK (not only a JRE), set JAVA_HOME, and ensure javac.exe is on PATH.'
        return
    }

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $javacPath -version 2>&1 | Out-String
        $javacExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }

    if ($javacExitCode -ne 0) {
        Add-Invalid "$compilerSource JDK could not be validated. Install JDK 17 or later, set JAVA_HOME, and ensure javac.exe is on PATH."
        return
    }

    $match = [regex]::Match($output, 'javac\s+(?<version>(?:1\.)?\d+)')
    if (-not $match.Success) {
        Add-Invalid "$compilerSource JDK returned an unrecognized version. Install JDK 17 or later, set JAVA_HOME, and ensure javac.exe is on PATH."
        return
    }

    $version = $match.Groups['version'].Value
    $major = if ($version.StartsWith('1.')) { [int]$version.Substring(2) } else { [int]$version }
    if ($major -lt 17) {
        if ($compilerSource -eq 'JAVA_HOME') {
            Add-Invalid "JAVA_HOME points to JDK $major, below the required 17. Set JAVA_HOME to JDK 17 or later; PATH is ignored while JAVA_HOME is set."
        } else {
            Add-Invalid "JDK version $major is below the required 17. Install JDK 17 or later, set JAVA_HOME, and ensure javac.exe is on PATH."
        }
        return
    }

    Write-Host "OK: $compilerSource JDK version $major"
}

function Test-Docker {
    if ($SimulateDockerUnavailable) {
        Add-Invalid 'Docker Engine could not be validated. Start Docker Desktop (or the Docker daemon) and retry.'
        Write-Host 'OK: Docker Compose v2 is available'
        return
    }

    $docker = Get-Command 'docker' -ErrorAction SilentlyContinue
    if ($null -eq $docker) {
        Add-Missing 'Docker Engine and Docker Compose. Install Docker Desktop or Docker Engine with the Compose v2 plugin.'
        return
    }

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $dockerVersion = & docker version --format '{{.Client.Version}}' 2>&1 | Out-String
        $dockerExitCode = $LASTEXITCODE
        $composeVersion = & docker compose version 2>&1 | Out-String
        $composeExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }

    if ($dockerExitCode -ne 0 -or [string]::IsNullOrWhiteSpace($dockerVersion)) {
        Add-Invalid 'Docker Engine could not be validated. Start Docker Desktop (or the Docker daemon) and retry.'
    } else {
        Write-Host 'OK: Docker Engine CLI is available'
    }

    if ($composeExitCode -ne 0 -or [string]::IsNullOrWhiteSpace($composeVersion)) {
        Add-Missing 'Docker Compose v2. Install the Docker Compose plugin and retry.'
    } else {
        Write-Host 'OK: Docker Compose v2 is available'
    }
}

function Test-WebView2 {
    $programFilesRoots = @($env:ProgramFiles, ${env:ProgramFiles(x86)}) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    $runtimeFound = $false

    foreach ($root in $programFilesRoots) {
        $applicationRoot = Join-Path $root 'Microsoft\EdgeWebView\Application'
        if (-not (Test-Path -LiteralPath $applicationRoot -PathType Container)) {
            continue
        }

        $runtimeFound = $null -ne (Get-ChildItem -LiteralPath $applicationRoot -Directory -ErrorAction SilentlyContinue |
            Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName 'msedgewebview2.exe') -PathType Leaf } |
            Select-Object -First 1)
        if ($runtimeFound) {
            break
        }
    }

    if ($runtimeFound) {
        Write-Host 'OK: Microsoft Edge WebView2 Evergreen Runtime is installed'
    } else {
        Add-Missing 'Microsoft Edge WebView2 Evergreen Runtime. Install the Evergreen Runtime before running or packaging the Windows client.'
    }
}

function Test-Android {
    if ($SimulateMissing -eq 'Android') {
        Add-Missing 'JDK 17 or later. Install a JDK (not only a JRE), set JAVA_HOME, and ensure javac.exe is on PATH.'
        Add-Missing (Get-MobileEgressAndroidSdkRemediation)
        return
    }

    Test-Jdk

    $sdkRoot = Get-MobileEgressAndroidSdkRoot -RepositoryRoot $repositoryRoot
    if ([string]::IsNullOrWhiteSpace($sdkRoot)) {
        Add-Missing (Get-MobileEgressAndroidSdkRemediation)
        return
    }

    $platform = Join-Path $sdkRoot 'platforms\android-35\android.jar'
    $buildToolsRoot = Join-Path $sdkRoot 'build-tools'
    $buildTools = @()
    if (Test-Path -LiteralPath $buildToolsRoot -PathType Container) {
        $buildTools = Get-ChildItem -LiteralPath $buildToolsRoot -Directory -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -match '^35(\.|$)' -and (Test-Path -LiteralPath (Join-Path $_.FullName 'apksigner.bat') -PathType Leaf) }
    }

    if (-not (Test-Path -LiteralPath $platform -PathType Leaf) -or $buildTools.Count -eq 0) {
        Add-Missing (Get-MobileEgressAndroidSdkRemediation)
        return
    }

    Write-Host 'OK: Android SDK Platform 35 and Build-Tools 35 are installed'
}

$requestedComponents = if ($Components -contains 'All') {
    @('Go', 'Node', 'Docker', 'WebView2', 'Android')
} else {
    $Components | Select-Object -Unique
}

foreach ($component in $requestedComponents) {
    switch ($component) {
        'Go' { Test-Go }
        'Node' { Test-Node }
        'Docker' { Test-Docker }
        'WebView2' { Test-WebView2 }
        'Android' { Test-Android }
    }
}

foreach ($message in $missing) {
    Write-Host "MISSING: $message"
}

foreach ($message in $invalid) {
    Write-Host "INVALID: $message"
}

if ($invalid.Count -gt 0) {
    exit 11
}

if ($missing.Count -gt 0) {
    exit 10
}

Write-Host 'Prerequisite validation passed.'
