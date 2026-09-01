package adminservice

import (
	"reflect"
	"testing"
)

func TestDarwinIntegrationPathRefusalRejectsExactDescendantCaseAndCanonicalAliases(t *testing.T) {
	product := "/Library/Application Support/ZFNF Mobile Egress"
	for _, test := range []struct {
		name      string
		spelled   string
		canonical string
	}{
		{name: "exact", spelled: product, canonical: product},
		{name: "descendant", spelled: product + "/Relay/test", canonical: product + "/Relay/test"},
		{name: "case variant", spelled: "/library/application support/zfnf mobile egress", canonical: "/library/application support/zfnf mobile egress"},
		{name: "resolved symlink alias", spelled: "/private/tmp/zfnf-state-alias", canonical: product + "/Relay"},
		{name: "socket", spelled: "/var/run/com.cbjjensen.mobile-egress.relay.sock", canonical: "/private/var/run/com.cbjjensen.mobile-egress.relay.sock"},
		{name: "lock", spelled: "/private/var/run/com.cbjjensen.mobile-egress.relay.lock", canonical: "/private/var/run/com.cbjjensen.mobile-egress.relay.lock"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !darwinIntegrationPathRefused(test.spelled, test.canonical) {
				t.Fatalf("path refusal accepted spelled %q canonical %q", test.spelled, test.canonical)
			}
		})
	}
	if darwinIntegrationPathRefused("/private/tmp/mobile-egress-tests", "/private/tmp/mobile-egress-tests") {
		t.Fatal("path refusal rejected a safe temporary root")
	}
}

func TestDarwinIntegrationRootAdmissionCanonicalizesExistingBaseBeforeCreation(t *testing.T) {
	var events []string
	factory := darwinTestRootFactory{
		canonicalize: func(candidate string) (string, error) {
			events = append(events, "canonical:"+candidate)
			return candidate, nil
		},
		create: func() (string, error) {
			events = append(events, "create")
			return "/private/tmp/mobile-egress-tests/fixture", nil
		},
	}
	root, err := factory.Create("/private/tmp/mobile-egress-tests")
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"canonical:/private/tmp/mobile-egress-tests",
		"create",
		"canonical:/private/tmp/mobile-egress-tests/fixture",
		"canonical:/private/tmp/mobile-egress-tests",
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("admission events = %#v, want %#v", events, wantEvents)
	}
	if !root.Contains(
		"/private/tmp/mobile-egress-tests/fixture/Relay/state.db",
		"/private/tmp/mobile-egress-tests/fixture/Relay/state.db",
	) {
		t.Fatal("admitted root rejected its safe descendant")
	}
	for _, candidate := range [][2]string{
		{"/private/tmp/mobile-egress-tests/sibling", "/private/tmp/mobile-egress-tests/sibling"},
		{"/private/tmp/mobile-egress-tests/fixture/link", "/Library/Application Support/ZFNF Mobile Egress/Relay"},
	} {
		if root.Contains(candidate[0], candidate[1]) {
			t.Fatalf("admitted root accepted outside/aliased mutation path %#v", candidate)
		}
	}
}

func TestDarwinAdmittedMutationNeverInvokesCallbackForProtectedOrEscapedPaths(t *testing.T) {
	root := darwinTestRoot{
		spelled:   "/private/tmp/mobile-egress-tests/fixture",
		canonical: "/private/tmp/mobile-egress-tests/fixture",
	}
	mutations := 0
	mutate := func() error {
		mutations++
		return nil
	}
	for _, test := range []struct {
		name      string
		spelled   string
		canonical string
	}{
		{name: "outside sibling", spelled: "/private/tmp/mobile-egress-tests/sibling", canonical: "/private/tmp/mobile-egress-tests/sibling"},
		{name: "canonical escape", spelled: root.spelled + "/alias", canonical: "/Library/Application Support/ZFNF Mobile Egress/Relay"},
		{name: "product exact", spelled: "/Library/Application Support/ZFNF Mobile Egress", canonical: "/Library/Application Support/ZFNF Mobile Egress"},
		{name: "product descendant", spelled: "/Library/Application Support/ZFNF Mobile Egress/Relay/test", canonical: "/Library/Application Support/ZFNF Mobile Egress/Relay/test"},
		{name: "product case variant", spelled: "/library/application support/zfnf mobile egress/relay", canonical: "/library/application support/zfnf mobile egress/relay"},
		{name: "socket exact", spelled: "/var/run/com.cbjjensen.mobile-egress.relay.sock", canonical: "/private/var/run/com.cbjjensen.mobile-egress.relay.sock"},
		{name: "socket descendant", spelled: "/private/var/run/com.cbjjensen.mobile-egress.relay.sock/child", canonical: "/private/var/run/com.cbjjensen.mobile-egress.relay.sock/child"},
		{name: "lock exact", spelled: "/var/run/com.cbjjensen.mobile-egress.relay.lock", canonical: "/private/var/run/com.cbjjensen.mobile-egress.relay.lock"},
		{name: "lock descendant", spelled: "/private/var/run/com.cbjjensen.mobile-egress.relay.lock/child", canonical: "/private/var/run/com.cbjjensen.mobile-egress.relay.lock/child"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := runDarwinAdmittedMutation(root, test.spelled, test.canonical, mutate); err == nil {
				t.Fatalf("mutation admission accepted %q -> %q", test.spelled, test.canonical)
			}
		})
	}
	if mutations != 0 {
		t.Fatalf("refused mutation callback ran %d times, want zero", mutations)
	}

	safe := root.spelled + "/Relay/admin.sock"
	if err := runDarwinAdmittedMutation(root, safe, safe, mutate); err != nil {
		t.Fatalf("safe mutation rejected: %v", err)
	}
	if mutations != 1 {
		t.Fatalf("safe mutation callback ran total %d times, want one", mutations)
	}
}
