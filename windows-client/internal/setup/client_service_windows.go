//go:build windows

package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const clientStateDirectory = `C:\ProgramData\MobileEgressClient`
const standaloneClientInstallRoot = `C:\Program Files\Mobile Egress Client`
const legacyClientStateDirectory = `C:\ProgramData\MobileEgress\Client`
const legacyClientExecutable = `C:\Program Files\MobileEgress\mobile-egress-client.exe`

type clientServiceInstall struct {
	manager                      clientServiceManager
	service                      clientWindowsService
	previous                     mgr.Config
	wasRunning, created, stopped bool
	stateDirectory               string
}

// Keep SCM operations behind their native boundary so registration and
// rollback can be exercised without installing a service on the test host.
type clientWindowsService interface {
	Query() (svc.Status, error)
	Control(svc.Cmd) (svc.Status, error)
	UpdateConfig(mgr.Config) error
	SetRecoveryActions([]mgr.RecoveryAction, uint32) error
	Start(...string) error
	Delete() error
	Close() error
}

type clientServiceManager interface {
	CreateService(string, string, mgr.Config, ...string) (clientWindowsService, error)
	Disconnect() error
}

type nativeClientServiceManager struct{ *mgr.Mgr }

func (m nativeClientServiceManager) CreateService(name, path string, config mgr.Config, args ...string) (clientWindowsService, error) {
	service, err := m.Mgr.CreateService(name, path, config, args...)
	if err != nil {
		return nil, err
	}
	return service, nil
}

func standaloneClientCommand() string {
	return standaloneClientCommandForState(clientStateDirectory)
}
func standaloneClientCommandForState(stateDir string) string {
	return syscall.EscapeArg(filepath.Join(standaloneClientInstallRoot, ClientExecutableName)) + ` serve --standalone --state-dir ` + syscall.EscapeArg(stateDir)
}

func validateExistingClientCommand(command string) error {
	_, err := existingClientStateDirectory(command)
	return err
}
func existingClientStateDirectory(command string) (string, error) {
	args, err := windows.DecomposeCommandLine(command)
	if err == nil && len(args) == 4 && strings.EqualFold(args[0], legacyClientExecutable) && args[1] == "serve" && args[2] == "--state-dir" && strings.EqualFold(args[3], legacyClientStateDirectory) {
		return legacyClientStateDirectory, nil
	}
	if err == nil && len(args) == 5 && strings.EqualFold(args[0], filepath.Join(standaloneClientInstallRoot, ClientExecutableName)) && args[1] == "serve" && args[2] == "--standalone" && args[3] == "--state-dir" {
		for _, allowed := range []string{clientStateDirectory, legacyClientStateDirectory} {
			if strings.EqualFold(args[4], allowed) {
				return allowed, nil
			}
		}
	}
	return "", errors.New("The existing Client service has an unrecognized installation or state path. Restore its known installation before migrating.")
}

func prepareClientService() (*clientServiceInstall, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, errors.New("connect to Windows service manager")
	}
	transaction := &clientServiceInstall{manager: nativeClientServiceManager{manager}, stateDirectory: clientStateDirectory}
	service, err := manager.OpenService("MobileEgressClient")
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		manager.Disconnect()
		return nil, errors.New("inspect existing Client service")
	}
	if err == nil {
		transaction.service = service
		transaction.previous, err = service.Config()
		if err == nil {
			transaction.stateDirectory, err = existingClientStateDirectory(transaction.previous.BinaryPathName)
			if err == nil && !strings.EqualFold(transaction.previous.ServiceStartName, "LocalSystem") && !strings.EqualFold(transaction.previous.ServiceStartName, `NT AUTHORITY\SYSTEM`) {
				err = errors.New("The existing Client service must use LocalSystem to preserve its protected credentials.")
			}
		}
		if err == nil {
			var status svc.Status
			status, err = service.Query()
			transaction.wasRunning = status.State != svc.Stopped
		}
		if err != nil {
			service.Close()
			manager.Disconnect()
			return nil, err
		}
	}
	return transaction, nil
}

