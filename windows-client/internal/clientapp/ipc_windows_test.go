//go:build windows

package clientapp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

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
