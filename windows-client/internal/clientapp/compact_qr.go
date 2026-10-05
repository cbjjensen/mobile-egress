package clientapp

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"errors"
)

// QR wrapping is presentation-only. Copyable bundles and signed bytes stay intact.
func compactQRBundle(bundle string) (string, error) {
	const maxBytes = 65536
	const maxText = 87384
	invalid := errors.New("Invalid QR bundle.")
	if len(bundle) == 0 || len(bundle) > maxText {
		return "", invalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(bundle)
	if err != nil || len(raw) == 0 || len(raw) > maxBytes || base64.RawURLEncoding.EncodeToString(raw) != bundle {
		return "", invalid
	}
	var compressed bytes.Buffer
	writer, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	if err != nil {
		return "", invalid
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return "", invalid
	}
	if err := writer.Close(); err != nil || compressed.Len() > maxBytes {
		return "", invalid
	}
	compact := "MEQR1:" + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	if len(compact) > maxText {
		return "", invalid
	}
	return compact, nil
}
