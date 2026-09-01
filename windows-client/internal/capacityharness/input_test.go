//go:build capacityharness

package capacityharness

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRunSecretsAcceptsOnlyBoundedStrictInput(t *testing.T) {
	t.Parallel()

	tokenMaterial := []byte("0123456789abcdefghijklmnopqrstuv")
	token := base64.RawURLEncoding.EncodeToString(tokenMaterial)
	valid := `{"token":"` + token + `","targetHost":"echo.example.com","targetPort":443}`
	secrets, err := ReadRunSecrets(strings.NewReader(valid))
	if err != nil {
		t.Fatalf("ReadRunSecrets(valid) = %v", err)
	}
	if len(secrets.Token) != tokenBytes || secrets.TargetHost != "echo.example.com" || secrets.TargetPort != 443 {
		t.Fatalf("ReadRunSecrets(valid) = token %d bytes, host %q, port %d", len(secrets.Token), secrets.TargetHost, secrets.TargetPort)
	}
	secrets.Zero()
	for index, value := range secrets.Token {
		if value != 0 {
			t.Fatalf("Zero() left token byte %d non-zero", index)
		}
	}

	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "unknown field", raw: strings.TrimSuffix(valid, "}") + `,"identity":"SECRET-IDENTITY"}`},
		{name: "duplicate token", raw: `{"token":"` + token + `","token":"` + token + `","targetHost":"echo.example.com","targetPort":443}`},
		{name: "duplicate target host", raw: `{"token":"` + token + `","targetHost":"echo.example.com","targetHost":"echo.example.com","targetPort":443}`},
		{name: "duplicate target port", raw: `{"token":"` + token + `","targetHost":"echo.example.com","targetPort":443,"targetPort":443}`},
		{name: "trailing input", raw: valid + `{}`},
		{name: "weak token", raw: `{"token":"d2Vhaw","targetHost":"echo.example.com","targetPort":443}`},
		{name: "unsafe port", raw: `{"token":"` + token + `","targetHost":"echo.example.com","targetPort":80}`},
		{name: "IP destination", raw: `{"token":"` + token + `","targetHost":"127.0.0.1","targetPort":443}`},
		{name: "URL destination", raw: `{"token":"` + token + `","targetHost":"https://echo.example.com","targetPort":443}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ReadRunSecrets(strings.NewReader(test.raw)); err == nil {
				t.Fatalf("ReadRunSecrets(%s) succeeded", test.name)
			} else if strings.Contains(err.Error(), "SECRET-IDENTITY") {
				t.Fatal("validation error disclosed secret input")
			}
		})
	}
}

func TestReadSecretDocumentsRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", maxSecretDocumentBytes+1)
	if _, err := ReadRunSecrets(strings.NewReader(oversized)); err == nil {
		t.Fatal("ReadRunSecrets accepted oversized input")
	}
	if _, err := ReadTargetSecrets(strings.NewReader(oversized)); err == nil {
		t.Fatal("ReadTargetSecrets accepted oversized input")
	}
}

func TestReadTargetSecretsRequiresTokenHostnameAndProtectedFilePaths(t *testing.T) {
	t.Parallel()

	tokenMaterial := []byte("abcdefghijklmnopqrstuvwxyz012345")
	token := base64.RawURLEncoding.EncodeToString(tokenMaterial)
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "fullchain.pem")
	privateKeyFile := filepath.Join(directory, "privkey.pem")
	valid := targetSecretTestDocument(t, token, "echo.example.com", certificateFile, privateKeyFile)
	secrets, err := ReadTargetSecrets(strings.NewReader(valid))
	if err != nil {
		t.Fatalf("ReadTargetSecrets(valid) = %v", err)
	}
	if len(secrets.Token) != tokenBytes || secrets.Hostname != "echo.example.com" ||
		secrets.CertificateFile != certificateFile || secrets.PrivateKeyFile != privateKeyFile {
		t.Fatalf("ReadTargetSecrets(valid) = %#v", secrets)
	}
	secrets.Zero()

	certificateJSON := targetSecretTestJSONValue(t, certificateFile)
	privateKeyJSON := targetSecretTestJSONValue(t, privateKeyFile)
	tokenJSON := targetSecretTestJSONValue(t, token)
	for _, raw := range []string{
		strings.TrimSuffix(valid, "}") + `,"destination":"SECRET-DESTINATION"}`,
		strings.TrimSuffix(valid, "}") + `,"token":` + tokenJSON + `}`,
		strings.TrimSuffix(valid, "}") + `,"hostname":"echo.example.com"}`,
		strings.TrimSuffix(valid, "}") + `,"certificateFile":` + certificateJSON + `}`,
		strings.TrimSuffix(valid, "}") + `,"privateKeyFile":` + privateKeyJSON + `}`,
		targetSecretTestDocument(t, token, "echo.example.com", "relative.pem", privateKeyFile),
		targetSecretTestDocument(t, token, "echo.example.com", certificateFile, "relative.pem"),
		targetSecretTestDocument(t, token, "localhost", certificateFile, privateKeyFile),
	} {
		if _, err := ReadTargetSecrets(strings.NewReader(raw)); err == nil {
			t.Fatal("ReadTargetSecrets accepted invalid input")
		} else if strings.Contains(err.Error(), "SECRET-DESTINATION") {
			t.Fatal("validation error disclosed secret input")
		}
	}
}

func targetSecretTestDocument(t *testing.T, token, hostname, certificateFile, privateKeyFile string) string {
	t.Helper()
	document, err := json.Marshal(targetSecretDocument{
		Token: token, Hostname: hostname,
		CertificateFile: certificateFile, PrivateKeyFile: privateKeyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

func targetSecretTestJSONValue(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
