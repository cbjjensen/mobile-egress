package macosrelease

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Executes the real POSIX builders with OS/toolchain boundaries replaced. This
// verifies payload ownership and failure cleanup without signing or Mac access.
func TestClientUserBuildContainsOnlyUserAppAndBindsMode(t *testing.T) {
	f := newDMGScriptFixture(t)
	stage := filepath.Join(f.root, "windows-client", "build", "private-user")
	output, err := f.build(t, "2.0.6", stage, "app")
	if err != nil {
		t.Fatalf("user build: %v %s", err, output)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 1 || entries[0].Name() != "Inevitable Mobile Relay.app" {
		t.Fatalf("unexpected user payload: %v %v", entries, err)
	}
	info, err := os.ReadFile(filepath.Join(stage, "Inevitable Mobile Relay.app", "Contents", "Info.plist"))
	if err != nil || !strings.Contains(string(info), "<key>MobileEgressRuntimeMode</key><string>app</string>") || !strings.Contains(string(info), strings.Repeat("a", 40)) {
		t.Fatalf("user metadata unbound: %s %v", info, err)
	}
}

func TestClientUserBuildRejectsWrongRuntimeAndPreservesCallerOwnedStage(t *testing.T) {
	f := newDMGScriptFixture(t)
	stage := filepath.Join(f.root, "windows-client", "build", "private-user")
	output, err := f.build(t, "2.0.6", stage, "service")
	if err == nil || !strings.Contains(string(output), "runtime mode mismatch") {
		t.Fatalf("service binary accepted: %v %s", err, output)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("failed user stage retained")
	}
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stage, "keep")
	if err := os.WriteFile(marker, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := f.build(t, "2.0.6", stage, "app"); err == nil {
		t.Fatalf("existing stage accepted: %s", output)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "owned" {
		t.Fatal("caller stage removed")
	}
}

func TestClientUserBuildRejectsPreDMGVersionBeforeStaging(t *testing.T) {
	f := newDMGScriptFixture(t)
	stage := filepath.Join(f.root, "windows-client", "build", "private-user")
	output, err := f.build(t, "2.0.5", stage, "app")
	if err == nil || !strings.Contains(string(output), "user DMG requires 2.0.6 or later") {
		t.Fatalf("historical DMG build accepted: %v %s", err, output)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("pre-contract user staging created")
	}
}

type dmgScriptFixture struct{ root, shell, posixRoot string }

func TestClientDMGReleaseVerifiesMountedPayloadBeforeExclusivePromotion(t *testing.T) {
	for _, fault := range []string{"none", "mount-hash", "notary", "concurrent-record"} {
		t.Run(fault, func(t *testing.T) {
			f := newDMGScriptFixture(t)
			f.installRelease(t)
			output, err := f.release(t, fault)
			artifact := filepath.Join(f.root, "windows-client", "build", "release", "inevitable-mobile-relay-macos-2.0.6-arm64.dmg")
			record := artifact + ".verification.json"
			if fault == "none" {
				if err != nil {
					t.Fatalf("release failed: %v %s", err, output)
				}
				for _, p := range []string{artifact, record} {
					if _, err := os.Stat(p); err != nil {
						t.Fatal(err)
					}
				}
				if output, err = f.release(t, "none"); err == nil || !strings.Contains(string(output), "already exists") {
					t.Fatalf("immutable output accepted: %v %s", err, output)
				}
			} else {
				if err == nil {
					t.Fatalf("%s fault accepted: %s", fault, output)
				}
				wantFailure := map[string]string{"mount-hash": "mounted app executable hash mismatch", "notary": "app notarization was not accepted", "concurrent-record": "File exists"}[fault]
				if !strings.Contains(string(output), wantFailure) {
					t.Fatalf("wrong %s failure: %v %s", fault, err, output)
				}
				if _, err := os.Stat(artifact); !os.IsNotExist(err) {
					t.Fatalf("failed release left image: %v %s", err, output)
				}
				if fault == "concurrent-record" {
					if b, err := os.ReadFile(record); err != nil || string(b) != "concurrent evidence" {
						t.Fatal("concurrent record altered")
					}
				} else if _, err := os.Stat(record); !os.IsNotExist(err) {
					t.Fatal("failed release left record")
				}
			}
			entries, err := os.ReadDir(filepath.Dir(artifact))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".release-client-dmg.") {
					t.Fatalf("private staging retained after %s", fault)
				}
			}
		})
	}
}

