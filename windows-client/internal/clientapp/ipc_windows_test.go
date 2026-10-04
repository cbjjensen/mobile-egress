//go:build windows

package clientapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func TestPipeOwnerExchangesBytesWithoutCreatingPipeInstances(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		t.Skip("requires an interactive user account without SYSTEM's full access")
	}
	path := fmt.Sprintf(`\\.\pipe\MobileEgressClient-owner-access-%d-%d`, os.Getpid(), time.Now().UnixNano())
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// A native first instance avoids granting this fixture's user the pipe
	// creation right that go-winio needs when accepting subsequent instances.
	// Production creates those instances as SYSTEM, which retains full access.
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;;GA;;;SY)(A;;0x%08x;;;%s)", localPipeClientAccess, user.User.Sid.String()))
	if err != nil {
		t.Fatal(err)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	handle, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_REJECT_REMOTE_CLIENTS, 2, 4096, 4096, 0, &sa)
	if err != nil {
		t.Fatal(err)
	}
	server := os.NewFile(uintptr(handle), path)
	defer server.Close()
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Request only otherwise-permitted access to a second server instance.
	// Windows must additionally require FILE_CREATE_PIPE_INSTANCE (0x4).
	ntName, err := windows.NewNTUnicodeString(`\??\` + path[4:])
	if err != nil {
		t.Fatal(err)
	}
	oa := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), ObjectName: ntName, Attributes: windows.OBJ_CASE_INSENSITIVE}
	var forbidden windows.Handle
	var status windows.IO_STATUS_BLOCK
	timeout := int64(-500000)
	err = windows.NtCreateNamedPipeFile(&forbidden, localPipeClientAccess, &oa, &status, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN, 0, windows.FILE_PIPE_REJECT_REMOTE_CLIENTS, 0, 0, 2, 4096, 4096, &timeout)
	if err == nil {
		windows.CloseHandle(forbidden)
		t.Fatal("owner was granted the pipe-instance creation right")
	}
	if !errors.Is(err, windows.STATUS_ACCESS_DENIED) {
		t.Fatalf("pipe-instance creation right denied for unexpected reason: %v", err)
	}
	client, err := winio.DialPipeAccess(ctx, path, localPipeClientAccess)
	if err != nil {
		t.Fatalf("owner cannot open the Client pipe with production rights: %v", err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	request, response := []byte("{\"method\":\"status\"}\n"), []byte("{\"status\":{\"phase\":\"waiting\"}}\n")
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	received := make([]byte, len(request))
	if _, err := io.ReadFull(server, received); err != nil || string(received) != string(request) {
		t.Fatalf("server did not receive status request: %q, %v", received, err)
	}
	if _, err := server.Write(response); err != nil {
		t.Fatal(err)
	}
	received = make([]byte, len(response))
	if _, err := io.ReadFull(client, received); err != nil || string(received) != string(response) {
		t.Fatalf("owner did not receive status response: %q, %v", received, err)
	}
}

func TestGUIRejectsAnUnprivilegedNamedPipeServer(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		t.Skip("requires an interactive user account")
	}
	path := fmt.Sprintf(`\\.\pipe\MobileEgressClient-test-%d`, time.Now().UnixNano())
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			<-accepted
		}
	}()
	defer close(accepted)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := dialAuthenticatedPipe(ctx, path)
	if err == nil {
		connection.Close()
		t.Fatal("GUI trusted a pipe owned by an ordinary user")
	}
}
