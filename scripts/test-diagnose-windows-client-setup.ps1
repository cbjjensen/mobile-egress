$ErrorActionPreference = 'Stop'

function Assert-Condition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "Assertion failed: $Message" }
}

$diagnosticScript = Join-Path $PSScriptRoot 'diagnose-windows-client-setup.ps1'
Assert-Condition (Test-Path -LiteralPath $diagnosticScript -PathType Leaf) 'The read-only setup diagnostic script must exist.'
. $diagnosticScript

# These replace host reads, not report generation, so tests require no service,
# certificates, installed app, privileges, network, or signed executable.
$readers = @{
    FileReader = { param($Path) [pscustomobject]@{ Length = 25913632; Hash = '59365093e3c2cf9b448f9f5c776c5c54f11df557ca767d5ece353f6ca7a6011f'; PrivatePath = $Path } }
    SignatureReader = { param($Path) [pscustomobject]@{ Status = 'Valid'; SignerCertificate = [pscustomobject]@{ Thumbprint = '85F220C1BF05A5D3A86B5DD408787EC1B122ECB7'; Subject = 'private subject' }; TimeStamperCertificate = [pscustomobject]@{ Thumbprint = '001122' } } }
    OSReader = { [pscustomobject]@{ Version = '10.0.20348'; BuildNumber = '20348'; CSName = 'private-machine' } }
    ElevationReader = { $true }
    TrustReader = { param($Store, $Thumbprint) [pscustomobject]@{ state = 'present'; certificatePresent = $Store -eq 'Root' } }
    ServiceReader = { [pscustomobject]@{ Name = 'MobileEgressClient'; State = 'Stopped'; ExitCode = 1067; ServiceSpecificExitCode = 5; PathName = 'private executable path'; StartName = 'private-account' } }
    EventReader = { @() }
}
$report = Get-MobileEgressSetupDiagnostic -InstallerPath 'C:\private-user\installer.exe' @readers
Assert-Condition ($report.installer.matchesPublished207 -eq $true) 'The exact published 2.0.7 bytes must match.'
Assert-Condition ($report.signature.status -eq 'Valid' -and $report.signature.expectedSigner -eq $true -and $report.signature.timestampPresent -eq $true) 'Signature status, exact signer and timestamp presence must be reported separately.'
Assert-Condition ($report.trust.localMachineRoot.certificatePresent -eq $true -and $report.trust.localMachineTrustedPublisher.certificatePresent -eq $false) 'The two exact publisher trust stores must remain distinct.'
Assert-Condition ($report.service.exists -eq $true -and $report.service.exitCode -eq 1067 -and $report.service.serviceSpecificExitCode -eq 5) 'The exact service failure codes must survive reporting.'
Assert-Condition ($report.os.version -eq '10.0.20348' -and $report.elevated -eq $true) 'OS and token facts must be reported.'
$json = $report | ConvertTo-Json -Depth 8
Assert-Condition ($json -notmatch 'private-|PrivatePath|StartName|PathName|Subject|CSName') 'The report must never serialize identity, paths or certificate details from providers.'

$readers.FileReader = { param($Path) [pscustomobject]@{ Length = 25913632; Hash = ('0' * 64) } }
$readers.SignatureReader = { param($Path) [pscustomobject]@{ Status = 'NotTrusted'; SignerCertificate = [pscustomobject]@{ Thumbprint = ('0' * 40) }; TimeStamperCertificate = $null } }
$readers.ServiceReader = { $null }
$report = Get-MobileEgressSetupDiagnostic -InstallerPath 'C:\private-user\installer.exe' @readers
Assert-Condition ($report.installer.matchesPublished207 -eq $false) 'Matching size alone must never identify the release.'
Assert-Condition ($report.signature.expectedSigner -eq $false -and $report.signature.status -eq 'NotTrusted' -and $report.signature.timestampPresent -eq $false) 'Untrusted or wrong-signer evidence must not be converted to success.'
Assert-Condition ($report.service.exists -eq $false) 'Missing service must be distinct from failure to read the service.'