func (s *clientServiceInstall) stop() error {
	if s.service == nil {
		return nil
	}
	status, err := s.service.Query()
	if err != nil {
		return err
	}
	if status.State != svc.Stopped {
		if _, err := s.service.Control(svc.Stop); err != nil {
			return err
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			status, err = s.service.Query()
			if err != nil {
				return err
			}
			if status.State == svc.Stopped {
				s.stopped = true
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return errors.New("Client service did not stop before the update")
	}
	s.stopped = true
	return nil
}

func protectClientOwner() error {
	return protectClientOwnerAt(clientStateDirectory)
}
func protectClientOwnerAt(stateDirectory string) error {
	// Only the service/admin can write the persisted owner and DPAPI ciphertext.
	// No enrollment/bootstrap operation is invoked during installation or repair.
	if info, err := os.Lstat(stateDirectory); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Client state directory is unsafe")
		}
		sd, securityErr := windows.GetNamedSecurityInfo(stateDirectory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if securityErr != nil {
			return securityErr
		}
		owner, _, securityErr := sd.Owner()
		if securityErr != nil || (!owner.IsWellKnown(windows.WinLocalSystemSid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
			return errors.New("existing Client state directory is not owned by the system")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(stateDirectory, 0700); err != nil {
		return err
	}
	// Administrators is an assignable owner in the elevated token; assigning
	// SYSTEM here would require enabling SeRestorePrivilege during setup.
	sd, err := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(stateDirectory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, dacl, nil); err != nil {
		return err
	}
	ownerPath := filepath.Join(stateDirectory, "owner.sid")
	if info, err := os.Lstat(ownerPath); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("Client owner file is unsafe")
		}
		raw, err := os.ReadFile(ownerPath)
		if err != nil {
			return err
		}
		_, err = windows.StringToSid(strings.TrimSpace(string(raw)))
		return err // Existing owner survives updates and repair.
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(ownerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(user.User.Sid.String())
	return errors.Join(writeErr, file.Close())
}

func (s *clientServiceInstall) install() error {
	if s.stateDirectory == "" {
		s.stateDirectory = clientStateDirectory
	}
	if err := protectClientOwnerAt(s.stateDirectory); err != nil {
		return err
	}
	return s.installRegistration()
}

func (s *clientServiceInstall) installRegistration() error {
	config := mgr.Config{
		DisplayName:      "Inevitable Mobile Relay",
		Description:      "Inevitable Mobile Relay cellular workload proxy",
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "LocalSystem",
		BinaryPathName:   standaloneClientCommandForState(s.stateDirectory),
		// UpdateConfig passes these values directly to Windows. The firewall's
		// service-specific rule requires a service SID in the started token.
		ServiceType: windows.SERVICE_WIN32_OWN_PROCESS,
		SidType:     windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	var err error
	if s.service == nil {
		s.service, err = s.manager.CreateService("MobileEgressClient", filepath.Join(standaloneClientInstallRoot, ClientExecutableName), config, "serve", "--standalone", "--state-dir", s.stateDirectory)
		if err != nil {
			return errors.New("register Client background service")
		}
		s.created = true
	} else if err := s.service.UpdateConfig(config); err != nil {
		return errors.New("update Client background service")
	}
	if err := s.service.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}, {Type: mgr.ServiceRestart, Delay: 30 * time.Second}}, 86400); err != nil {
		return err
	}
	if err := s.service.Start(); err != nil {
		return errors.New("start Client background service")
	}
	return nil
}

func (s *clientServiceInstall) close(failed bool) error {
	defer s.manager.Disconnect()
	if s.service == nil {
		return nil
	}
	defer s.service.Close()
	if !failed {
		return nil
	}
	var err error
	if s.created {
		err = s.service.Delete()
	} else if s.stopped {
		err = s.service.UpdateConfig(s.previous)
		if err == nil && s.wasRunning {
			err = s.service.Start()
		}
	}
	if err != nil {
		return errors.Join(ErrInstallRollback, errors.New("restore previous Client service registration"))
	}
	return nil
}
