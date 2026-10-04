package nodeservice

import "time"

// StandaloneStatus is intentionally independent of the secret-bearing repository.
type StandaloneStatus struct {
	Transport           string     `json:"transport"`
	ActivationState     string     `json:"activationState"`
	GatewayState        string     `json:"gatewayState"`
	ClientID            string     `json:"clientId"`
	DisplayName         string     `json:"displayName"`
	BindAddress         string     `json:"bindAddress"`
	Endpoint            string     `json:"endpoint"`
	Paired              bool       `json:"paired"`
	UpdatePending       bool       `json:"updatePending"`
	InvitationExpiresAt *time.Time `json:"invitationExpiresAt,omitempty"`
	Phase               string     `json:"phase"`
	Message             string     `json:"message"`
	Running             bool       `json:"running"`
	Connected           bool       `json:"connected"`
	SOCKSAddress        string     `json:"socksAddress"`
	HTTPAddress         string     `json:"httpAddress"`
	Generation          uint64     `json:"generation"`
	Version             string     `json:"version"`
}
