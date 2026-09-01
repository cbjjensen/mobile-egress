package tailscale

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const expectedTailscaleAppRequirement = `=(anchor apple generic and identifier "io.tailscale.ipn.macsys" and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = "W5364U7YZB") or (anchor apple generic and identifier "io.tailscale.ipn.macos" and certificate leaf[field.1.2.840.113635.100.6.1.9] exists and certificate leaf[subject.OU] = "W5364U7YZB")`

func TestParseCodeSignIdentityAcceptsExactFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		output string
		want   codeIdentity
	}{
		{
			name: "standalone",
			output: "Executable=/Applications/Tailscale.app/Contents/MacOS/Tailscale\n" +
				"Identifier=io.tailscale.ipn.macsys\nFormat=app bundle with Mach-O thin (arm64)\n" +
				"TeamIdentifier=W5364U7YZB\nRuntime Version=13.0.0\n",
			want: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"},
		},
		{
			name: "app-store-crlf-no-final-newline",
			output: "Identifier=io.tailscale.ipn.macos\r\n" +
				"Authority=Apple Mac OS Application Signing\r\nTeamIdentifier=W5364U7YZB",
			want: codeIdentity{BundleID: "io.tailscale.ipn.macos", TeamID: "W5364U7YZB"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCodeSignIdentity([]byte(test.output))
			if err != nil {
				t.Fatalf("parseCodeSignIdentity() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("parseCodeSignIdentity() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseCodeSignIdentityRejectsAmbiguityAndLookalikes(t *testing.T) {
	t.Parallel()
	valid := "Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n"
	cases := map[string][]byte{
		"missing identifier":                     []byte("TeamIdentifier=W5364U7YZB\n"),
		"missing team":                           []byte("Identifier=io.tailscale.ipn.macsys\n"),
		"duplicate identifier":                   []byte("Identifier=io.tailscale.ipn.macsys\nIdentifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n"),
		"duplicate team":                         []byte("Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\nTeamIdentifier=W5364U7YZB\n"),
		"identifier key wrong case":              []byte("identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n"),
		"team key wrong case":                    []byte("Identifier=io.tailscale.ipn.macsys\nteamIdentifier=W5364U7YZB\n"),
		"identifier leading space":               []byte(" Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n"),
		"team trailing space":                    []byte("Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB \n"),
		"embedded identifier":                    []byte("note Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n"),
		"embedded team":                          []byte("Identifier=io.tailscale.ipn.macsys\nnote TeamIdentifier=W5364U7YZB\n"),
		"team not set":                           []byte("Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=not set\n"),
		"empty identifier":                       []byte("Identifier=\nTeamIdentifier=W5364U7YZB\n"),
		"empty team":                             []byte("Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=\n"),
		"nul":                                    append([]byte(valid), 0),
		"bare carriage return":                   []byte("Identifier=io.tailscale.ipn.macsys\rTeamIdentifier=W5364U7YZB\n"),
		"oversized":                              []byte("Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n" + strings.Repeat("x", maximumIdentityTrustOutput)),
		"malformed utf8":                         append([]byte(valid), 0xff),
		"lowercase identifier beside valid pair": []byte(valid + "identifier=io.tailscale.ipn.macsys\n"),
		"lowercase team beside valid pair":       []byte(valid + "teamidentifier=W5364U7YZB\n"),
		"near identifier key beside valid pair":  []byte(valid + "IdentifierExtra=io.tailscale.ipn.macsys\n"),
		"near team key beside valid pair":        []byte(valid + "TeamIdentifierExtra=W5364U7YZB\n"),
		"prefixed identifier beside valid pair":  []byte(valid + "XIdentifier=io.tailscale.ipn.macsys\n"),
		"prefixed team beside valid pair":        []byte(valid + "XTeamIdentifier=W5364U7YZB\n"),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := parseCodeSignIdentity(input); err == nil {
				t.Fatalf("parseCodeSignIdentity() = %#v, want error", got)
			}
		})
	}
}

type identityTestGuard struct {
	mu               sync.Mutex
	bundle           string
	executable       string
	revalidateErr    error
	revalidations    int
	closeCalls       int
	underlying       int
	closeErr         error
	closed           bool
	live             int
	closeStarted     chan struct{}
	allowClose       chan struct{}
	onExecutablePath func()
}

func newIdentityTestGuard() *identityTestGuard {
	return &identityTestGuard{
		bundle:     "/Applications/Tailscale.app",
		executable: "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
		live:       2,
	}
}

func (guard *identityTestGuard) Revalidate(context.Context) error {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	guard.revalidations++
	if guard.closed {
		return errors.New("use after close")
	}
	return guard.revalidateErr
}

func (guard *identityTestGuard) BundlePath() string {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.closed {
		return ""
	}
	return guard.bundle
}

func (guard *identityTestGuard) ExecutablePath() string {
	guard.mu.Lock()
	hook := guard.onExecutablePath
	if guard.closed {
		guard.mu.Unlock()
		return ""
	}
	executable := guard.executable
	guard.mu.Unlock()
	if hook != nil {
		hook()
	}
	return executable
}

func (guard *identityTestGuard) Close() error {
	guard.mu.Lock()
	guard.closeCalls++
	if guard.closed {
		err := guard.closeErr
		guard.mu.Unlock()
		return err
	}
	guard.closed = true
	guard.underlying++
	started := guard.closeStarted
	allow := guard.allowClose
	guard.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if allow != nil {
		<-allow
	}
	guard.mu.Lock()
	guard.live = 0
	err := guard.closeErr
	guard.mu.Unlock()
	return err
}

func TestFindDarwinInstallationUsesOneFixedAssessmentForBothVariants(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		bundleID string
		variant  DarwinVariant
	}{
		{name: "standalone", bundleID: "io.tailscale.ipn.macsys", variant: DarwinStandalone},
		{name: "app-store", bundleID: "io.tailscale.ipn.macos", variant: DarwinAppStore},
	} {
		t.Run(test.name, func(t *testing.T) {
			guard := newIdentityTestGuard()
			calls := 0
			verifier := func(_ context.Context, bundle, executable, requirement string) (verifiedDarwinApp, error) {
				calls++
				if bundle != "/Applications/Tailscale.app" {
					t.Fatalf("bundle = %q", bundle)
				}
				if executable != "/Applications/Tailscale.app/Contents/MacOS/Tailscale" {
					t.Fatalf("executable = %q", executable)
				}
				if requirement != expectedTailscaleAppRequirement {
					t.Fatalf("requirement = %q", requirement)
				}
				return verifiedDarwinApp{
					Identity: codeIdentity{BundleID: test.bundleID, TeamID: "W5364U7YZB"},
					Guard:    guard,
				}, nil
			}
			got, err := findDarwinInstallation(context.Background(), verifier)
			if err != nil {
				t.Fatalf("findDarwinInstallation() error = %v", err)
			}
			if calls != 1 {
				t.Fatalf("verifier calls = %d, want 1", calls)
			}
			if got.BundlePath != "/Applications/Tailscale.app" ||
				got.Executable != "/Applications/Tailscale.app/Contents/MacOS/Tailscale" ||
				got.BundleID != test.bundleID || got.Variant != test.variant || got.guard != guard {
				t.Fatalf("installation = %#v", got)
			}
			guard.mu.Lock()
			if guard.live != 2 || guard.closeCalls != 0 {
				t.Fatalf("accepted guard live=%d closeCalls=%d, want 2/0", guard.live, guard.closeCalls)
			}
			guard.mu.Unlock()
			if err := got.guard.Close(); err != nil {
				t.Fatalf("accepted guard Close() error = %v", err)
			}
		})
	}
}

func TestFindDarwinInstallationRejectsEveryOtherIdentityAndClosesGuard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		identity    codeIdentity
		mutate      func(*identityTestGuard)
		nilGuard    bool
		verifierErr error
	}{
		{name: "bundle prefix", identity: codeIdentity{BundleID: "io.tailscale.ipn.mac", TeamID: "W5364U7YZB"}},
		{name: "bundle suffix", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys.extra", TeamID: "W5364U7YZB"}},
		{name: "bundle case", identity: codeIdentity{BundleID: "io.tailscale.ipn.Macsys", TeamID: "W5364U7YZB"}},
		{name: "other bundle", identity: codeIdentity{BundleID: "com.example.tailscale", TeamID: "W5364U7YZB"}},
		{name: "team prefix", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZ"}},
		{name: "team suffix", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZBX"}},
		{name: "team case", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "w5364u7yzb"}},
		{name: "empty identity", identity: codeIdentity{}},
		{name: "wrong bundle path", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"}, mutate: func(g *identityTestGuard) { g.bundle = "/tmp/Tailscale.app" }},
		{name: "wrong executable path", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"}, mutate: func(g *identityTestGuard) { g.executable += ".old" }},
		{name: "nil guard", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"}, nilGuard: true},
		{name: "verifier error with guard", identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"}, verifierErr: errors.New("hostile /tmp/native detail")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			guard := newIdentityTestGuard()
			if test.mutate != nil {
				test.mutate(guard)
			}
			var returned appExecutionGuard = guard
			if test.nilGuard {
				returned = nil
			}
			_, err := findDarwinInstallation(context.Background(), func(context.Context, string, string, string) (verifiedDarwinApp, error) {
				return verifiedDarwinApp{Identity: test.identity, Guard: returned}, test.verifierErr
			})
			if !errors.Is(err, errTailscaleAppVerification) {
				t.Fatalf("error = %v, want fixed verification error", err)
			}
			if strings.Contains(fmt.Sprint(err), "/tmp/") || strings.Contains(fmt.Sprint(err), "native detail") {
				t.Fatalf("error leaked raw diagnostic: %v", err)
			}
			guard.mu.Lock()
			defer guard.mu.Unlock()
			if test.nilGuard {
				if guard.closeCalls != 0 || guard.live != 2 {
					t.Fatalf("unreturned guard touched: close=%d live=%d", guard.closeCalls, guard.live)
				}
			} else if guard.closeCalls != 1 || guard.underlying != 1 || guard.live != 0 {
				t.Fatalf("rejected guard close=%d underlying=%d live=%d, want 1/1/0", guard.closeCalls, guard.underlying, guard.live)
			}
		})
	}
}

func TestFindDarwinInstallationCloseUncertaintyOverridesRejection(t *testing.T) {
	t.Parallel()
	guard := newIdentityTestGuard()
	guard.closeErr = errors.New("descriptor 7: raw close detail")
	_, err := findDarwinInstallation(context.Background(), func(context.Context, string, string, string) (verifiedDarwinApp, error) {
		return verifiedDarwinApp{
			Identity: codeIdentity{BundleID: "com.example", TeamID: "W5364U7YZB"},
			Guard:    guard,
		}, nil
	})
	if !errors.Is(err, errTailscaleAppCleanup) || fmt.Sprint(err) != "Tailscale application verification cleanup failed" {
		t.Fatalf("error = %v, want fixed cleanup error", err)
	}
}

func TestFindDarwinInstallationPreservesVerifierCleanupUncertainty(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		withGuard  bool
		cancel     bool
		wrappedErr bool
	}{
		{name: "nil guard"},
		{name: "joined guard close", withGuard: true},
		{name: "nil guard after cancellation", cancel: true},
		{name: "joined guard close after cancellation", withGuard: true, cancel: true},
		{name: "wrapped cleanup sentinel", withGuard: true, wrappedErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			guard := newIdentityTestGuard()
			var returned appExecutionGuard
			if test.withGuard {
				returned = guard
			}
			verifierErr := error(errTailscaleAppCleanup)
			if test.wrappedErr {
				verifierErr = fmt.Errorf("untrusted wrapper: %w", errTailscaleAppCleanup)
			}
			installation, err := findDarwinInstallation(ctx, func(context.Context, string, string, string) (verifiedDarwinApp, error) {
				if test.cancel {
					cancel()
				}
				return verifiedDarwinApp{Guard: returned}, verifierErr
			})
			if installation != (DarwinInstallation{}) || !errors.Is(err, errTailscaleAppCleanup) ||
				fmt.Sprint(err) != "Tailscale application verification cleanup failed" {
				t.Fatalf("installation=%#v error=%v, want only fixed cleanup uncertainty", installation, err)
			}
			guard.mu.Lock()
			defer guard.mu.Unlock()
			if test.withGuard {
				if guard.closeCalls != 1 || guard.underlying != 1 || guard.live != 0 {
					t.Fatalf("guard close=%d underlying=%d live=%d, want 1/1/0", guard.closeCalls, guard.underlying, guard.live)
				}
			} else if guard.closeCalls != 0 || guard.live != 2 {
				t.Fatalf("unreturned guard close=%d live=%d, want 0/2", guard.closeCalls, guard.live)
			}
		})
	}
}

func TestFindDarwinInstallationCancellationAfterVerifierClosesInsteadOfTransferring(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	guard := newIdentityTestGuard()
	installation, err := findDarwinInstallation(ctx, func(context.Context, string, string, string) (verifiedDarwinApp, error) {
		cancel()
		return verifiedDarwinApp{
			Identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"},
			Guard:    guard,
		}, nil
	})
	if !errors.Is(err, errTailscaleAppVerification) || installation.guard != nil {
		t.Fatalf("installation=%#v error=%v, want rejection", installation, err)
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.closeCalls != 1 || guard.underlying != 1 || guard.live != 0 {
		t.Fatalf("guard close=%d underlying=%d live=%d, want 1/1/0", guard.closeCalls, guard.underlying, guard.live)
	}
}

func TestFindDarwinInstallationCanceledContextDoesNotInvokeVerifier(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := findDarwinInstallation(ctx, func(context.Context, string, string, string) (verifiedDarwinApp, error) {
		calls++
		return verifiedDarwinApp{}, nil
	})
	if !errors.Is(err, errTailscaleAppVerification) || calls != 0 {
		t.Fatalf("error=%v verifier calls=%d, want fixed error/0", err, calls)
	}
}

func TestFindDarwinInstallationCancellationDuringClassificationClosesInsteadOfTransferring(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	guard := newIdentityTestGuard()
	guard.onExecutablePath = cancel
	installation, err := findDarwinInstallation(ctx, func(context.Context, string, string, string) (verifiedDarwinApp, error) {
		return verifiedDarwinApp{
			Identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"},
			Guard:    guard,
		}, nil
	})
	if !errors.Is(err, errTailscaleAppVerification) || installation.guard != nil {
		t.Fatalf("installation=%#v error=%v, want rejection", installation, err)
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.closeCalls != 1 || guard.underlying != 1 || guard.live != 0 {
		t.Fatalf("guard close=%d underlying=%d live=%d, want 1/1/0", guard.closeCalls, guard.underlying, guard.live)
	}
}

func TestFindDarwinInstallationRevalidatesAfterClassificationBeforeTransfer(t *testing.T) {
	t.Parallel()
	guard := newIdentityTestGuard()
	guard.onExecutablePath = func() {
		guard.mu.Lock()
		guard.revalidateErr = errors.New("persistent replacement during classification")
		guard.mu.Unlock()
	}
	installation, err := findDarwinInstallation(context.Background(), func(context.Context, string, string, string) (verifiedDarwinApp, error) {
		return verifiedDarwinApp{
			Identity: codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"},
			Guard:    guard,
		}, nil
	})
	if !errors.Is(err, errTailscaleAppVerification) || installation.guard != nil {
		t.Fatalf("installation=%#v error=%v, want rejection", installation, err)
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.revalidations != 1 || guard.closeCalls != 1 || guard.underlying != 1 || guard.live != 0 {
		t.Fatalf("revalidations=%d close=%d underlying=%d live=%d, want 1/1/1/0", guard.revalidations, guard.closeCalls, guard.underlying, guard.live)
	}
}

func TestTailscaleAppRequirementBranchesHaveIndependentApplePolicies(t *testing.T) {
	t.Parallel()
	standalone := requirementFixture{
		bundleID: "io.tailscale.ipn.macsys", teamID: "W5364U7YZB",
		appleGeneric: true, developerIDIntermediate: true, developerIDApplication: true,
	}
	appStore := requirementFixture{
		bundleID: "io.tailscale.ipn.macos", teamID: "W5364U7YZB",
		appleGeneric: true, appStoreApplication: true,
	}
	if !identityRequirementFixtureAllows(expectedTailscaleAppRequirement, standalone) {
		t.Fatal("exact requirement did not admit standalone fixture")
	}
	if !identityRequirementFixtureAllows(expectedTailscaleAppRequirement, appStore) {
		t.Fatal("exact requirement did not admit App Store fixture")
	}
	negativeFixtures := []requirementFixture{
		{bundleID: "io.tailscale.ipn.macsys", teamID: "W5364U7YZB"},
		{bundleID: "io.tailscale.ipn.macsys", teamID: "OTHERTEAM1", appleGeneric: true, developerIDIntermediate: true, developerIDApplication: true},
		{bundleID: "io.tailscale.ipn.macsys", teamID: "W5364U7YZB", appleGeneric: true, developerIDApplication: true},
		{bundleID: "io.tailscale.ipn.macsys", teamID: "W5364U7YZB", appleGeneric: true, developerIDIntermediate: true, appStoreApplication: true},
		{bundleID: "io.tailscale.ipn.macos", teamID: "W5364U7YZB", appleGeneric: true, developerIDApplication: true},
		{bundleID: "io.tailscale.ipn.macos", teamID: "OTHERTEAM1", appleGeneric: true, appStoreApplication: true},
	}
	for index, fixture := range negativeFixtures {
		if identityRequirementFixtureAllows(expectedTailscaleAppRequirement, fixture) {
			t.Fatalf("negative fixture %d was admitted: %#v", index, fixture)
		}
	}

	mutations := []struct {
		name           string
		requirement    string
		wantStandalone bool
		wantAppStore   bool
	}{
		{name: "unchanged", requirement: expectedTailscaleAppRequirement, wantStandalone: true, wantAppStore: true},
		{name: "standalone outer parenthesis", requirement: strings.Replace(expectedTailscaleAppRequirement, "=(anchor", "=anchor", 1), wantStandalone: false, wantAppStore: true},
		{name: "app store outer parenthesis", requirement: strings.Replace(expectedTailscaleAppRequirement, ") or (anchor", ") or anchor", 1), wantStandalone: true, wantAppStore: false},
		{name: "standalone anchor", requirement: strings.Replace(expectedTailscaleAppRequirement, "anchor apple generic and ", "", 1), wantStandalone: false, wantAppStore: true},
		{name: "app store anchor", requirement: replaceLast(expectedTailscaleAppRequirement, "anchor apple generic and ", ""), wantStandalone: true, wantAppStore: false},
		{name: "standalone identifier", requirement: strings.Replace(expectedTailscaleAppRequirement, `identifier "io.tailscale.ipn.macsys"`, `identifier "io.tailscale.ipn.changed"`, 1), wantStandalone: false, wantAppStore: true},
		{name: "app store identifier", requirement: strings.Replace(expectedTailscaleAppRequirement, `identifier "io.tailscale.ipn.macos"`, `identifier "io.tailscale.ipn.changed"`, 1), wantStandalone: true, wantAppStore: false},
		{name: "standalone team", requirement: strings.Replace(expectedTailscaleAppRequirement, `certificate leaf[subject.OU] = "W5364U7YZB"`, `certificate leaf[subject.OU] = "OTHERTEAM1"`, 1), wantStandalone: false, wantAppStore: true},
		{name: "app store team", requirement: replaceLast(expectedTailscaleAppRequirement, `certificate leaf[subject.OU] = "W5364U7YZB"`, `certificate leaf[subject.OU] = "OTHERTEAM1"`), wantStandalone: true, wantAppStore: false},
		{name: "developer intermediate", requirement: strings.Replace(expectedTailscaleAppRequirement, "1.2.840.113635.100.6.2.6", "1.2.840.113635.100.6.2.7", 1), wantStandalone: false, wantAppStore: true},
		{name: "developer application", requirement: strings.Replace(expectedTailscaleAppRequirement, "1.2.840.113635.100.6.1.13", "1.2.840.113635.100.6.1.9", 1), wantStandalone: false, wantAppStore: true},
		{name: "app store application", requirement: replaceLast(expectedTailscaleAppRequirement, "1.2.840.113635.100.6.1.9", "1.2.840.113635.100.6.1.13"), wantStandalone: true, wantAppStore: false},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			gotStandalone := identityRequirementFixtureAllows(mutation.requirement, standalone)
			gotAppStore := identityRequirementFixtureAllows(mutation.requirement, appStore)
			if gotStandalone != mutation.wantStandalone || gotAppStore != mutation.wantAppStore {
				t.Fatalf("variant tuple = (%t, %t), want (%t, %t)", gotStandalone, gotAppStore, mutation.wantStandalone, mutation.wantAppStore)
			}
		})
	}
}

type requirementFixture struct {
	bundleID                string
	teamID                  string
	appleGeneric            bool
	developerIDIntermediate bool
	developerIDApplication  bool
	appStoreApplication     bool
}

func identityRequirementFixtureAllows(requirement string, fixture requirementFixture) bool {
	if !strings.HasPrefix(requirement, "=") {
		return false
	}
	branches := strings.Split(strings.TrimPrefix(requirement, "="), " or ")
	if len(branches) != 2 {
		return false
	}
	for _, encodedBranch := range branches {
		if !strings.HasPrefix(encodedBranch, "(") || !strings.HasSuffix(encodedBranch, ")") {
			continue
		}
		branch := strings.TrimSuffix(strings.TrimPrefix(encodedBranch, "("), ")")
		if !fixture.appleGeneric || !strings.Contains(branch, "anchor apple generic") ||
			!strings.Contains(branch, `identifier "`+fixture.bundleID+`"`) ||
			!strings.Contains(branch, `certificate leaf[subject.OU] = "`+fixture.teamID+`"`) {
			continue
		}
		if fixture.bundleID == "io.tailscale.ipn.macsys" && fixture.developerIDIntermediate && fixture.developerIDApplication &&
			!fixture.appStoreApplication && strings.Contains(branch, "certificate 1[field.1.2.840.113635.100.6.2.6] exists") &&
			strings.Contains(branch, "certificate leaf[field.1.2.840.113635.100.6.1.13] exists") {
			return true
		}
		if fixture.bundleID == "io.tailscale.ipn.macos" && fixture.appStoreApplication &&
			!fixture.developerIDIntermediate && !fixture.developerIDApplication &&
			strings.Contains(branch, "certificate leaf[field.1.2.840.113635.100.6.1.9] exists") {
			return true
		}
	}
	return false
}

func replaceLast(value, old, replacement string) string {
	index := strings.LastIndex(value, old)
	if index < 0 {
		return value
	}
	return value[:index] + replacement + value[index+len(old):]
}

func baselineIdentityObservation() identityAppObservation {
	bundleDigest := sha256.Sum256([]byte("bundle metadata sentinel"))
	executableDigest := sha256.Sum256([]byte("executable bytes"))
	return identityAppObservation{
		Bundle: identityPathObservation{
			Path: "/Applications/Tailscale.app", Present: true, ExactCase: true, SymlinkFree: true,
			Kind: identityPathDirectory, Device: 2, Inode: 10, Generation: 3, UID: 501, GID: 20,
			Mode: 0o40755, LinkCount: 4, DeviceType: 0, Size: 128, Flags: 1,
			BirthTime:  identityTimestamp{Seconds: 100, Nanoseconds: 1},
			ChangeTime: identityTimestamp{Seconds: 101, Nanoseconds: 2},
			ModifyTime: identityTimestamp{Seconds: 102, Nanoseconds: 3}, Digest: bundleDigest,
		},
		Executable: identityPathObservation{
			Path: "/Applications/Tailscale.app/Contents/MacOS/Tailscale", Present: true, ExactCase: true, SymlinkFree: true,
			Kind: identityPathRegular, Executable: true, Device: 2, Inode: 11, Generation: 4, UID: 501, GID: 20,
			Mode: 0o100755, LinkCount: 1, DeviceType: 0, Size: 16, Flags: 2,
			BirthTime:  identityTimestamp{Seconds: 103, Nanoseconds: 4},
			ChangeTime: identityTimestamp{Seconds: 104, Nanoseconds: 5},
			ModifyTime: identityTimestamp{Seconds: 105, Nanoseconds: 6}, Digest: executableDigest,
		},
	}
}

func TestValidateIdentityAppObservationRejectsEveryPathAndMetadataMutation(t *testing.T) {
	t.Parallel()
	baseline := baselineIdentityObservation()
	if err := validateIdentityAppObservation(baseline, baseline); err != nil {
		t.Fatalf("baseline rejected: %v", err)
	}
	mutations := []struct {
		name   string
		mutate func(*identityAppObservation)
	}{
		{name: "missing app", mutate: func(o *identityAppObservation) { o.Bundle.Present = false }},
		{name: "missing CLI", mutate: func(o *identityAppObservation) { o.Executable.Present = false }},
		{name: "app replaced by file", mutate: func(o *identityAppObservation) { o.Bundle.Kind = identityPathRegular }},
		{name: "CLI replaced by directory", mutate: func(o *identityAppObservation) { o.Executable.Kind = identityPathDirectory }},
		{name: "app symlink", mutate: func(o *identityAppObservation) { o.Bundle.SymlinkFree = false }},
		{name: "CLI chain symlink", mutate: func(o *identityAppObservation) { o.Executable.SymlinkFree = false }},
		{name: "app wrong case", mutate: func(o *identityAppObservation) { o.Bundle.ExactCase = false }},
		{name: "CLI wrong case", mutate: func(o *identityAppObservation) { o.Executable.ExactCase = false }},
		{name: "CLI nonregular", mutate: func(o *identityAppObservation) { o.Executable.Kind = identityPathOther }},
		{name: "CLI no execute bit", mutate: func(o *identityAppObservation) { o.Executable.Executable = false }},
		{name: "bundle path", mutate: func(o *identityAppObservation) { o.Bundle.Path = "/Applications/tailscale.app" }},
		{name: "CLI path", mutate: func(o *identityAppObservation) { o.Executable.Path += ".old" }},
		{name: "device", mutate: func(o *identityAppObservation) { o.Executable.Device++ }},
		{name: "inode", mutate: func(o *identityAppObservation) { o.Executable.Inode++ }},
		{name: "generation", mutate: func(o *identityAppObservation) { o.Executable.Generation++ }},
		{name: "uid", mutate: func(o *identityAppObservation) { o.Executable.UID++ }},
		{name: "gid", mutate: func(o *identityAppObservation) { o.Executable.GID++ }},
		{name: "type mode", mutate: func(o *identityAppObservation) { o.Executable.Mode ^= 0o100000 }},
		{name: "permission mode", mutate: func(o *identityAppObservation) { o.Executable.Mode ^= 0o020 }},
		{name: "link count", mutate: func(o *identityAppObservation) { o.Executable.LinkCount++ }},
		{name: "device type", mutate: func(o *identityAppObservation) { o.Executable.DeviceType++ }},
		{name: "size", mutate: func(o *identityAppObservation) { o.Executable.Size++ }},
		{name: "flags", mutate: func(o *identityAppObservation) { o.Executable.Flags++ }},
		{name: "birth seconds", mutate: func(o *identityAppObservation) { o.Executable.BirthTime.Seconds++ }},
		{name: "birth nanos", mutate: func(o *identityAppObservation) { o.Executable.BirthTime.Nanoseconds++ }},
		{name: "change time", mutate: func(o *identityAppObservation) { o.Executable.ChangeTime.Seconds++ }},
		{name: "modify time", mutate: func(o *identityAppObservation) { o.Executable.ModifyTime.Nanoseconds++ }},
		{name: "digest", mutate: func(o *identityAppObservation) { o.Executable.Digest[0] ^= 0xff }},
		{name: "bundle device", mutate: func(o *identityAppObservation) { o.Bundle.Device++ }},
		{name: "bundle inode", mutate: func(o *identityAppObservation) { o.Bundle.Inode++ }},
		{name: "bundle generation", mutate: func(o *identityAppObservation) { o.Bundle.Generation++ }},
		{name: "bundle uid", mutate: func(o *identityAppObservation) { o.Bundle.UID++ }},
		{name: "bundle gid", mutate: func(o *identityAppObservation) { o.Bundle.GID++ }},
		{name: "bundle permission mode", mutate: func(o *identityAppObservation) { o.Bundle.Mode ^= 0o020 }},
		{name: "bundle link count", mutate: func(o *identityAppObservation) { o.Bundle.LinkCount++ }},
		{name: "bundle device type", mutate: func(o *identityAppObservation) { o.Bundle.DeviceType++ }},
		{name: "bundle size", mutate: func(o *identityAppObservation) { o.Bundle.Size++ }},
		{name: "bundle flags", mutate: func(o *identityAppObservation) { o.Bundle.Flags++ }},
		{name: "bundle birth time", mutate: func(o *identityAppObservation) { o.Bundle.BirthTime.Seconds++ }},
		{name: "bundle change time", mutate: func(o *identityAppObservation) { o.Bundle.ChangeTime.Nanoseconds++ }},
		{name: "bundle modify time", mutate: func(o *identityAppObservation) { o.Bundle.ModifyTime.Seconds++ }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := baseline
			mutation.mutate(&changed)
			if err := validateIdentityAppObservation(baseline, changed); err == nil {
				t.Fatalf("mutation was admitted: %#v", changed)
			}
		})
	}
}

func TestValidateIdentityAppObservationRejectsInvalidAdmissionFacts(t *testing.T) {
	t.Parallel()
	mutations := []struct {
		name   string
		mutate func(*identityAppObservation)
	}{
		{name: "zero executable digest", mutate: func(o *identityAppObservation) { o.Executable.Digest = [32]byte{} }},
		{name: "hard linked executable", mutate: func(o *identityAppObservation) { o.Executable.LinkCount = 2 }},
		{name: "zero executable links", mutate: func(o *identityAppObservation) { o.Executable.LinkCount = 0 }},
		{name: "zero bundle links", mutate: func(o *identityAppObservation) { o.Bundle.LinkCount = 0 }},
		{name: "zero executable size", mutate: func(o *identityAppObservation) { o.Executable.Size = 0 }},
		{name: "directory mode mismatch", mutate: func(o *identityAppObservation) { o.Bundle.Mode = 0o100755 }},
		{name: "regular mode mismatch", mutate: func(o *identityAppObservation) { o.Executable.Mode = 0o40755 }},
		{name: "execute bool without mode bit", mutate: func(o *identityAppObservation) { o.Executable.Mode = 0o100644 }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			observation := baselineIdentityObservation()
			test.mutate(&observation)
			if err := validateIdentityAppObservation(observation, observation); err == nil {
				t.Fatalf("invalid base observation admitted: %#v", observation)
			}
		})
	}
}

type identityFakePathState struct {
	mu                 sync.Mutex
	observation        identityAppObservation
	observeCalls       int
	failObserve        map[int]error
	mutateObserve      map[int]func(*identityAppObservation)
	events             *[]string
	replacement        bool
	closeExecutable    int
	closeBundle        int
	executableCloseErr error
	bundleCloseErr     error
	closeStarted       chan struct{}
	allowClose         chan struct{}
}

func (state *identityFakePathState) Observe(_ context.Context) (identityAppObservation, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.observeCalls++
	if state.events != nil {
		*state.events = append(*state.events, fmt.Sprintf("revalidate-%d", state.observeCalls))
	}
	if mutate := state.mutateObserve[state.observeCalls]; mutate != nil {
		mutate(&state.observation)
	}
	if err := state.failObserve[state.observeCalls]; err != nil {
		return identityAppObservation{}, err
	}
	return state.observation, nil
}

func (state *identityFakePathState) CloseExecutable() error {
	state.mu.Lock()
	state.closeExecutable++
	started := state.closeStarted
	allow := state.allowClose
	err := state.executableCloseErr
	state.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if allow != nil {
		<-allow
	}
	return err
}

func (state *identityFakePathState) CloseBundle() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closeBundle++
	return state.bundleCloseErr
}