func (f dmgScriptFixture) installRelease(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("../../../scripts/release-client-dmg.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(data), "\r\n", "\n")
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the plist OS boundary fixture")
	}
	plutilJS := filepath.Join(f.root, "fake", "plist.mjs")
	write(plutilJS, `import {readFileSync,writeFileSync} from 'node:fs';
const a=process.argv.slice(2),path=a.at(-1);
if(a[0]==='-lint')process.exit(0);
if(a[0]==='-create'){writeFileSync(path,'{}');process.exit(0)}
let data=readFileSync(path,'utf8');
let p=data.startsWith('{')?JSON.parse(data):{};
if(a[0]==='-extract'){
 const k=a[1]; const found=p[k]??new RegExp('<key>'+k+'</key><string>([^<]*)</string>').exec(data)?.[1];
 if(found===undefined)process.exit(1);console.log(found);process.exit(0);
}
if(a[0]==='-insert'){
 const [parent,child]=a[1].split('.');const v=a[2]==='-dictionary'?{}:a[2]==='-bool'?a[3]==='true':a[2]==='-integer'?Number(a[3]):a[3];
 if(child)p[parent][child]=v;else p[parent]=v;writeFileSync(path,JSON.stringify(p));process.exit(0);
}
if(a[0]==='-convert'){writeFileSync(a[a.indexOf('-o')+1],JSON.stringify(p));process.exit(0)}
process.exit(1);
`)
	commands := map[string]string{
		"/usr/bin/shasum": `shift 2;exec /usr/bin/sha256sum "$@"`,
		"/bin/mkdir":      `if [ "$1" = -m ];then shift 2;fi;exec /bin/mkdir "$@"`,
		"/usr/bin/uname":  `case "$1" in -s) echo Darwin;; -m) echo arm64;; esac`,
		"/usr/bin/codesign": `case "$*" in *" -d "*|-d*)
 echo 'Authority=Developer ID Application: Fixture (ABCDEFGHIJ)' >&2
 echo 'TeamIdentifier=ABCDEFGHIJ' >&2
 echo 'flags=0x10000(runtime)' >&2
 case "$*" in *.dmg) echo 'Identifier=com.zfnf.mobile-egress.client.app.dmg' >&2;; *) echo 'Identifier=com.zfnf.mobile-egress.client.app' >&2;; esac;; esac`,
		"/usr/bin/ditto":  `case "$1" in -c) for arg;do target=$arg;done;echo 'zip fixture' > "$target";; *) /bin/cp -R "$1" "$2";;esac`,
		"/usr/bin/lipo":   `echo arm64`,
		"/usr/sbin/spctl": `exit 0`,
		"/usr/bin/plutil": "'" + filepath.ToSlash(node) + "' '" + filepath.ToSlash(plutilJS) + "' \"$@\"",
		"/usr/bin/xcrun": `case "$1" in
 vtool) echo 'minos 13.0';;
 notarytool) if [ "$FIXTURE_RELEASE_FAULT" = notary ];then echo '{"status":"Invalid"}';else echo '{"status":"Accepted"}';fi;;
 stapler) exit 0;; esac`,
		"/usr/bin/hdiutil": `case "$1" in
 create) for arg;do target=$arg;done;printf 'fixture image' > "$target";;
 attach) image=$2;shift 2;mount='';while [ "$#" -gt 0 ];do case "$1" in -mountpoint) mount=$2;shift 2;; *) shift;;esac;done
 /bin/cp -R "$(dirname "$image")/payload/." "$mount/"
 if [ "$FIXTURE_RELEASE_FAULT" = mount-hash ];then printf '\n#altered' >> "$mount/Inevitable Mobile Relay.app/Contents/MacOS/mobile-egress-client-app";fi;;
 detach) exit 0;; esac`,
	}
	for command, body := range commands {
		path := filepath.Join(f.root, "fake", "release-"+filepath.Base(command))
		write(path, "#!/bin/sh\n"+body+"\n")
		script = strings.ReplaceAll(script, command, "'"+filepath.ToSlash(path)+"'")
	}
	write(filepath.Join(f.root, "scripts", "release-client-dmg.sh"), script)
	readme, err := os.ReadFile("../../macos/client/DMG-ReadMe.txt")
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(f.root, "windows-client", "macos", "client", "DMG-ReadMe.txt"), string(readme))
	write(filepath.Join(f.root, "notary.p8"), "fixture")
}

func (f dmgScriptFixture) release(t *testing.T, fault string) ([]byte, error) {
	t.Helper()
	command := exec.Command(f.shell, filepath.ToSlash(filepath.Join(f.root, "scripts", "release-client-dmg.sh")), "--release-version", "2.0.6", "--source-commit", strings.Repeat("a", 40), "--team-id", "ABCDEFGHIJ", "--application-identity", "Developer ID Application: Fixture (ABCDEFGHIJ)", "--notary-api-key", filepath.ToSlash(filepath.Join(f.root, "notary.p8")), "--notary-api-key-id", "ABCDEFGHIJ", "--notary-api-issuer-id", "11111111-2222-3333-4444-555555555555")
	command.Env = append(os.Environ(), "MOBILE_EGRESS_MAC_BUILD_ROOT="+filepath.ToSlash(filepath.Join(f.root, "toolchain")), "FIXTURE_RUNTIME=app", "FIXTURE_RELEASE_FAULT="+fault, "FIXTURE_ROOT="+filepath.ToSlash(f.root))
	return command.CombinedOutput()
}