$readers.FileReader = { param($Path) [pscustomobject]@{ Length = 12; Hash = '59365093e3c2cf9b448f9f5c776c5c54f11df557ca767d5ece353f6ca7a6011f' } }
$readers.SignatureReader = { param($Path) [pscustomobject]@{ Status = 'NotSigned'; SignerCertificate = $null; TimeStamperCertificate = $null } }
$report = Get-MobileEgressSetupDiagnostic -InstallerPath 'C:\private-user\installer.exe' @readers
Assert-Condition ($report.installer.matchesPublished207 -eq $false) 'An inconsistent size must not identify the release even if a reader returns the expected hash.'
Assert-Condition ($report.signature.status -eq 'NotSigned' -and $null -eq $report.signature.signerThumbprint) 'Unsigned files must remain distinguishable from unreadable signature metadata.'

$readers.TrustReader = { param($Store, $Thumbprint) [pscustomobject]@{ state = 'absent'; certificatePresent = $null } }
$report = Get-MobileEgressSetupDiagnostic -InstallerPath 'C:\private-user\installer.exe' @readers
Assert-Condition ($report.trust.localMachineRoot.state -eq 'absent' -and $null -eq $report.trust.localMachineRoot.certificatePresent) 'A missing certificate store must be distinct from a missing certificate in an existing store.'

foreach ($key in @('FileReader', 'SignatureReader', 'OSReader', 'ElevationReader', 'TrustReader', 'ServiceReader', 'EventReader')) {
    $readers[$key] = { throw 'private-account: denied C:\private-user\secret-path' }
}
$report = Get-MobileEgressSetupDiagnostic -InstallerPath 'C:\private-user\installer.exe' @readers
Assert-Condition ($null -eq $report.installer.matchesPublished207 -and $null -eq $report.service.exists -and $null -eq $report.elevated) 'Inaccessible data must be unknown, not success or absence.'
Assert-Condition ($report.trust.localMachineRoot.state -eq 'unavailable' -and $null -eq $report.trust.localMachineRoot.certificatePresent) 'Inaccessible trust stores must not be reported as absent.'
Assert-Condition ($report.unavailable.Count -eq 8) 'Each failed probe, including both certificate stores, must remain diagnosable.'
Assert-Condition (($report | ConvertTo-Json -Depth 8) -notmatch 'private-|secret-path') 'Provider exception messages must never enter the support report.'

$eventXml = '<Event><System><Provider Name="Service Control Manager"/><EventID>7023</EventID><TimeCreated SystemTime="2026-10-11T14:00:00.0000000Z"/></System><EventData><Data Name="param1">Inevitable Mobile Relay</Data><Data Name="param2">%%5</Data><Data Name="private">private-account</Data></EventData></Event>'
$event = ConvertTo-MobileEgressSetupEvent -Xml $eventXml
Assert-Condition ($event.id -eq 7023 -and $event.errorCodes[0] -eq 5) 'Matching service failure events must retain the numeric failure code.'
$qualifiedEventXml = $eventXml.Replace('<Event>', '<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event">').Replace('<EventID>', '<EventID Qualifiers="49152">')
$qualifiedEvent = ConvertTo-MobileEgressSetupEvent -Xml $qualifiedEventXml
Assert-Condition ($qualifiedEvent.id -eq 7023 -and $qualifiedEvent.errorCodes[0] -eq 5) 'Real SCM events with an XML namespace and EventID Qualifiers must retain their failure code.'
Assert-Condition (($event | ConvertTo-Json -Depth 8) -notmatch 'private-|param1|param2|Inevitable') 'Event output must contain only event ID, time and numeric failure codes.'
Assert-Condition ($null -eq (ConvertTo-MobileEgressSetupEvent -Xml ($eventXml.Replace('Inevitable Mobile Relay', 'Inevitable Mobile Relay Other')))) 'Similar names must not expose unrelated service events.'
Assert-Condition ($null -eq (ConvertTo-MobileEgressSetupEvent -Xml ($eventXml.Replace('Service Control Manager', 'Other Provider')))) 'Only SCM provider events are relevant.'
$event = ConvertTo-MobileEgressSetupEvent -Xml ($eventXml.Replace('%%5', 'Access denied private-account'))
Assert-Condition ($event.errorCodes.Count -eq 0) 'Freeform event details must not be serialized as error codes.'
Assert-Condition ($null -eq (ConvertTo-MobileEgressSetupEvent -Xml '<invalid>')) 'Malformed event data must be ignored safely.'

Write-Output 'Windows setup diagnostic tests passed.'
