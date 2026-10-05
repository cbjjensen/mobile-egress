package clientapp

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

func TestCompactQRPreservesOriginalBundleAndReducesInvitationDensity(t *testing.T) {
	fixture, err := os.ReadFile("../../../testdata/compact-qr-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Valid []struct{ Name, Original, Compact string }
	}
	if err := json.Unmarshal(fixture, &cases); err != nil {
		t.Fatal(err)
	}
	for _, sample := range cases.Valid {
		t.Run(sample.Name, func(t *testing.T) {
			compact, err := compactQRBundle(sample.Original)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(compact, "MEQR1:") {
				t.Fatal("QR representation was not compacted")
			}
			for _, representation := range []string{compact, sample.Compact} {
				compressed, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(representation, "MEQR1:"))
				if err != nil {
					t.Fatal(err)
				}
				reader, err := zlib.NewReader(bytes.NewReader(compressed))
				if err != nil {
					t.Fatal(err)
				}
				expanded, err := io.ReadAll(io.LimitReader(reader, 65537))
				if err != nil {
					t.Fatal(err)
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				if len(expanded) > 65536 || base64.RawURLEncoding.EncodeToString(expanded) != sample.Original {
					t.Fatal("QR envelope changed original bundle bytes")
				}
			}
			if sample.Name != "update" {
				oldQR, err := qrcode.New(sample.Original, qrcode.Medium)
				if err != nil {
					t.Fatal(err)
				}
				newQR, err := qrcode.New(compact, qrcode.Medium)
				if err != nil {
					t.Fatal(err)
				}
				if len(newQR.Bitmap()) >= len(oldQR.Bitmap()) || len(compact)*100 > len(sample.Original)*80 {
					t.Fatal("invitation QR density did not materially decrease")
				}
			}
		})
	}
}

func TestCompactQRRejectsInvalidAndOversizedSource(t *testing.T) {
	for _, input := range []string{"", "not base64", "YQ==", "YR", "YQ\n", strings.Repeat("A", 87385), base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("a"), 65537))} {
		if _, err := compactQRBundle(input); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
}

func TestCompactQRDoesNotAcceptAlreadyWrappedInput(t *testing.T) {
	if _, err := compactQRBundle("MEQR1:eA"); err == nil {
		t.Fatal("nested envelope accepted")
	}
}
