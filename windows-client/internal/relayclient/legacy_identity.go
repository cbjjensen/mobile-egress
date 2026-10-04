package relayclient

// Identity is the read-only legacy record shape retained for encrypted migration.
type Identity struct {
	RelayURL         string `json:"relayUrl"`
	DialAddress      string `json:"dialAddress,omitempty"`
	Role             string `json:"role"`
	Serial           string `json:"serial"`
	PrivateKeyPEM    string `json:"privateKeyPem"`
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
}
