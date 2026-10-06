package macosrelease

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Run the actual installer script in a private fixture. Only privileged OS
// commands and installation paths are replaced; shell branching is unchanged.
func TestClientMacInstallerHandsOffOnlyToActiveSavedOwner(t *testing.T) {
	for _, tc := range []struct {
		name, owner, console, gui, open string
		wantLaunch                      bool
	}{
		{"owner", "501", "501", "yes", "ok", true},
		{"another saved owner", "502", "502", "yes", "ok", true},
		{"different foreground user", "501", "502", "yes", "ok", false},
		{"headless repair", "501", "0", "no", "ok", false},
		{"missing GUI domain", "501", "501", "no", "ok", false},
		{"console lookup unavailable", "501", "error", "yes", "ok", false},
		{"empty console lookup", "501", "empty", "yes", "ok", false},
		{"nested UID only", "501", "nested", "yes", "ok", false},
		{"duplicate console UID", "501", "duplicate", "yes", "ok", false},
		{"malformed console UID", "501", "malformed", "yes", "ok", false},
		{"launch failure", "501", "501", "yes", "fail", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newClientInstallerFixture(t)
			fixture.owner = tc.owner
			if err := os.WriteFile(filepath.Join(fixture.state, "owner.uid"), []byte(tc.owner+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := fixture.run(t, tc.console, tc.gui, tc.open)
			if err != nil {
				t.Fatalf("GUI handoff affected completed installation: %v\n%s", err, output)
			}
			journal := fixture.journal(t)
			if !strings.Contains(journal, "kickstart system/com.zfnf.mobile-egress.client\n") {
				t.Fatal("installation did not start the daemon")
			}
			launched := strings.Contains(journal, "open uid="+tc.owner+" app=/Applications/Inevitable Mobile Relay.app\n")
			if launched != tc.wantLaunch {
				t.Fatalf("owner GUI launch = %v, want %v; journal:\n%s", launched, tc.wantLaunch, journal)
			}
			if launched && (strings.Index(journal, "asuser "+tc.owner+" ") < strings.Index(journal, "kickstart ") || !strings.Contains(journal, "print gui/"+tc.owner+"\n")) {
				t.Fatalf("GUI launch did not enter the owner session after daemon startup:\n%s", journal)
			}
			if (!tc.wantLaunch || tc.open == "fail") && !strings.Contains(string(output), "Applications") {
				t.Fatalf("missing manual launch fallback: %s", output)
			}
			owner, err := os.ReadFile(filepath.Join(fixture.state, "owner.uid"))
			if err != nil || string(owner) != tc.owner+"\n" {
				t.Fatalf("repair changed saved ownership: %q %v", owner, err)
			}
			credentials, err := os.ReadFile(filepath.Join(fixture.state, "retained-state"))
			if err != nil || string(credentials) != "preserve fixture state" {
				t.Fatal("repair changed protected configuration")
			}
		})
	}
}

func TestClientMacInstallerDoesNotWaitIndefinitelyForGUI(t *testing.T) {
	t.Parallel()
	fixture := newClientInstallerFixture(t)
	started := time.Now()
	output, err := fixture.run(t, "501", "yes", "hang")
	if err != nil {
		t.Fatalf("stalled GUI handoff failed installation: %v\n%s", err, output)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("GUI handoff exceeded its bound: %v", elapsed)
	}
	if !strings.Contains(fixture.journal(t), "open uid=501") || !strings.Contains(string(output), "Applications") {
		t.Fatalf("stalled launch was not attempted with a manual fallback: %s", output)
	}
}

func TestClientMacInstallerBoundsGUISessionLookup(t *testing.T) {
	t.Parallel()
	fixture := newClientInstallerFixture(t)
	started := time.Now()
	output, err := fixture.run(t, "501", "hang", "ok")
	if err != nil || time.Since(started) > 8*time.Second {
		t.Fatalf("GUI domain lookup held up installation: %v\n%s", err, output)
	}
	if strings.Contains(fixture.journal(t), "open uid=") || !strings.Contains(string(output), "Applications") {
		t.Fatalf("unavailable GUI lookup must skip launch and provide fallback: %s", output)
	}
}

func TestClientMacInstallerBoundsConsoleUserLookup(t *testing.T) {
	t.Parallel()
	fixture := newClientInstallerFixture(t)
	started := time.Now()
	output, err := fixture.run(t, "hang", "yes", "ok")
	if err != nil || time.Since(started) > 8*time.Second {
		t.Fatalf("console user lookup held up installation: %v\n%s", err, output)
	}
	if strings.Contains(fixture.journal(t), "open uid=") || !strings.Contains(string(output), "Applications") {
		t.Fatalf("unavailable console user lookup must skip launch and provide fallback: %s", output)
	}
	if !strings.Contains(fixture.journal(t), "scutil\n") {
		t.Fatal("console-user lookup was not attempted")
	}
}

type clientInstallerFixture struct {
	shell, script, state, log, owner string
	app, legacy                      string
}

// Each fixture launches several real shell processes. Bound that fan-out so
// parallel race builds do not turn host scheduling contention into installer
// timeout failures. Acquire before callers start their elapsed-time checks;
// the production handoff and per-command timeout budgets remain unchanged.
var clientInstallerFixtureSlots = make(chan struct{}, 3)

func newClientInstallerFixture(t *testing.T) clientInstallerFixture {
	return newClientInstallerScriptFixture(t, "postinstall", true)
}

func newClientInstallerScriptFixture(t *testing.T, scriptName string, existing bool) clientInstallerFixture {
	t.Helper()
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "sh.exe")
	}
	if _, err := os.Stat(shell); err != nil {
		t.Skip("installer fixture requires a POSIX shell")
	}
	clientInstallerFixtureSlots <- struct{}{}
	t.Cleanup(func() { <-clientInstallerFixtureSlots })
	root := t.TempDir()
	state := filepath.Join(root, "state")
	app := filepath.Join(root, "Applications", "Inevitable Mobile Relay.app")
	legacy := filepath.Join(root, "Applications", "ZFNF Mobile Egress Client.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("verified GUI"), 0600); err != nil {
		t.Fatal(err)
	}
	if existing {
		if err := os.MkdirAll(filepath.Join(state, "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if existing {
		write(filepath.Join(state, "owner.uid"), "501\n")
		write(filepath.Join(state, "retained-state"), "preserve fixture state")
		write(filepath.Join(state, "bin", "mobile-egress-client"), "fixture daemon")
	}
	log := filepath.Join(root, "commands.log")
	write(log, "")
	source, err := os.ReadFile("../../macos/client/scripts/" + scriptName)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(source), "\r\n", "\n")
	script = strings.ReplaceAll(script, "/Library/Application Support/MobileEgressClient", filepath.ToSlash(state))
	script = strings.ReplaceAll(script, "/Library/LaunchDaemons/com.zfnf.mobile-egress.client.plist", filepath.ToSlash(filepath.Join(root, "daemon.plist")))
	script = strings.ReplaceAll(script, "APP='/Applications/Inevitable Mobile Relay.app'", "APP='"+filepath.ToSlash(app)+"'")
	script = strings.ReplaceAll(script, "LEGACY_APP='/Applications/ZFNF Mobile Egress Client.app'", "LEGACY_APP='"+filepath.ToSlash(legacy)+"'")
	script = strings.ReplaceAll(script, "@@TEAM_ID@@", "ABCDEFGHIJ")
	commands := map[string]string{
		"/usr/bin/id": `printf '0\n'`,
		// A remote GUI session can belong to UID 501 while /dev/console
		// remains root-owned. File metadata is not the active-user authority.
		"/usr/bin/stat": `case "$2" in %u) printf '0\n';; %Lp) printf '700\n';; *) exit 46;; esac`,
		"/usr/sbin/scutil": `printf 'scutil\n' >> "$FIXTURE_LOG"
case "$FIXTURE_CONSOLE" in
  error) printf '<dictionary> {\n  UID : 501\n}\n'; exit 1;;
  hang) exec /bin/sleep 30;;
  empty) exit 0;;
  nested) printf '<dictionary> {\n  SessionInfo : <array> {\n    0 : <dictionary> {\n      UID : 501\n    }\n  }\n}\n';;
  duplicate) printf '<dictionary> {\n  UID : 501\n  UID : 501\n}\n';;
  malformed) printf '<dictionary> {\n  UID : 501oops\n}\n';;
  *) printf '<dictionary> {\n  Name : fixture-user\n  SessionInfo : <array> {\n    0 : <dictionary> {\n      UID : 999\n    }\n  }\n  UID : %s\n}\n' "$FIXTURE_CONSOLE";;
esac`,
		"/usr/sbin/chown": `exit 0`,
		"/bin/chmod":      `exit 0`,
		"/usr/bin/plutil": `for path; do :; done
value=$(cat "$path")
case "$2" in
  CFBundleIdentifier) if [ "$value" = wrong-id ]; then printf 'unrelated.bundle\n'; else printf 'com.zfnf.mobile-egress.client.app\n'; fi;;
  CFBundleExecutable) if [ "$value" = wrong-executable ]; then printf 'unrelated-app\n'; else printf 'mobile-egress-client-app\n'; fi;;
  *) exit 47;;
esac`,
		"/usr/bin/codesign": `for path; do :; done
printf 'codesign %s\n' "$*" >> "$FIXTURE_LOG"
if [ -d "$path" ]; then
  case "$*" in *'anchor apple generic and identifier "com.zfnf.mobile-egress.client.app" and certificate leaf[subject.OU] = "ABCDEFGHIJ"'*) ;; *) exit 48;; esac
  [ "$(cat "$path/Contents/Info.plist")" != wrong-signer ] || exit 49
fi
exit 0`,
		"/bin/mv": `[ "$FIXTURE_OPEN" != move-fail ] || exit 50
exec /bin/mv "$@"`,
		"/bin/launchctl": `printf '%s\n' "$*" >> "$FIXTURE_LOG"
case "$1" in
  print) case "$2" in
    gui/*) if [ "$FIXTURE_GUI" = hang ]; then exec /bin/sleep 30; fi
      [ "$2" = "gui/$FIXTURE_OWNER" ] && [ "$FIXTURE_GUI" = yes ];;
    system/*) exit 1;;
    *) exit 42;;
    esac;;
  asuser) [ "$2" = "$FIXTURE_OWNER" ] || exit 41; shift 2; exec "$@";;
  kickstart) [ "$FIXTURE_OPEN" != daemon-fail ];;
  enable|bootstrap) exit 0;;
  *) exit 42;;
esac`,
		"/usr/bin/sudo": `[ "$1" = -n ] && [ "$2" = -H ] && [ "$3" = -u ] && [ "$4" = "#$FIXTURE_OWNER" ] || exit 43
shift 4
export FIXTURE_LAUNCH_UID="$FIXTURE_OWNER"
exec "$@"`,
		"/usr/bin/open": `[ "$1" = -a ] && [ "$2" = '/Applications/Inevitable Mobile Relay.app' ] && [ "$#" = 2 ] || exit 44
printf 'open uid=%s app=%s\n' "${FIXTURE_LAUNCH_UID:-0}" "$2" >> "$FIXTURE_LOG"
case "$FIXTURE_OPEN" in
  fail) exit 45;;
  hang) exec /bin/sleep 30;;
esac`,
	}
	for command, body := range commands {
		path := filepath.Join(root, filepath.Base(command))
		write(path, "#!/bin/sh\nset -eu\n"+body+"\n")
		script = strings.ReplaceAll(script, command, "'"+strings.ReplaceAll(filepath.ToSlash(path), "'", "'\\''")+"'")
	}
	path := filepath.Join(root, scriptName)
	write(path, script)
	return clientInstallerFixture{shell: shell, script: path, state: state, log: log, owner: "501", app: app, legacy: legacy}
}

