package nodeservice

import (
	"context"
	"runtime"
	"testing"
)

// Copying and manual entry must agree, including after restoring a later slot.
// Mac HTTP compatibility must not change the SOCKS endpoint or credentials.
func TestPhoneProxyCopyMatchesDisplayedAddresses(t *testing.T) {
	httpHost, socksHost := "127.0.0.2", "127.0.0.2"
	if runtime.GOOS == "darwin" {
		httpHost, socksHost = "localtest.me", "127.0.0.1"
	}
	for _, tc := range []struct {
		name, httpPort, socksPort string
		slot                      int
	}{
		{"first", "1081", "1080", 0},
		{"second", "1083", "1082", 1},
		{"last", "1099", "1098", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m, _, _ := directTestConfigured(t)
			next := m.cloneLocked()
			next.Phones[0].Slot = tc.slot
			next.Phones[0].Username = "fixture-user"
			next.Phones[0].Password = "fixture-password"
			if err := m.saveLocked(ctx, next); err != nil {
				t.Fatal(err)
			}
			m.refreshLocked()
			view, err := m.Phones(ctx)
			if err != nil || len(view.Phones) != 1 {
				t.Fatalf("phone status: %v %v", view, err)
			}
			wantHTTP, wantSOCKS := httpHost+":"+tc.httpPort, socksHost+":"+tc.socksPort
			phone, single := view.Phones[0], m.Status()
			if phone.HTTPAddress != wantHTTP || single.HTTPAddress != wantHTTP || phone.SOCKSAddress != wantSOCKS || single.SOCKSAddress != wantSOCKS {
				t.Fatalf("displayed addresses: phone HTTP=%s SOCKS=%s, single HTTP=%s SOCKS=%s; want HTTP=%s SOCKS=%s", phone.HTTPAddress, phone.SOCKSAddress, single.HTTPAddress, single.SOCKSAddress, wantHTTP, wantSOCKS)
			}
			for kind, want := range map[string]string{
				"http":  wantHTTP + ":fixture-user:fixture-password",
				"socks": "socks5://fixture-user:fixture-password@" + wantSOCKS,
			} {
				for name, copy := range map[string]func() (string, error){
					"single": func() (string, error) { return m.Proxy(ctx, kind) },
					"phone":  func() (string, error) { return m.PhoneProxy(ctx, phone.ID, kind) },
				} {
					if got, err := copy(); err != nil || got != want {
						t.Fatalf("%s %s proxy = %q, %v; want %q", name, kind, got, err, want)
					}
				}
			}
		})
	}
}
