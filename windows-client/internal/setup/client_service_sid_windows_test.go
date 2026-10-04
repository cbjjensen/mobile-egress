//go:build windows

package setup

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestClientServiceEnablesFirewallSIDBeforeFreshOrRepairStart(t *testing.T) {
	for _, repair := range []bool{false, true} {
		name := "fresh"
		if repair {
			name = "repair existing NONE SID"
		}
		t.Run(name, func(t *testing.T) {
			service := &fixtureClientService{}
			manager := &fixtureClientServiceManager{service: service}
			transaction := &clientServiceInstall{manager: manager, stateDirectory: clientStateDirectory}
			if repair {
				transaction.service = service
				transaction.stateDirectory = legacyClientStateDirectory
				transaction.previous = mgr.Config{SidType: windows.SERVICE_SID_TYPE_NONE, ServiceType: windows.SERVICE_WIN32_OWN_PROCESS, ServiceStartName: "LocalSystem", BinaryPathName: standaloneClientCommandForState(legacyClientStateDirectory)}
				service.config = transaction.previous
			}
			if err := transaction.installRegistration(); err != nil {
				t.Fatal(err)
			}
			if len(service.started) != 1 {
				t.Fatalf("service start attempts = %d", len(service.started))
			}
			atStart := service.started[0]
			if atStart.SidType != windows.SERVICE_SID_TYPE_UNRESTRICTED {
				t.Fatalf("service started with SID type %d; service-scoped firewall cannot match NONE", atStart.SidType)
			}
			if atStart.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS {
				t.Fatalf("service registration type = %d; repair must pass an explicit own-process type", atStart.ServiceType)
			}
			if atStart.ServiceStartName != "LocalSystem" || atStart.BinaryPathName != standaloneClientCommandForState(transaction.stateDirectory) {
				t.Fatalf("service SID setup changed protected account/state: %+v", atStart)
			}
			if !repair && (manager.name != "MobileEgressClient" || manager.path != filepath.Join(standaloneClientInstallRoot, ClientExecutableName) || !reflect.DeepEqual(manager.args, []string{"serve", "--standalone", "--state-dir", clientStateDirectory})) {
				t.Fatalf("unexpected service creation: %+v", manager)
			}
			if repair && manager.name != "" {
				t.Fatal("repair recreated the existing service")
			}
		})
	}
}

func TestClientServiceFailedRepairRestoresOriginalSIDAndServiceType(t *testing.T) {
	for _, sidType := range []uint32{windows.SERVICE_SID_TYPE_NONE, windows.SERVICE_SID_TYPE_UNRESTRICTED, windows.SERVICE_SID_TYPE_RESTRICTED} {
		previous := mgr.Config{SidType: sidType, ServiceType: windows.SERVICE_WIN32_SHARE_PROCESS, ServiceStartName: "LocalSystem", BinaryPathName: standaloneClientCommandForState(legacyClientStateDirectory), DisplayName: "previous Client", StartType: mgr.StartManual, Description: "previous description", Dependencies: []string{"Tcpip"}, DelayedAutoStart: true}
		service := &fixtureClientService{config: previous, failFirstStart: true}
		manager := &fixtureClientServiceManager{service: service}
		transaction := &clientServiceInstall{manager: manager, service: service, previous: previous, wasRunning: true, stopped: true, stateDirectory: legacyClientStateDirectory}
		if err := transaction.installRegistration(); err == nil {
			t.Fatal("injected startup failure was ignored")
		}
		if len(service.started) != 1 || service.started[0].SidType != windows.SERVICE_SID_TYPE_UNRESTRICTED || service.started[0].ServiceType != windows.SERVICE_WIN32_OWN_PROCESS {
			t.Fatalf("repair did not establish a firewall-capable own-process service before start: %+v", service.started)
		}
		if err := transaction.close(true); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(service.config, previous) || !reflect.DeepEqual(transaction.previous, previous) {
			t.Fatalf("rollback lost original service configuration: got %+v, snapshot %+v, want %+v", service.config, transaction.previous, previous)
		}
		if len(service.started) != 2 || !reflect.DeepEqual(service.started[1], previous) {
			t.Fatal("rollback restarted before restoring the complete prior configuration")
		}
		if !manager.disconnected || !service.closed || service.deleted {
			t.Fatal("rollback did not close native handles or deleted existing registration")
		}
	}
}

type fixtureClientServiceManager struct {
	service      *fixtureClientService
	name, path   string
	args         []string
	disconnected bool
}

func (m *fixtureClientServiceManager) CreateService(name, path string, config mgr.Config, args ...string) (clientWindowsService, error) {
	m.name, m.path, m.args = name, path, args
	m.service.config = config
	return m.service, nil
}
func (m *fixtureClientServiceManager) Disconnect() error { m.disconnected = true; return nil }

type fixtureClientService struct {
	config                          mgr.Config
	started                         []mgr.Config
	failFirstStart, deleted, closed bool
}

func (s *fixtureClientService) Query() (svc.Status, error) {
	return svc.Status{State: svc.Stopped}, nil
}
func (s *fixtureClientService) Control(svc.Cmd) (svc.Status, error) {
	return svc.Status{State: svc.Stopped}, nil
}
func (s *fixtureClientService) UpdateConfig(config mgr.Config) error                  { s.config = config; return nil }
func (s *fixtureClientService) SetRecoveryActions([]mgr.RecoveryAction, uint32) error { return nil }
func (s *fixtureClientService) Start(...string) error {
	s.started = append(s.started, s.config)
	if s.failFirstStart && len(s.started) == 1 {
		return errors.New("injected start failure")
	}
	return nil
}
func (s *fixtureClientService) Delete() error { s.deleted = true; return nil }
func (s *fixtureClientService) Close() error  { s.closed = true; return nil }
