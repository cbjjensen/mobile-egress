package proxyendpoint

import (
	"runtime"
	"testing"
)

func TestNativeLoopbackEndpoint(t *testing.T) {
	want := "127.0.0.2:1080"
	if runtime.GOOS == "darwin" {
		want = "127.0.0.1:1080"
	}
	if SOCKSAddress() != want {
		t.Fatalf("proxy endpoint = %s, want %s", SOCKSAddress(), want)
	}
}
