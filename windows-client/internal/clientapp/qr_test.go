package clientapp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"os"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

func TestDensePairingQRPreservesWholeReadableModules(t *testing.T) {
	// Public disposable interoperability material, never a live invitation.
	raw, err := os.ReadFile("../../../testdata/direct-v2-wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Invitation, Update string }
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	invitationJSON, err := base64.RawURLEncoding.DecodeString(fixture.Invitation)
	if err != nil {
		t.Fatal(err)
	}
	var invitation map[string]any
	if err := json.Unmarshal(invitationJSON, &invitation); err != nil {
		t.Fatal(err)
	}
	invitation["transport"] = "hosted"
	invitation["endpoint"] = "https://r-0123456789abcdef0123456789abcdef.mobile-gateway.example.com"
	hostedJSON, err := json.Marshal(invitation)
	if err != nil {
		t.Fatal(err)
	}
	for name, bundle := range map[string]string{"hosted": base64.RawURLEncoding.EncodeToString(hostedJSON), "direct": fixture.Invitation, "update": fixture.Update} {
		t.Run(name, func(t *testing.T) {
			view, err := renderBundle(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if view.Bundle != bundle {
				t.Fatal("rendering changed the invitation or signed update")
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(view.QRDataURL, "data:image/png;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			qr, err := qrcode.New(bundle, qrcode.Medium)
			if err != nil {
				t.Fatal(err)
			}
			bitmap := qr.Bitmap() // Includes the required four-module quiet zone.
			width := img.Bounds().Dx()
			scale := width / len(bitmap)
			if width != img.Bounds().Dy() || width%len(bitmap) != 0 || scale < 3 {
				t.Fatalf("dense QR needs whole modules of at least 3px: image=%d modules=%d", width, len(bitmap))
			}
			for y := 0; y < width; y++ {
				for x := 0; x < width; x++ {
					r, g, b, _ := img.At(x, y).RGBA()
					black := r == 0 && g == 0 && b == 0
					white := r == 65535 && g == 65535 && b == 65535
					if (!black && !white) || black != bitmap[y/scale][x/scale] {
						t.Fatal("QR module was resampled or quiet zone changed")
					}
				}
			}
			// Optional local optical check with the exact Android ZXing decoder.
			if dir := os.Getenv("MOBILE_EGRESS_QR_TEST_OUTPUT"); dir != "" {
				if err := os.WriteFile(dir+"/"+name+".png", data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dir+"/"+name+".txt", []byte(bundle), 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