func identityFakeOpener(state *identityFakePathState, openErr error) identityAppPathOpener {
	return func(context.Context, string, string) (identityAppPathState, identityAppObservation, error) {
		return state, state.observation, openErr
	}
}

type identityRecordedCommand struct {
	path  string
	args  []string
	env   []string
	limit int
}

type identityFakeTrustRunner struct {
	mu          sync.Mutex
	commands    []identityRecordedCommand
	outputs     map[int][]byte
	failures    map[int]error
	events      *[]string
	consumerABA func(int)
	mutateInput func(int, []string, []string)
}

func (runner *identityFakeTrustRunner) Run(_ context.Context, path string, args []string, env []string, limit int) ([]byte, error) {
	runner.mu.Lock()
	index := len(runner.commands) + 1
	runner.commands = append(runner.commands, identityRecordedCommand{path: path, args: append([]string(nil), args...), env: append([]string(nil), env...), limit: limit})
	if runner.events != nil {
		*runner.events = append(*runner.events, fmt.Sprintf("command-%d", index))
	}
	aba := runner.consumerABA
	mutateInput := runner.mutateInput
	output := append([]byte(nil), runner.outputs[index]...)
	err := runner.failures[index]
	runner.mu.Unlock()
	if aba != nil {
		aba(index)
	}
	if mutateInput != nil {
		mutateInput(index, args, env)
	}
	return output, err
}