func TestClientMacUpgradeArchivesOnlyVerifiedLegacyGUIAfterServiceStarts(t *testing.T) {
	fixture := newClientInstallerFixture(t)
	writeLegacyGUI(t, fixture, "verified GUI")
	output, err := fixture.run(t, "501", "yes", "ok")
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if _, err := os.Stat(fixture.legacy); !os.IsNotExist(err) {
		t.Fatal("legacy Applications GUI remains")
	}
	matches, _ := filepath.Glob(filepath.Join(fixture.state, ".legacy-gui-recovery.*", "ZFNF Mobile Egress Client.app", "Contents", "Info.plist"))
	if len(matches) != 1 {
		t.Fatal("verified legacy GUI was not retained for recovery")
	}
	b, _ := os.ReadFile(matches[0])
	if string(b) != "verified GUI" {
		t.Fatal("recovery lost legacy bytes")
	}
	if !strings.Contains(fixture.journal(t), "kickstart system/com.zfnf.mobile-egress.client") {
		t.Fatal("service did not start")
	}
}

func TestClientMacFailedReplacementLeavesLegacyGUIAvailable(t *testing.T) {
	for _, failure := range []string{"wrong-signer", "daemon-fail", "move-fail"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newClientInstallerFixture(t)
			writeLegacyGUI(t, fixture, "verified GUI")
			if failure == "wrong-signer" {
				if err := os.WriteFile(filepath.Join(fixture.app, "Contents", "Info.plist"), []byte(failure), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if output, err := fixture.run(t, "501", "yes", failure); err == nil {
				t.Fatalf("failed replacement accepted: %s", output)
			}
			b, err := os.ReadFile(filepath.Join(fixture.legacy, "Contents", "Info.plist"))
			if err != nil || string(b) != "verified GUI" {
				t.Fatal("old app unavailable after failure")
			}
			b, err = os.ReadFile(filepath.Join(fixture.state, "retained-state"))
			if err != nil || string(b) != "preserve fixture state" {
				t.Fatal("protected state changed")
			}
		})
	}
}

