#requires -Version 5.1
<#
.SYNOPSIS
Prints a read-only, minimal support report for Windows Client setup failures.
.DESCRIPTION
Run from ordinary Windows PowerShell with the installer path. This does not run
the installer, alter trust, change services, or change security settings. It
omits account/machine names, file paths, raw event messages and certificates.
No explicit network requests are made. Get-AuthenticodeSignature uses Windows
trust verification, whose certificate retrieval behavior follows OS policy.
The release comparison is pinned to the published Windows 2.0.7 installer.
.EXAMPLE
& .\diagnose-windows-client-setup.ps1 -InstallerPath "$env:USERPROFILE\Downloads\InevitableMobileRelaySetup.exe"
#>
param([string]$InstallerPath)

function Read-MobileEgressSetupTrust {
    param([ValidateSet('Root', 'TrustedPublisher')][string]$Store, [string]$Thumbprint)
    $certificateStore = New-Object Security.Cryptography.X509Certificates.X509Store($Store, [Security.Cryptography.X509Certificates.StoreLocation]::LocalMachine)
    try {
        # OpenExistingOnly is critical: even a diagnostic must not create a
        # missing store, which is itself evidence for this installer failure.
        $flags = [Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly -bor [Security.Cryptography.X509Certificates.OpenFlags]::OpenExistingOnly
        $certificateStore.Open($flags)
        $found = $certificateStore.Certificates.Find([Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint, $Thumbprint, $false)
        [pscustomobject]@{ state = 'present'; certificatePresent = $found.Count -gt 0 }
    } catch {
        $failure = $_.Exception
        while ($null -ne $failure.InnerException) { $failure = $failure.InnerException }
        # CRYPT_E_NOT_FOUND means the requested existing store does not exist.
        # All other errors stay unknown; access denied is not evidence of absence.
        if (([long]$failure.HResult -band 4294967295L) -eq 2148081668L) {
            [pscustomobject]@{ state = 'absent'; certificatePresent = $null }
        } else { throw }
    } finally { $certificateStore.Close() }
}

function ConvertTo-MobileEgressSetupEvent {
    param([string]$Xml)
    # Only a tiny allowlist of SCM failures is useful here. Never emit messages,
    # binary payloads, account names, service paths or unrelated services.
    if ([string]::IsNullOrWhiteSpace($Xml) -or $Xml.Length -gt 65536) { return $null }
    try {
        $document = New-Object System.Xml.XmlDocument
        $document.XmlResolver = $null
        $document.LoadXml($Xml)
        if ($document.Event.System.Provider.Name -cne 'Service Control Manager') { return $null }
        $eventID = $document.SelectSingleNode('/*[local-name()="Event"]/*[local-name()="System"]/*[local-name()="EventID"]')
        $id = [int]$eventID.InnerText
        if ($id -notin @(7000, 7009, 7011, 7023, 7024, 7031, 7034)) { return $null }
        $data = @{}
        foreach ($item in @($document.Event.EventData.Data)) { $data[[string]$item.Name] = [string]$item.InnerText }
        $serviceField = if ($id -in @(7009, 7011)) { 'param2' } else { 'param1' }
        if ($data[$serviceField] -cnotin @('MobileEgressClient', 'Inevitable Mobile Relay')) { return $null }
        $created = [DateTimeOffset]::Parse([string]$document.Event.System.TimeCreated.SystemTime, [Globalization.CultureInfo]::InvariantCulture)
        $codes = @()
        # Other event parameters contain timeout durations or restart counts,
        # not error codes. Preserve only the documented failure-code slot.
        if ($id -in @(7000, 7023, 7024) -and $data['param2'] -match '^(?:%%)?([0-9]{1,10})$') {
            $code = [uint64]$Matches[1]
            if ($code -le [uint32]::MaxValue) { $codes = @([uint32]$code) }
        }
        [pscustomobject]@{ id = $id; timeUtc = $created.UtcDateTime.ToString('o'); errorCodes = @($codes) }
    } catch { return $null }
}

function Get-MobileEgressSetupDiagnostic {
    param(
        [Parameter(Mandatory = $true)][string]$InstallerPath,
        [scriptblock]$FileReader = {
            param($Path)
            $file = Get-Item -LiteralPath $Path -ErrorAction Stop
            if ($file.PSIsContainer) { throw 'Installer is not a file.' }
            [pscustomobject]@{ Length = $file.Length; Hash = (Get-FileHash -LiteralPath $Path -Algorithm SHA256 -ErrorAction Stop).Hash }
        },
        [scriptblock]$SignatureReader = { param($Path) Get-AuthenticodeSignature -LiteralPath $Path -ErrorAction Stop },
        [scriptblock]$OSReader = { Get-CimInstance -ClassName Win32_OperatingSystem -Property Version, BuildNumber -ErrorAction Stop },
        [scriptblock]$ElevationReader = {
            $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
            try {
                $principal = New-Object Security.Principal.WindowsPrincipal($identity)
                $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
            } finally { $identity.Dispose() }
        },
        [scriptblock]$TrustReader = {
            param($Store, $Thumbprint)
            Read-MobileEgressSetupTrust -Store $Store -Thumbprint $Thumbprint
        },
        [scriptblock]$ServiceReader = {
            Get-CimInstance -ClassName Win32_Service -Filter "Name='MobileEgressClient'" -Property Name, State, ExitCode, ServiceSpecificExitCode -ErrorAction Stop
        },
        [scriptblock]$EventReader = {
            try {
                Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Service Control Manager'; Id = @(7000, 7009, 7011, 7023, 7024, 7031, 7034); StartTime = (Get-Date).AddHours(-24) } -MaxEvents 200 -ErrorAction Stop |
                    ForEach-Object { $_.ToXml() }
            } catch {
                if ($_.FullyQualifiedErrorId -notlike 'NoMatchingEventsFound*') { throw }
            }
        }
    )
    $expectedThumbprint = '85F220C1BF05A5D3A86B5DD408787EC1B122ECB7'
    $report = [ordered]@{
        schemaVersion = 1
        expectedRelease = '2.0.7'
        installer = [ordered]@{ sizeBytes = $null; sha256 = $null; matchesPublished207 = $null }
        signature = [ordered]@{ status = $null; signerThumbprint = $null; expectedSigner = $null; timestampPresent = $null }
        os = [ordered]@{ version = $null; build = $null }
        elevated = $null
        trust = [ordered]@{
            localMachineRoot = [ordered]@{ state = 'unavailable'; certificatePresent = $null }
            localMachineTrustedPublisher = [ordered]@{ state = 'unavailable'; certificatePresent = $null }
        }
        service = [ordered]@{ exists = $null; state = $null; exitCode = $null; serviceSpecificExitCode = $null }
        recentServiceFailures = @()
        unavailable = @()
    }
    try {
        $file = & $FileReader $InstallerPath
        $hash = ([string]$file.Hash).ToLowerInvariant()
        if ($hash -notmatch '^[a-f0-9]{64}$') { throw 'Invalid hash.' }
        $size = [long]$file.Length
        $report.installer.sizeBytes = $size
        $report.installer.sha256 = $hash
        $report.installer.matchesPublished207 = $size -eq 25913632 -and $hash -ceq '59365093e3c2cf9b448f9f5c776c5c54f11df557ca767d5ece353f6ca7a6011f'
    } catch { $report.unavailable += 'installer' }
    try {
        $signature = & $SignatureReader $InstallerPath
        $status = [string]$signature.Status
        if ($status -notin @('Valid', 'UnknownError', 'NotSigned', 'HashMismatch', 'NotTrusted', 'NotSupportedFileFormat', 'Incompatible')) { throw 'Unknown signature status.' }
        $thumbprint = [string]$signature.SignerCertificate.Thumbprint
        if ($thumbprint -and $thumbprint -notmatch '^[A-Fa-f0-9]{40}$') { throw 'Invalid thumbprint.' }
        $report.signature.status = $status
        $report.signature.signerThumbprint = if ($thumbprint) { $thumbprint.ToUpperInvariant() } else { $null }
        $report.signature.expectedSigner = $thumbprint -eq $expectedThumbprint
        $report.signature.timestampPresent = $null -ne $signature.TimeStamperCertificate
    } catch { $report.unavailable += 'signature' }
    try {
        $os = & $OSReader
        if ([string]$os.Version -notmatch '^\d+\.\d+\.\d+(?:\.\d+)?$' -or [string]$os.BuildNumber -notmatch '^\d+$') { throw 'Invalid OS version.' }
        $report.os.version = [string]$os.Version
        $report.os.build = [string]$os.BuildNumber
    } catch { $report.unavailable += 'os' }
    try { $report.elevated = [bool](& $ElevationReader) } catch { $report.unavailable += 'elevated' }
    foreach ($store in @('Root', 'TrustedPublisher')) {
        $key = "localMachine$store"
        try {
            $trust = & $TrustReader $store $expectedThumbprint
            if ($trust.state -notin @('present', 'absent')) { throw 'Unknown store state.' }
            $report.trust[$key].state = [string]$trust.state
            $report.trust[$key].certificatePresent = if ($trust.state -eq 'present') { [bool]$trust.certificatePresent } else { $null }
        } catch { $report.unavailable += $key }
    }
    try {
        $service = & $ServiceReader
        if ($null -eq $service) { $report.service.exists = $false } else {
            if ($service.Name -cne 'MobileEgressClient' -or $service.State -notin @('Stopped', 'Start Pending', 'Stop Pending', 'Running', 'Continue Pending', 'Pause Pending', 'Paused', 'Unknown')) { throw 'Unexpected service.' }
            $report.service.state = [string]$service.State
            $report.service.exitCode = [uint32]$service.ExitCode
            $report.service.serviceSpecificExitCode = [uint32]$service.ServiceSpecificExitCode
            $report.service.exists = $true
        }
    } catch { $report.unavailable += 'service' }
    try {
        $report.recentServiceFailures = @(& $EventReader | Select-Object -First 200 | ForEach-Object { ConvertTo-MobileEgressSetupEvent -Xml $_ } | Where-Object { $null -ne $_ } | Select-Object -First 20)
    } catch { $report.unavailable += 'recentServiceFailures' }
    [pscustomobject]$report
}

if ($MyInvocation.InvocationName -ne '.') {
    if ([string]::IsNullOrWhiteSpace($InstallerPath)) { throw 'Supply -InstallerPath with the downloaded installer file.' }
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Run this diagnostic in Windows PowerShell.' }
    Get-MobileEgressSetupDiagnostic -InstallerPath $InstallerPath | ConvertTo-Json -Depth 6
}