func successfulIdentityVerifierDependencies(events *[]string) (*identityFakePathState, *identityFakeTrustRunner, identityAppPathOpener) {
	state := &identityFakePathState{
		observation: baselineIdentityObservation(), failObserve: map[int]error{},
		mutateObserve: map[int]func(*identityAppObservation){}, events: events,
	}
	runner := &identityFakeTrustRunner{
		outputs:  map[int][]byte{3: []byte("Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n")},
		failures: map[int]error{}, events: events,
	}
	return state, runner, identityFakeOpener(state, nil)
}

func TestVerifyDarwinAppUsesExactRequirementGatekeeperDisplayPolicy(t *testing.T) {
	t.Parallel()
	events := []string{}
	state, runner, opener := successfulIdentityVerifierDependencies(&events)
	verified, err := verifyDarwinAppWithDependencies(
		context.Background(),
		"/Applications/Tailscale.app",
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
		expectedTailscaleAppRequirement,
		opener,
		runner,
	)
	if err != nil {
		t.Fatalf("verifyDarwinAppWithDependencies() error = %v", err)
	}
	if verified.Identity != (codeIdentity{BundleID: "io.tailscale.ipn.macsys", TeamID: "W5364U7YZB"}) || verified.Guard == nil {
		t.Fatalf("verified = %#v", verified)
	}
	wantCommands := []identityRecordedCommand{
		{path: "/usr/bin/codesign", args: []string{"--verify", "--deep", "--strict", "--verbose=4", "-R", expectedTailscaleAppRequirement, "/Applications/Tailscale.app"}, env: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, limit: 4 << 20},
		{path: "/usr/sbin/spctl", args: []string{"--assess", "--type", "execute", "/Applications/Tailscale.app"}, env: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, limit: 4 << 20},
		{path: "/usr/bin/codesign", args: []string{"--display", "--verbose=4", "/Applications/Tailscale.app"}, env: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, limit: 4 << 20},
	}
	runner.mu.Lock()
	gotCommands := append([]identityRecordedCommand(nil), runner.commands...)
	runner.mu.Unlock()
	if fmt.Sprint(gotCommands) != fmt.Sprint(wantCommands) {
		t.Fatalf("commands = %#v, want %#v", gotCommands, wantCommands)
	}
	wantEvents := []string{"revalidate-1", "revalidate-2", "command-1", "revalidate-3", "revalidate-4", "command-2", "revalidate-5", "revalidate-6", "command-3", "revalidate-7", "revalidate-8"}
	if fmt.Sprint(events) != fmt.Sprint(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	state.mu.Lock()
	if state.closeExecutable != 0 || state.closeBundle != 0 {
		t.Fatalf("accepted guard closed early: executable=%d bundle=%d", state.closeExecutable, state.closeBundle)
	}
	state.mu.Unlock()
	if err := verified.Guard.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestVerifyDarwinAppStopsAtEveryPersistentBoundaryAndCloses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		failObserve  int
		wantCommands int
	}{
		{name: "initial admission", failObserve: 1, wantCommands: 0},
		{name: "before verify", failObserve: 2, wantCommands: 0},
		{name: "after verify", failObserve: 3, wantCommands: 1},
		{name: "before spctl", failObserve: 4, wantCommands: 1},
		{name: "after spctl", failObserve: 5, wantCommands: 2},
		{name: "before display", failObserve: 6, wantCommands: 2},
		{name: "after display", failObserve: 7, wantCommands: 3},
		{name: "post-parse transfer", failObserve: 8, wantCommands: 3},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, runner, opener := successfulIdentityVerifierDependencies(nil)
			state.mutateObserve[test.failObserve] = func(observation *identityAppObservation) {
				observation.Executable.Inode++
			}
			_, err := verifyDarwinAppWithDependencies(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, expectedTailscaleAppRequirement, opener, runner)
			if !errors.Is(err, errTailscaleAppVerification) {
				t.Fatalf("error = %v, want fixed verification error", err)
			}
			runner.mu.Lock()
			commands := len(runner.commands)
			runner.mu.Unlock()
			if commands != test.wantCommands {
				t.Fatalf("commands = %d, want %d", commands, test.wantCommands)
			}
			state.mu.Lock()
			if state.closeExecutable != 1 || state.closeBundle != 1 {
				t.Fatalf("close executable=%d bundle=%d, want 1/1", state.closeExecutable, state.closeBundle)
			}
			state.mu.Unlock()
		})
	}
}

