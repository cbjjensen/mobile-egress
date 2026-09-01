package relayadmin

import (
	"path"
	"strings"
	"testing"
)

func TestDarwinAdminSocketPathContract(t *testing.T) {
	const want = "/var/run/com.cbjjensen.mobile-egress.relay.sock"
	if DarwinAdminSocketPath != want {
		t.Fatalf("DarwinAdminSocketPath = %q, want %q", DarwinAdminSocketPath, want)
	}
	if !path.IsAbs(DarwinAdminSocketPath) {
		t.Fatal("DarwinAdminSocketPath must be absolute")
	}
	if path.Clean(DarwinAdminSocketPath) != DarwinAdminSocketPath {
		t.Fatal("DarwinAdminSocketPath must be canonical")
	}
	if strings.ContainsRune(DarwinAdminSocketPath, '\x00') {
		t.Fatal("DarwinAdminSocketPath must not contain NUL")
	}
	if strings.HasSuffix(DarwinAdminSocketPath, "/") {
		t.Fatal("DarwinAdminSocketPath must not have a trailing slash")
	}
	if len(DarwinAdminSocketPath) > 103 {
		t.Fatalf("DarwinAdminSocketPath is %d bytes; Darwin sun_path permits at most 103", len(DarwinAdminSocketPath))
	}
}
