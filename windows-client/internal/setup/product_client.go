package setup

// Build-time product selection preserves the exact same signer, elevation
// binding, mutex, protected staging and transactional file install as setup.
const (
	SetupExecutableName      = "MobileEgressClientSetup.exe"
	ControllerExecutableName = "mobile-egress-client-app.exe"
	InstallRoot              = `C:\Program Files\Mobile Egress Client`
	commonShortcutPath       = `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Mobile Egress Client.lnk`
	clientProduct            = true
)

var installedExecutableNames = []string{ControllerExecutableName, ClientExecutableName}
var verifiedReleaseExecutables = []string{SetupExecutableName, ControllerExecutableName, ClientExecutableName}
var payloadNames = []string{ControllerExecutableName, ClientExecutableName, PublicCertificateName, PublicIdentityRecordName}