func TestClientMacPreflightRejectsConflictingApplicationBeforeStoppingService(t *testing.T) {
	for _, pathName := range []string{"branded", "legacy"} {
		for _, conflict := range []string{"wrong-id", "wrong-executable", "wrong-signer", "file", "symlink"} {
			t.Run(pathName+"/"+conflict, func(t *testing.T) {
				fixture := newClientInstallerScriptFixture(t, "preinstall", true)
				path := fixture.app
				if pathName == "legacy" {
					path = fixture.legacy
				}
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				switch conflict {
				case "file":
					if err := os.WriteFile(path, []byte("unrelated"), 0600); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(fixture.state, path); err != nil {
						t.Skip("symlink creation unavailable")
					}
				default:
					if err := os.MkdirAll(filepath.Join(path, "Contents"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(path, "Contents", "Info.plist"), []byte(conflict), 0600); err != nil {
						t.Fatal(err)
					}
				}
				output, err := fixture.run(t, "501", "yes", "ok")
				if err == nil || !strings.Contains(string(output), "unrecognized application") {
					t.Fatalf("conflict accepted: %v %s", err, output)
				}
				if strings.Contains(fixture.journal(t), "system/com.zfnf.mobile-egress.client") {
					t.Fatal("changed service before rejecting conflict")
				}
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("conflicting object lost")
				}
			})
		}
	}
}

func writeLegacyGUI(t *testing.T, fixture clientInstallerFixture, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(fixture.legacy, "Contents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.legacy, "Contents", "Info.plist"), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f clientInstallerFixture) run(t *testing.T, console, gui, open string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, f.shell, filepath.ToSlash(f.script), "fixture.pkg", "/", "/")
	command.Env = append(os.Environ(), "FIXTURE_CONSOLE="+console, "FIXTURE_GUI="+gui, "FIXTURE_OPEN="+open, "FIXTURE_LOG="+filepath.ToSlash(f.log), "FIXTURE_OWNER="+f.owner)
	command.WaitDelay = time.Second
	return command.CombinedOutput()
}

func (f clientInstallerFixture) journal(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
