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

type clientServiceInstall struct {
	manager                      *mgr.Mgr
	service                      *mgr.Service
	previous                     mgr.Config
	wasRunning, created, stopped bool
}

func standaloneClientCommand() string {
	return syscall.EscapeArg(filepath.Join(standaloneClientInstallRoot, ClientExecutableName)) + ` serve --standalone --state-dir ` + syscall.EscapeArg(clientStateDirectory)
}

func validateExistingClientCommand(command string) error {
	if !strings.EqualFold(command, standaloneClientCommand()) {
		return errors.New("an existing AWS or other Client installation uses this service; keep managing it through its existing installation")
	}
	return nil
}

func prepareClientService() (*clientServiceInstall, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, errors.New("connect to Windows service manager")
	}
	transaction := &clientServiceInstall{manager: manager}
	service, err := manager.OpenService("MobileEgressClient")
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		manager.Disconnect()
		return nil, errors.New("inspect existing Client service")
	}
	if err == nil {
		transaction.service = service
		transaction.previous, err = service.Config()
		if err == nil {
			err = validateExistingClientCommand(transaction.previous.BinaryPathName)
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
	// Only the service/admin can write the persisted owner and DPAPI ciphertext.
	// No enrollment/bootstrap operation is invoked during installation or repair.
	if info, err := os.Lstat(clientStateDirectory); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Client state directory is unsafe")
		}
		sd, securityErr := windows.GetNamedSecurityInfo(clientStateDirectory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
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
	if err := os.MkdirAll(clientStateDirectory, 0700); err != nil {
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
	if err := windows.SetNamedSecurityInfo(clientStateDirectory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, dacl, nil); err != nil {
		return err
	}
	ownerPath := filepath.Join(clientStateDirectory, "owner.sid")
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
	if err := protectClientOwner(); err != nil {
		return err
	}
	config := mgr.Config{DisplayName: "Mobile Egress Client", Description: "Paired workload Client proxy", StartType: mgr.StartAutomatic, ServiceStartName: "LocalSystem", BinaryPathName: standaloneClientCommand()}
	var err error
	if s.service == nil {
		s.service, err = s.manager.CreateService("MobileEgressClient", filepath.Join(standaloneClientInstallRoot, ClientExecutableName), config, "serve", "--standalone", "--state-dir", clientStateDirectory)
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