func TestIdentityTrustEnvironmentIsFreshAndRunnerMutationCannotAffectLaterPhases(t *testing.T) {
	t.Parallel()
	first := newIdentityTrustEnvironment()
	first[0] = "DYLD_INSERT_LIBRARIES=/tmp/hostile.dylib"
	second := newIdentityTrustEnvironment()
	want := []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	if fmt.Sprint(second) != fmt.Sprint(want) {
		t.Fatalf("fresh environment = %v, want %v", second, want)
	}

	state, runner, opener := successfulIdentityVerifierDependencies(nil)
	runner.mutateInput = func(_ int, args, env []string) {
		args[0] = "--hostile-mutation"
		env[0] = "DYLD_INSERT_LIBRARIES=/tmp/hostile.dylib"
	}
	verified, err := verifyDarwinAppWithDependencies(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, expectedTailscaleAppRequirement, opener, runner)
	if err != nil {
		t.Fatalf("verifyDarwinAppWithDependencies() error = %v", err)
	}
	defer func() { _ = verified.Guard.Close() }()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for index, command := range runner.commands {
		if fmt.Sprint(command.env) != fmt.Sprint(want) {
			t.Fatalf("command %d environment = %v, want %v", index+1, command.env, want)
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.observeCalls != 8 {
		t.Fatalf("revalidations = %d, want 8", state.observeCalls)
	}
}

func TestVerifyDarwinAppCancellationAfterDisplayParseClosesGuard(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	state, runner, opener := successfulIdentityVerifierDependencies(nil)
	state.mutateObserve[8] = func(*identityAppObservation) { cancel() }
	verified, err := verifyDarwinAppWithDependencies(ctx, fixedTailscaleBundlePath, fixedTailscaleExecutablePath, expectedTailscaleAppRequirement, opener, runner)
	if !errors.Is(err, errTailscaleAppVerification) || verified.Guard != nil {
		t.Fatalf("verified=%#v error=%v, want cancellation rejection", verified, err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closeExecutable != 1 || state.closeBundle != 1 {
		t.Fatalf("close executable=%d bundle=%d, want 1/1", state.closeExecutable, state.closeBundle)
	}
}

func TestNewIdentityAppExecutionGuardNormalizesNilContextBeforeOpener(t *testing.T) {
	t.Parallel()
	state := &identityFakePathState{
		observation: baselineIdentityObservation(), failObserve: map[int]error{},
		mutateObserve: map[int]func(*identityAppObservation){},
	}
	openerSawNil := false
	opener := func(ctx context.Context, _, _ string) (identityAppPathState, identityAppObservation, error) {
		openerSawNil = ctx == nil
		return state, state.observation, nil
	}
	guard, err := newIdentityAppExecutionGuard(nil, fixedTailscaleBundlePath, fixedTailscaleExecutablePath, opener)
	if err != nil {
		t.Fatalf("newIdentityAppExecutionGuard() error = %v", err)
	}
	if openerSawNil {
		t.Fatal("opener received nil context")
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNewIdentityAppExecutionGuardCanceledContextDoesNotInvokeOpener(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := 0
	opener := func(context.Context, string, string) (identityAppPathState, identityAppObservation, error) {
		opened++
		return nil, identityAppObservation{}, nil
	}
	guard, err := newIdentityAppExecutionGuard(ctx, fixedTailscaleBundlePath, fixedTailscaleExecutablePath, opener)
	if !errors.Is(err, errTailscaleAppVerification) || guard != nil || opened != 0 {
		t.Fatalf("guard=%#v error=%v opened=%d, want nil/fixed/0", guard, err, opened)
	}
}

func TestNewIdentityAppExecutionGuardCancellationDuringAdmissionClosesState(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	state := &identityFakePathState{
		observation: baselineIdentityObservation(), failObserve: map[int]error{},
		mutateObserve: map[int]func(*identityAppObservation){1: func(*identityAppObservation) { cancel() }},
	}
	guard, err := newIdentityAppExecutionGuard(ctx, fixedTailscaleBundlePath, fixedTailscaleExecutablePath, identityFakeOpener(state, nil))
	if !errors.Is(err, errTailscaleAppVerification) || guard != nil {
		t.Fatalf("guard=%#v error=%v, want nil/fixed", guard, err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closeExecutable != 1 || state.closeBundle != 1 {
		t.Fatalf("close executable=%d bundle=%d, want 1/1", state.closeExecutable, state.closeBundle)
	}
}

func TestNewIdentityAppExecutionGuardClosesPartialOpenAndInvalidAdmission(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		openErr error
		mutate  func(*identityAppObservation)
	}{
		{name: "partial open error", openErr: errors.New("raw open error")},
		{name: "invalid admission", mutate: func(observation *identityAppObservation) { observation.Executable.LinkCount = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &identityFakePathState{
				observation: baselineIdentityObservation(), failObserve: map[int]error{},
				mutateObserve: map[int]func(*identityAppObservation){},
			}
			if test.mutate != nil {
				test.mutate(&state.observation)
			}
			guard, err := newIdentityAppExecutionGuard(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, identityFakeOpener(state, test.openErr))
			if !errors.Is(err, errTailscaleAppVerification) || guard != nil {
				t.Fatalf("guard=%#v error=%v", guard, err)
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.closeExecutable != 1 || state.closeBundle != 1 {
				t.Fatalf("close executable=%d bundle=%d, want 1/1", state.closeExecutable, state.closeBundle)
			}
		})
	}
}

func TestVerifyDarwinAppStopsOnEveryCommandAndParseFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		command      int
		failure      error
		display      []byte
		wantCommands int
	}{
		{name: "requirement exit", command: 1, failure: errors.New("codesign raw path"), wantCommands: 1},
		{name: "requirement timeout", command: 1, failure: context.DeadlineExceeded, wantCommands: 1},
		{name: "requirement overflow", command: 1, failure: errIdentityTrustOutput, wantCommands: 1},
		{name: "spctl exit", command: 2, failure: errors.New("spctl rejected /Applications/Tailscale.app"), wantCommands: 2},
		{name: "display exit", command: 3, failure: errors.New("display raw"), wantCommands: 3},
		{name: "display parse", display: []byte("Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=not set\n"), wantCommands: 3},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, runner, opener := successfulIdentityVerifierDependencies(nil)
			if test.command != 0 {
				runner.failures[test.command] = test.failure
			}
			if test.display != nil {
				runner.outputs[3] = test.display
			}
			_, err := verifyDarwinAppWithDependencies(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, expectedTailscaleAppRequirement, opener, runner)
			if !errors.Is(err, errTailscaleAppVerification) || fmt.Sprint(err) != "Tailscale application verification failed" {
				t.Fatalf("error = %v, want fixed verification error", err)
			}
			runner.mu.Lock()
			commands := len(runner.commands)
			runner.mu.Unlock()
			if commands != test.wantCommands {
				t.Fatalf("commands = %d, want %d", commands, test.wantCommands)
			}
			state.mu.Lock()
			if state.closeExecutable != 1 || state.closeBundle != 1 {
				t.Fatalf("close executable=%d bundle=%d", state.closeExecutable, state.closeBundle)
			}
			state.mu.Unlock()
		})
	}
}

func TestIdentityAppGuardCloseJoinsOnceAndRejectsUseAfterCloseBegins(t *testing.T) {
	t.Parallel()
	state := &identityFakePathState{
		observation: baselineIdentityObservation(), failObserve: map[int]error{},
		mutateObserve: map[int]func(*identityAppObservation){},
		closeStarted:  make(chan struct{}), allowClose: make(chan struct{}),
	}
	guard, err := newIdentityAppExecutionGuard(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, identityFakeOpener(state, nil))
	if err != nil {
		t.Fatalf("newIdentityAppExecutionGuard() error = %v", err)
	}
	const callers = 32
	results := make(chan error, callers)
	for index := 0; index < callers; index++ {
		go func() { results <- guard.Close() }()
	}
	select {
	case <-state.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("Close did not begin")
	}
	if err := guard.Revalidate(context.Background()); !errors.Is(err, errTailscaleAppVerification) {
		t.Fatalf("Revalidate during close = %v", err)
	}
	if guard.BundlePath() != "" || guard.ExecutablePath() != "" {
		t.Fatal("path access succeeded after close began")
	}
	close(state.allowClose)
	for index := 0; index < callers; index++ {
		if err := <-results; err != nil {
			t.Fatalf("Close caller %d error = %v", index, err)
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closeExecutable != 1 || state.closeBundle != 1 {
		t.Fatalf("underlying closes executable=%d bundle=%d, want 1/1", state.closeExecutable, state.closeBundle)
	}
}

func TestIdentityAppGuardCloseFailureIsFixedAndJoined(t *testing.T) {
	t.Parallel()
	state := &identityFakePathState{
		observation: baselineIdentityObservation(), failObserve: map[int]error{},
		mutateObserve:      map[int]func(*identityAppObservation){},
		executableCloseErr: errors.New("fd 22 raw"), bundleCloseErr: errors.New("fd 21 raw"),
		closeStarted: make(chan struct{}), allowClose: make(chan struct{}),
	}
	guard, err := newIdentityAppExecutionGuard(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, identityFakeOpener(state, nil))
	if err != nil {
		t.Fatalf("newIdentityAppExecutionGuard() error = %v", err)
	}
	const callers = 16
	results := make(chan error, callers)
	for index := 0; index < callers; index++ {
		go func() { results <- guard.Close() }()
	}
	select {
	case <-state.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("Close did not begin")
	}
	close(state.allowClose)
	for index := 0; index < callers; index++ {
		if err := <-results; !errors.Is(err, errTailscaleAppCleanup) || fmt.Sprint(err) != "Tailscale application verification cleanup failed" {
			t.Fatalf("Close %d error = %v", index, err)
		}
	}
	if err := guard.Close(); !errors.Is(err, errTailscaleAppCleanup) {
		t.Fatalf("repeated Close error = %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closeExecutable != 1 || state.closeBundle != 1 {
		t.Fatalf("underlying closes executable=%d bundle=%d, want 1/1", state.closeExecutable, state.closeBundle)
	}
}

func TestVerifyDarwinAppCloseFailureOverridesSuccessAndOtherFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		commandErr error
	}{
		{name: "apparent success"},
		{name: "command failure", commandErr: errors.New("raw codesign detail")},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, runner, opener := successfulIdentityVerifierDependencies(nil)
			state.executableCloseErr = errors.New("raw close")
			if test.commandErr != nil {
				runner.failures[1] = test.commandErr
			} else {
				runner.outputs[3] = []byte("TeamIdentifier=W5364U7YZB\n")
			}
			_, err := verifyDarwinAppWithDependencies(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, expectedTailscaleAppRequirement, opener, runner)
			if !errors.Is(err, errTailscaleAppCleanup) || fmt.Sprint(err) != "Tailscale application verification cleanup failed" {
				t.Fatalf("error = %v, want fixed cleanup error", err)
			}
		})
	}
}

func TestIdentityVerifierCharacterizesIntraCommandABAResidual(t *testing.T) {
	t.Parallel()
	state, runner, opener := successfulIdentityVerifierDependencies(nil)
	consumerSawReplacement := 0
	runner.consumerABA = func(int) {
		state.mu.Lock()
		state.replacement = true
		if state.replacement {
			consumerSawReplacement++
		}
		state.replacement = false
		state.mu.Unlock()
	}
	verified, err := verifyDarwinAppWithDependencies(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, expectedTailscaleAppRequirement, opener, runner)
	if err != nil {
		t.Fatalf("verifyDarwinAppWithDependencies() error = %v", err)
	}
	if consumerSawReplacement != 3 {
		t.Fatalf("pathname consumers observing temporary replacement = %d, want 3", consumerSawReplacement)
	}
	if err := verified.Guard.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// This passing characterization is intentionally not object-binding proof:
	// both boundary observations saw the admitted object while each pathname
	// consumer could see a same-UID intra-command replacement.
}

func TestIdentityGuardPreExecValidationRejectsPersistentSwap(t *testing.T) {
	t.Parallel()
	state, runner, opener := successfulIdentityVerifierDependencies(nil)
	verified, err := verifyDarwinAppWithDependencies(context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath, expectedTailscaleAppRequirement, opener, runner)
	if err != nil {
		t.Fatalf("verifyDarwinAppWithDependencies() error = %v", err)
	}
	state.mu.Lock()
	state.observation.Executable.Digest[0] ^= 0xff
	state.mu.Unlock()
	cliCalls := 0
	if verified.Guard.Revalidate(context.Background()) == nil {
		cliCalls++
	}
	if cliCalls != 0 {
		t.Fatal("CLI dispatched after pre-exec persistent swap")
	}
	if err := verified.Guard.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestVerifyDarwinAppRejectsNonliteralInputsBeforeOpening(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		bundle      string
		executable  string
		requirement string
	}{
		{name: "alternate bundle", bundle: "/tmp/Tailscale.app", executable: fixedTailscaleExecutablePath, requirement: expectedTailscaleAppRequirement},
		{name: "alternate executable", bundle: fixedTailscaleBundlePath, executable: "/tmp/Tailscale", requirement: expectedTailscaleAppRequirement},
		{name: "alternate requirement", bundle: fixedTailscaleBundlePath, executable: fixedTailscaleExecutablePath, requirement: expectedTailscaleAppRequirement + " and true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			opened := 0
			opener := func(context.Context, string, string) (identityAppPathState, identityAppObservation, error) {
				opened++
				return nil, identityAppObservation{}, errors.New("unexpected")
			}
			_, err := verifyDarwinAppWithDependencies(context.Background(), test.bundle, test.executable, test.requirement, opener, &identityFakeTrustRunner{})
			if !errors.Is(err, errTailscaleAppVerification) || opened != 0 {
				t.Fatalf("error=%v opened=%d, want fixed error/0", err, opened)
			}
		})
	}
}
