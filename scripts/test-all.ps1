[CmdletBinding()]
param(
    [ValidateSet('Windows', 'Android')]
    [string[]]$Components = @('Windows', 'Android')
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot

function Resolve-MobileEgressToolDirectory {
    param(
        [Parameter(Mandatory)]
        [string]$Name,
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string[]]$Candidates
    )

    foreach ($candidate in $Candidates) {
        if (-not [string]::IsNullOrWhiteSpace($candidate) -and (Test-Path -LiteralPath $candidate -PathType Container)) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }

    throw "$Name was not found. Checked: $($Candidates -join ', ')"
}

function Add-MobileEgressPathEntry {
    param(
        [Parameter(Mandatory)]
        [string]$PathEntry
    )

    if (-not (Test-Path -LiteralPath $PathEntry -PathType Container)) {
        throw "Required PATH directory was not found: $PathEntry"
    }

    $pathParts = @($env:Path -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($pathParts -notcontains $PathEntry) {
        $env:Path = "$PathEntry;$env:Path"
    }
}

function Initialize-MobileEgressTestAllToolchain {
    param([Parameter(Mandatory)][string[]]$Components)

    if ($Components -contains 'Android') {
        $javaHome = Resolve-MobileEgressToolDirectory -Name 'JDK 17' -Candidates @(
            'C:\Users\Chad\AppData\Local\Programs\Eclipse Adoptium\jdk-17.0.20.1+1',
            $env:JAVA_HOME
        )
        $androidHome = Resolve-MobileEgressToolDirectory -Name 'Android SDK' -Candidates @(
            'C:\Users\Chad\AppData\Local\Android\Sdk',
            $env:ANDROID_HOME,
            $env:ANDROID_SDK_ROOT
        )
        $env:JAVA_HOME = $javaHome
        $env:ANDROID_HOME = $androidHome
        $env:ANDROID_SDK_ROOT = $androidHome
        Add-MobileEgressPathEntry -PathEntry (Join-Path $javaHome 'bin')
        Add-MobileEgressPathEntry -PathEntry (Join-Path $androidHome 'platform-tools')
        Add-MobileEgressPathEntry -PathEntry (Join-Path $androidHome 'cmdline-tools\latest\bin')
    }

    if ($Components -contains 'Windows') {
        $nodeHome = Resolve-MobileEgressToolDirectory -Name 'Node.js' -Candidates @(
            'C:\Users\Chad\AppData\Roaming\nvm\nodejs',
            'C:\Users\Chad\AppData\Roaming\nvm\v23.9.0',
            $env:NODE_HOME
        )
        $goHome = Resolve-MobileEgressToolDirectory -Name 'Go' -Candidates @(
            'C:\Users\Chad\AppData\Local\Programs\Go',
            $env:GOROOT,
            'C:\Program Files\Go'
        )
        $env:NODE_HOME = $nodeHome
        $env:GOROOT = $goHome
        Add-MobileEgressPathEntry -PathEntry $nodeHome
        Add-MobileEgressPathEntry -PathEntry (Join-Path $goHome 'bin')
    }

    Write-Host 'Using local toolchain:'
    if ($Components -contains 'Android') {
        Write-Host "  JAVA_HOME=$env:JAVA_HOME"
        Write-Host "  ANDROID_HOME=$env:ANDROID_HOME"
    }
    if ($Components -contains 'Windows') {
        Write-Host "  NODE_HOME=$env:NODE_HOME"
        Write-Host "  GOROOT=$env:GOROOT"
    }
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

Push-Location $repositoryRoot
try {
    Initialize-MobileEgressTestAllToolchain -Components $Components
    Invoke-RequiredCommand -Name 'Release orchestration tests' -Command { & (Join-Path $PSScriptRoot 'test-release-all.ps1') }

    if ($Components -contains 'Windows') {
        & (Join-Path $PSScriptRoot 'preflight.ps1') -Components Go, Node
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }

        Invoke-RequiredCommand -Name 'Go tests' -Command { go test ./... }
        Invoke-RequiredCommand -Name 'Go vet' -Command { go vet ./... }
        Invoke-RequiredCommand -Name 'Go build' -Command { go build ./... }

        Push-Location (Join-Path $repositoryRoot 'windows-client\frontend')
        try {
            Invoke-RequiredCommand -Name 'Frontend tests' -Command { npm test }
            Invoke-RequiredCommand -Name 'Frontend typecheck' -Command { npm run check }
            Invoke-RequiredCommand -Name 'Frontend build' -Command { npm run build }
        } finally {
            Pop-Location
        }
    }

    if ($Components -contains 'Android') {
        & (Join-Path $PSScriptRoot 'preflight.ps1') -Components Android
        if ($LASTEXITCODE -ne 0) {
            $androidPrerequisiteExit = $LASTEXITCODE
            Write-Host 'Android unit, lint, and assemble checks were not run. Install JDK 17+ and Android SDK Platform 35 with Build-Tools 35, then set JAVA_HOME and ANDROID_HOME (or android/local.properties) before retrying scripts\test-all.ps1.'
            exit $androidPrerequisiteExit
        }

        Push-Location (Join-Path $repositoryRoot 'android')
        try {
            Invoke-RequiredCommand -Name 'Android unit tests' -Command { .\gradlew.bat testDebugUnitTest }
            Invoke-RequiredCommand -Name 'Android lint' -Command { .\gradlew.bat lintDebug }
            Invoke-RequiredCommand -Name 'Android debug assemble' -Command { .\gradlew.bat assembleDebug }
        } finally {
            Pop-Location
        }
    }
} finally {
    Pop-Location
}

Write-Host 'All integration checks passed.'
