//go:build !client_setup

package setup

const (
	SetupExecutableName      = "MobileEgressSetup.exe"
	ControllerExecutableName = "mobile-egress-windows.exe"
	InstallRoot              = `C:\Program Files\MobileEgress\Controller`
	commonShortcutPath       = `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Mobile Egress.lnk`
	clientProduct            = false
)

var installedExecutableNames = []string{ControllerExecutableName, AdminExecutableName, RelayExecutableName}
var verifiedReleaseExecutables = []string{SetupExecutableName, ControllerExecutableName, AdminExecutableName, RelayExecutableName}
var payloadNames = []string{ControllerExecutableName, AdminExecutableName, RelayExecutableName, ClientExecutableName, ManifestName, PublicCertificateName, PublicIdentityRecordName}
