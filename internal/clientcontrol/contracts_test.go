package clientcontrol

import (
	"encoding/base64"
	"testing"
)

func TestEndpointUpdateStrictDecode(t *testing.T) {
	for _, raw := range []string{`{"version":1,"type":"client-endpoint-update","nodeId":"paired-one","enrollmentId":"id","envelope":{},"privateKey":"secret"}`, `{"version":2,"type":"client-endpoint-update","nodeId":"paired-one","enrollmentId":"id","envelope":{}}`, `{"version":1,"type":"client-endpoint-update","nodeId":"paired-one","enrollmentId":"id","envelope":{}} {}`} {
		if _, err := DecodeEndpointUpdate(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatal("accepted invalid update")
		}
	}
}

func TestBootstrapRejectsUnsupportedPlatformAndBadPublicKey(t *testing.T) {
	for _, b := range []Bootstrap{{Platform: "linux", Architecture: "amd64"}, {Platform: "windows", Architecture: "arm64"}, {Platform: "macos", Architecture: "arm64", ConfigurationPublicKey: "secret"}} {
		if err := b.Validate(); err == nil {
			t.Fatal("invalid bootstrap accepted")
		}
	}
}
