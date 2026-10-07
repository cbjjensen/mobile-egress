package nodeservice

import "time"

// DirectConfiguration separates the local listener from the public origin.
// The latter may have a different port when a router forwards inbound TLS.
type DirectConfiguration struct {
	Transport   string `json:"transport,omitempty"`
	BindAddress string `json:"bindAddress"`
	Endpoint    string `json:"endpoint"`
	DisplayName string `json:"displayName"`
}

type directInvitation struct {
	Transport        string    `json:"transport,omitempty"`
	Version          int       `json:"version"`
	Type             string    `json:"type"`
	ClientID         string    `json:"clientId"`
	DisplayName      string    `json:"displayName"`
	Endpoint         string    `json:"endpoint"`
	CACertificatePEM string    `json:"caCertificatePem"`
	InvitationID     string    `json:"invitationId"`
	Capability       string    `json:"capability"`
	ExpiresAt        time.Time `json:"expiresAt"`
	Role             string    `json:"role"`
}

type directIdentityResponse struct {
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
	Serial           string `json:"serial"`
	Role             string `json:"role"`
	ClientID         string `json:"clientId"`
	PairingID        string `json:"pairingId"`
	Generation       uint64 `json:"generation"`
}

type directPairing struct {
	ID             string                 `json:"id"`
	PublicKey      []byte                 `json:"publicKey"`
	Identity       directIdentityResponse `json:"identity"`
	PreviousSerial string                 `json:"previousSerial,omitempty"`
	Acknowledged   bool                   `json:"acknowledged"`
	Revoked        bool                   `json:"revoked"`
}

type directState struct {
	Phones                 []*directPhone       `json:"phones,omitempty"`
	Hosted                 *hostedState         `json:"hosted,omitempty"`
	Activation             *activationState     `json:"activation,omitempty"`
	Version                int                  `json:"version"`
	ClientID               string               `json:"clientId"`
	Configuration          *DirectConfiguration `json:"configuration,omitempty"`
	Generation             uint64               `json:"generation"`
	AcknowledgedGeneration uint64               `json:"acknowledgedGeneration"`
	AcknowledgedEndpoint   string               `json:"acknowledgedEndpoint,omitempty"`
	CACertificatePEM       string               `json:"caCertificatePem"`
	CAPrivateKeyPEM        string               `json:"caPrivateKeyPem"`
	ServerCertificatePEM   string               `json:"serverCertificatePem"`
	ServerPrivateKeyPEM    string               `json:"serverPrivateKeyPem"`
	Username               string               `json:"username"`
	Password               string               `json:"password"`
	Invitation             *directInvitation    `json:"invitation,omitempty"`
	Pairing                *directPairing       `json:"pairing,omitempty"`
	MigrationRequired      bool                 `json:"migrationRequired"`
}

// Legacy singleton fields above are read only during the atomic v2 migration.
// New schema records retain only the first-use credentials until slot 0 is added.
type directPhone struct {
	InvitationGeneration   uint64            `json:"invitationGeneration"`
	ID                     string            `json:"phoneId"`
	Name                   string            `json:"name"`
	Slot                   int               `json:"slot"`
	Username               string            `json:"username"`
	Password               string            `json:"password"`
	Invitation             *directInvitation `json:"invitation,omitempty"`
	Pairing                *directPairing    `json:"pairing,omitempty"`
	AcknowledgedGeneration uint64            `json:"acknowledgedGeneration"`
	AcknowledgedEndpoint   string            `json:"acknowledgedEndpoint,omitempty"`
}

type PhoneStatus struct {
	ID                  string     `json:"phoneId"`
	Name                string     `json:"name"`
	Slot                int        `json:"slot"`
	Paired              bool       `json:"paired"`
	Connected           bool       `json:"connected"`
	UpdatePending       bool       `json:"updatePending"`
	Phase               string     `json:"phase"`
	Message             string     `json:"message"`
	SOCKSAddress        string     `json:"socksAddress"`
	HTTPAddress         string     `json:"httpAddress"`
	ProxyRunning        bool       `json:"proxyRunning"`
	InvitationExpiresAt *time.Time `json:"invitationExpiresAt,omitempty"`
}
type PhonesStatus struct {
	Phones         []PhoneStatus `json:"phones"`
	MaxPhones      int           `json:"maxPhones"`
	PendingPhoneID string        `json:"pendingPhoneId,omitempty"`
}
type PhoneInvitation struct {
	PhoneID string `json:"phoneId"`
	Bundle  string `json:"bundle"`
}
