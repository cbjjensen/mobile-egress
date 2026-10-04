//go:build windows

package clientapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const localPipe = `\\.\pipe\MobileEgressClient`
const localPipeClientAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE

func ListenLocal(stateDir string) (net.Listener, error) {
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !token.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return nil, errors.New("Client service must run as LocalSystem")
	}
	owner, err := os.ReadFile(filepath.Join(stateDir, "owner.sid"))
	if err != nil {
		return nil, errors.New("Client owner is missing; repair the installation")
	}
	sid, err := windows.StringToSid(strings.TrimSpace(string(owner)))
	if err != nil {
		return nil, errors.New("Client owner is invalid; repair the installation")
	}
	// FILE_GENERIC_WRITE includes FILE_CREATE_PIPE_INSTANCE. Grant only data
	// read/write, read attributes, READ_CONTROL and SYNCHRONIZE. CreateFile also
	// checks read attributes when opening the client handle. The owner still
	// cannot create an impersonating server instance in this pipe namespace.
	descriptor := fmt.Sprintf("O:SYG:SYD:P(A;;GA;;;SY)(A;;0x%08x;;;%s)", localPipeClientAccess, sid.String())
	return winio.ListenPipe(localPipe, &winio.PipeConfig{SecurityDescriptor: descriptor, InputBufferSize: 65536, OutputBufferSize: 65536})
}

func dialLocal(ctx context.Context) (net.Conn, error) {
	return dialAuthenticatedPipe(ctx, localPipe)
}

func dialAuthenticatedPipe(ctx context.Context, path string) (net.Conn, error) {
	// READ_CONTROL lets the GUI verify the server's object owner before sending
	// an invitation. An unprivileged process cannot create a SYSTEM-owned pipe.
	connection, err := winio.DialPipeAccess(ctx, path, localPipeClientAccess)
	if err != nil {
		return nil, err
	}
	handleConnection, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		connection.Close()
		return nil, errors.New("cannot authenticate Client service")
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(handleConnection.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		connection.Close()
		return nil, errors.New("cannot authenticate Client service")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || !owner.IsWellKnown(windows.WinLocalSystemSid) {
		connection.Close()
		return nil, errors.New("Client service identity is invalid")
	}
	return connection, nil
}
