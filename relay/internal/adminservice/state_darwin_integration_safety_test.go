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
