package macosrelease

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
)

func TestTrackedMacReleaseInputsAreUsable(t *testing.T) {
	lock, err := os.Open("../../macos/toolchain.lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if _, err := ParsePinnedToolchain(lock); err != nil {
		t.Fatal(err)
	}

	icns, err := os.ReadFile("../../macos/appicon.icns")
	if err != nil {
		t.Fatal(err)
	}
	if len(icns) < 16 || string(icns[:4]) != "icns" || int(binary.BigEndian.Uint32(icns[4:8])) != len(icns) {
		t.Fatal("app icon is not a complete ICNS container")
	}
	representations := map[string]bool{}
	for offset := 8; offset < len(icns); {
		if offset+8 > len(icns) {
			t.Fatal("truncated ICNS chunk header")
		}
		kind := string(icns[offset : offset+4])
		length := int(binary.BigEndian.Uint32(icns[offset+4 : offset+8]))
		if length < 8 || offset+length > len(icns) {
			t.Fatalf("invalid ICNS chunk %q length", kind)
		}
		if _, err := png.Decode(bytes.NewReader(icns[offset+8 : offset+length])); err != nil {
			t.Fatalf("ICNS chunk %q is not PNG-backed: %v", kind, err)
		}
		representations[kind] = true
		offset += length
	}
	for _, required := range []string{"icp4", "ic07", "ic08", "ic09", "ic10"} {
		if !representations[required] {
			t.Fatalf("ICNS representation %q is missing", required)
		}
	}

}

func TestMacPlistTemplatesRenderReleaseAndStableKeychainIdentity(t *testing.T) {
	infoTemplate, err := os.ReadFile("../../macos/client/Info.plist.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	info := strings.ReplaceAll(string(infoTemplate), "@@RELEASE_VERSION@@", "2.0.0")
	info = strings.ReplaceAll(info, "@@BUILD_VERSION@@", "2.0.0")
	values := plistValues(t, info)
	want := map[string]string{
		"CFBundleDisplayName":        "ZFNF Mobile Egress Client",
		"CFBundleExecutable":         "mobile-egress-client-app",
		"CFBundleIdentifier":         "com.zfnf.mobile-egress.client.app",
		"CFBundlePackageType":        "APPL",
		"CFBundleShortVersionString": "2.0.0",
		"CFBundleVersion":            "2.0.0",
		"LSMinimumSystemVersion":     "13.0",
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Fatalf("Info.plist %s = %q, want %q", key, values[key], expected)
		}
	}

}

func TestClientMacReleasePublishesRecordLastAndRemovesOrphanPackage(t *testing.T) {
	script, err := os.ReadFile("../../../scripts/release-client-macos.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	packageMove := strings.Index(text, `/bin/mv "$WORK/$NAME" "$FINAL"`)
	recordMove := strings.Index(text, `/bin/mv "$WORK/record.json" "$RECORD"`)
	if packageMove < 0 || recordMove < packageMove || !strings.Contains(text, `[ "$PKG_PROMOTED" = 1 ] && [ ! -e "$RECORD" ]`) || !strings.Contains(text, `/bin/rm -f -- "$FINAL"`) {
		t.Fatal("Client PKG promotion must retain transactional completion-record semantics")
	}
}

func TestMacReleasePublishesPackageBeforeCompletionRecord(t *testing.T) {
	script, err := os.ReadFile("../../../scripts/release-macos.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	packageMove := strings.Index(text, `/bin/mv "$PKG" "$FINAL_PKG"`)
	recordMove := strings.Index(text, `/bin/mv "$RECORD_TEMP" "$FINAL_RECORD"`)
	if packageMove < 0 || recordMove < 0 || packageMove > recordMove {
		t.Fatal("release must publish the PKG before the verification-record completion marker")
	}
	if !strings.Contains(text, `[ ! -e "$FINAL_RECORD" ]`) ||
		!strings.Contains(text, `/bin/rm -f "$FINAL_PKG"`) {
		t.Fatal("failed completion-record publication must remove the orphan final PKG")
	}
}

func TestMacPackageDisablesBundleRelocation(t *testing.T) {
	component, err := os.ReadFile("../../macos/component.plist")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(component), "<key>BundleIsRelocatable</key>") ||
		!strings.Contains(string(component), "<false/>") {
		t.Fatal("macOS package component must disable bundle relocation")
	}

	script, err := os.ReadFile("../../../scripts/release-macos.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `PKG_ROOT="$WORK/pkg-root"`) ||
		!strings.Contains(string(script), `--root "$PKG_ROOT" --component-plist "$WINDOWS_CLIENT_ROOT/macos/component.plist"`) {
		t.Fatal("macOS release must stage a non-relocatable app root for pkgbuild")
	}
}

func plistValues(t *testing.T, document string) map[string]string {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(document))
	values := map[string]string{}
	key := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return values
			}
			t.Fatal(err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			if err := decoder.DecodeElement(&key, &start); err != nil {
				t.Fatal(err)
			}
		case "string":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				t.Fatal(err)
			}
			if key != "" {
				values[key] = value
				key = ""
			}
		}
	}
}
