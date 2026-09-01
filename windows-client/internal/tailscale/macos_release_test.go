package tailscale

import (
	"strings"
	"testing"
)

func TestParseStableMacPackagePageSelectsNewestCanonicalPackage(t *testing.T) {
	t.Parallel()

	page := []byte(`<html><body>
<a href="Tailscale-1.98.2-macos.pkg">old</a>
<a href="https://pkgs.tailscale.com/stable/Tailscale-1.100.1-macos.pkg">new</a>
</body></html>`)
	got, err := ParseStableMacPackagePage(page)
	if err != nil {
		t.Fatal(err)
	}
	want := MacRelease{
		Version:     "1.100.1",
		PKGURL:      "https://pkgs.tailscale.com/stable/Tailscale-1.100.1-macos.pkg",
		ChecksumURL: "https://pkgs.tailscale.com/stable/Tailscale-1.100.1-macos.pkg.sha256",
	}
	if got != want {
		t.Fatalf("ParseStableMacPackagePage() = %#v, want %#v", got, want)
	}
}

func TestParseStableMacPackagePageRejectsNoncanonicalCandidates(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"hostile absolute":        `<a href="https://evil.example/Tailscale-1.100.1-macos.pkg">bad</a>`,
		"embedded path":           `<a href="nested/Tailscale-1.100.1-macos.pkg">bad</a>`,
		"link text only":          `<a href="download">Tailscale-1.100.1-macos.pkg</a>`,
		"script":                  `<script>"Tailscale-1.100.1-macos.pkg"</script>`,
		"comment":                 `<!-- Tailscale-1.100.1-macos.pkg -->`,
		"MSI":                     `<a href="Tailscale-1.100.1-macos.msi">bad</a>`,
		"zip":                     `<a href="Tailscale-1.100.1-macos.zip">bad</a>`,
		"architecture suffix":     `<a href="Tailscale-1.100.1-macos-arm64.pkg">bad</a>`,
		"lowercase prefix":        `<a href="tailscale-1.100.1-macos.pkg">bad</a>`,
		"two components":          `<a href="Tailscale-1.100-macos.pkg">bad</a>`,
		"four components":         `<a href="Tailscale-1.100.1.2-macos.pkg">bad</a>`,
		"prerelease":              `<a href="Tailscale-1.100.1-rc1-macos.pkg">bad</a>`,
		"build":                   `<a href="Tailscale-1.100.1+1-macos.pkg">bad</a>`,
		"query":                   `<a href="Tailscale-1.100.1-macos.pkg?x=1">bad</a>`,
		"fragment":                `<a href="Tailscale-1.100.1-macos.pkg#x">bad</a>`,
		"extra extension":         `<a href="Tailscale-1.100.1-macos.pkg.txt">bad</a>`,
		"leading zero":            `<a href="Tailscale-1.0100.1-macos.pkg">bad</a>`,
		"canonical URL with port": `<a href="https://pkgs.tailscale.com:443/stable/Tailscale-1.100.1-macos.pkg">bad</a>`,
		"empty":                   ``,
	}
	for name, page := range tests {
		name, page := name, page
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseStableMacPackagePage([]byte(page)); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}

	tooLarge := strings.Repeat("x", (8<<20)+1)
	if _, err := ParseStableMacPackagePage([]byte(tooLarge)); err == nil {
		t.Fatal("accepted a page larger than 8 MiB")
	}
}