func newDMGScriptFixture(t *testing.T) dmgScriptFixture {
	t.Helper()
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "sh.exe")
	}
	if _, err := os.Stat(shell); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	root := t.TempDir()
	canonical, err := exec.Command(shell, "-c", `cd -- "$1" && pwd -P`, "fixture", filepath.ToSlash(root)).Output()
	if err != nil {
		t.Fatal(err)
	}
	posixRoot := strings.TrimSpace(string(canonical))
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile("../../../scripts/build-client-macos.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(data), "\r\n", "\n")
	for command, body := range map[string]string{
		"/bin/mkdir":      `if [ "$1" = -m ];then shift 2;fi;exec /bin/mkdir "$@"`,
		"/usr/bin/uname":  `case "$1" in -s) echo Darwin;; -m) echo arm64;; esac`,
		"/usr/bin/git":    `case "$*" in *rev-parse*) printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa;; *status*) exit 0;; esac`,
		"/usr/bin/lipo":   `echo arm64`,
		"/usr/bin/xcrun":  `echo 'minos 13.0'`,
		"/usr/bin/plutil": `exit 0`,
	} {
		path := filepath.Join(root, "fake", filepath.Base(command))
		write(path, "#!/bin/sh\n"+body+"\n")
		script = strings.ReplaceAll(script, command, "'"+filepath.ToSlash(path)+"'")
	}
	write(filepath.Join(root, "scripts", "build-client-macos.sh"), script)
	write(filepath.Join(root, "scripts", "bootstrap-macos-toolchain.sh"), "#!/bin/sh\nexit 0\n")
	info, err := os.ReadFile("../../macos/client/Info.plist.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "windows-client", "macos", "client", "Info.plist.tmpl"), string(info))
	write(filepath.Join(root, "windows-client", "macos", "client", "com.zfnf.mobile-egress.client.plist"), "fixture daemon plist")
	write(filepath.Join(root, "windows-client", "macos", "appicon.icns"), "fixture icon")
	write(filepath.Join(root, "toolchain", "toolchains", "go", "1.26.7", "bin", "go"), `#!/bin/sh
set -eu
if [ "$1" = version ]; then printf 'path\tmobile-egress/windows-client/cmd/mobile-egress-client-app\nbuild\tvcs.revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nbuild\tvcs.modified=false\nbuild\t-tags=production,client_usermode\n'; exit 0; fi
if [ "$1" = run ]; then
 if [ "$FIXTURE_RELEASE_FAULT" = concurrent-record ];then printf 'concurrent evidence' > "$FIXTURE_ROOT/windows-client/build/release/inevitable-mobile-relay-macos-2.0.6-arm64.dmg.verification.json";fi
 exit 0
fi
OUTPUT='' VERSION='' TAGS=''
while [ "$#" -gt 0 ]; do
 case "$1" in -o) OUTPUT=$2;shift 2;; -ldflags) VERSION=${2#-X main.version=};shift 2;; -tags) TAGS=$2;shift 2;; *) shift;; esac
done
MODE=service
case ",$TAGS," in *,client_usermode,*) MODE=app;; esac
if [ "$FIXTURE_RUNTIME" = service ];then MODE=service;fi
printf '#!/bin/sh\ncase "$1" in --runtime-mode) echo "%s";; --version) echo "%s";; esac\n' "$MODE" "$VERSION" > "$OUTPUT"
`)
	return dmgScriptFixture{root: root, shell: shell, posixRoot: posixRoot}
}
func (f dmgScriptFixture) build(t *testing.T, version, stage, mode string) ([]byte, error) {
	t.Helper()
	stageArg := f.posixRoot + strings.TrimPrefix(filepath.ToSlash(stage), filepath.ToSlash(f.root))
	command := exec.Command(f.shell, filepath.ToSlash(filepath.Join(f.root, "scripts", "build-client-macos.sh")), "--release-version", version, "--source-commit", strings.Repeat("a", 40), "--stage-dir", stageArg, "--runtime-mode", "app")
	command.Env = append(os.Environ(), "MOBILE_EGRESS_MAC_BUILD_ROOT="+filepath.ToSlash(filepath.Join(f.root, "toolchain")), "FIXTURE_RUNTIME="+mode)
	return command.CombinedOutput()
}
