package relayclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/sealedconfig"
)

// ClientControlError deliberately excludes response bodies and capabilities.
type ClientControlError struct {
	StatusCode int
	Code       string
}

// ErrClientUnauthorized indicates that the relay rejected this Client identity.
var ErrClientUnauthorized = errors.New("Client identity is no longer authorized")

func (err *ClientControlError) Error() string {
	return fmt.Sprintf("Client control request rejected: %s (HTTP %d)", err.Code, err.StatusCode)
}

func CreateClientInvitation(ctx context.Context, owner Identity, nodeID, displayName string) (clientcontrol.Invitation, error) {
	var value clientcontrol.Invitation
	if !clientcontrol.ValidID(nodeID) || !clientcontrol.ValidText(displayName, 128) {
		return value, errors.New("invalid Client name or ID")
	}
	err := ownerClientControl(ctx, owner, "", map[string]string{"nodeId": nodeID, "displayName": displayName}, &value)
	if err != nil {
		return value, err
	}
	if value.Validate() != nil || value.NodeID != nodeID || value.DisplayName != displayName || value.CACertificatePEM != owner.CACertificatePEM || !time.Now().Before(value.ExpiresAt) {
		return clientcontrol.Invitation{}, errors.New("relay returned an invalid Client invitation")
	}
	origin, err := pairing.RelayOrigin(owner.RelayURL)
	if err != nil {
		return clientcontrol.Invitation{}, err
	}
	returned, err := pairing.RelayOrigin(value.RelayURL)
	if err != nil || returned.String() != origin.String() {
		return clientcontrol.Invitation{}, errors.New("relay returned an invitation for a different origin")
	}
	return value, nil
}

func CancelClientInvitation(ctx context.Context, owner Identity, id string) error {
	if !clientcontrol.ValidID(id) {
		return errors.New("invalid Client enrollment ID")
	}
	return ownerClientControl(ctx, owner, "/cancel", map[string]string{"id": id}, nil)
}

func ClientEnrollments(ctx context.Context, owner Identity) ([]clientcontrol.Enrollment, error) {
	var values []clientcontrol.Enrollment
	if err := ownerClientControl(ctx, owner, "/list", struct{}{}, &values); err != nil {
		return nil, err
	}
	if len(values) > 100 {
		return nil, errors.New("relay returned too many Client enrollments")
	}
	for _, value := range values {
		if err := validateClientEnrollment(value, owner.CACertificatePEM); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func ApproveClientEnrollment(ctx context.Context, owner Identity, id string) (clientcontrol.Enrollment, error) {
	var value clientcontrol.Enrollment
	if !clientcontrol.ValidID(id) {
		return value, errors.New("invalid Client enrollment ID")
	}
	if err := ownerClientControl(ctx, owner, "/approve", map[string]string{"id": id}, &value); err != nil {
		return value, err
	}
	if value.ID != id || value.ClientSerial == "" {
		return clientcontrol.Enrollment{}, errors.New("relay returned an invalid approved Client")
	}
	if err := validateClientEnrollment(value, owner.CACertificatePEM); err != nil {
		return clientcontrol.Enrollment{}, err
	}
	return value, nil
}

func DeliverClientConfiguration(ctx context.Context, owner Identity, id string, generation uint64, envelope sealedconfig.Envelope) error {
	if !clientcontrol.ValidID(id) || generation == 0 || generation > math.MaxInt64 {
		return errors.New("invalid Client configuration delivery")
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if err = clientcontrol.ValidateEnvelope(raw); err != nil {
		return err
	}
	return ownerClientControl(ctx, owner, "/configuration", struct {
		ID         string          `json:"id"`
		Generation uint64          `json:"generation"`
		Envelope   json.RawMessage `json:"envelope"`
	}{id, generation, raw}, nil)
}

func SubmitClientBootstrap(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return clientBootstrapControl(ctx, invitation, bootstrap, "/bootstrap")
}

func PollClientEnrollment(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return clientBootstrapControl(ctx, invitation, bootstrap, "/poll")
}

func AcknowledgeClientConfiguration(ctx context.Context, identity Identity, id string, generation uint64, version string) error {
	return ReportClientStatus(ctx, identity, id, generation, version)
}

// ReportClientStatus also acknowledges updates imported without relay delivery.
// The controller reconciles this receipt against its own desired generation.
func ReportClientStatus(ctx context.Context, identity Identity, id string, generation uint64, version string) error {
	if identity.Role != "client" {
		return errors.New("Client identity required")
	}
	if !clientcontrol.ValidID(id) || generation == 0 || generation > math.MaxInt64 || !clientcontrol.ValidText(version, 64) {
		return errors.New("invalid Client configuration receipt")
	}
	return authenticatedClientControl(ctx, identity, "/acknowledge", struct {
		ID             string `json:"id"`
		Generation     uint64 `json:"generation"`
		ServiceVersion string `json:"serviceVersion"`
	}{id, generation, version}, nil)
}

func OwnerClientStatuses(ctx context.Context, owner Identity) ([]clientcontrol.Status, error) {
	var values []clientcontrol.Status
	if err := ownerClientControl(ctx, owner, "/statuses", struct{}{}, &values); err != nil {
		return nil, err
	}
	if len(values) > 10 {
		return nil, errors.New("relay returned too many Client statuses")
	}
	for _, value := range values {
		if !validSerial(value.ClientSerial) || value.AppliedGeneration > math.MaxInt64 {
			return nil, errors.New("relay returned invalid Client status")
		}
	}
	return values, nil
}

func ownerClientControl(ctx context.Context, owner Identity, path string, input, output any) error {
	if owner.Role != "owner" {
		return errors.New("owner identity required")
	}
	return authenticatedClientControl(ctx, owner, path, input, output)
}

func authenticatedClientControl(ctx context.Context, identity Identity, path string, input, output any) error {
	origin, err := validateRelayURL(identity.RelayURL)
	if err != nil {
		return err
	}
	client, transport, err := identityHTTPClient(identity)
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	return performClientControl(ctx, client, origin.String(), path, input, output)
}

func clientBootstrapControl(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap, path string) (clientcontrol.Enrollment, error) {
	var value clientcontrol.Enrollment
	if err := invitation.Validate(); err != nil {
		return value, err
	}
	if err := bootstrap.Validate(); err != nil {
		return value, err
	}
	origin, err := validateRelayURL(invitation.RelayURL)
	if err != nil {
		return value, err
	}
	ca, err := pairing.CACertificate(invitation.CACertificatePEM)
	if err != nil {
		return value, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: origin.Hostname()}}
	defer transport.CloseIdleConnections()
	input := struct {
		ID         string                  `json:"id"`
		Capability string                  `json:"capability"`
		Bootstrap  clientcontrol.Bootstrap `json:"bootstrap"`
	}{invitation.ID, invitation.Capability, bootstrap}
	if err = performClientControl(ctx, &http.Client{Transport: transport}, origin.String(), path, input, &value); err != nil {
		return value, err
	}
	if value.ID != invitation.ID || value.NodeID != invitation.NodeID || value.Bootstrap == nil || *value.Bootstrap != bootstrap {
		return clientcontrol.Enrollment{}, errors.New("relay returned an enrollment for different Client keys")
	}
	if err = validateClientEnrollment(value, invitation.CACertificatePEM); err != nil {
		return clientcontrol.Enrollment{}, err
	}
	return value, nil
}

func performClientControl(ctx context.Context, client *http.Client, origin, path string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if len(raw) > clientcontrol.MaxMessageBytes {
		return errors.New("Client control request too large")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/v1/client-enrollments"+path, bytes.NewReader(raw))
	if err != nil {
		return errors.New("invalid Client control request")
	}
	request.Header.Set("Content-Type", "application/json")
	client.Timeout = 15 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return errors.New("Client relay control is unavailable")
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, clientcontrol.MaxMessageBytes+1))
	if err != nil {
		return errors.New("Client relay response is unavailable")
	}
	if response.StatusCode != 200 && response.StatusCode != 201 && response.StatusCode != 204 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		switch failure.Error {
		case "invalid_capability", "client_limit", "bootstrap_conflict", "enrollment_state", "configuration_generation", "invalid_request", "invalid_public_key", "owner_required", "client_required", "unauthorized", "internal_error":
		default:
			failure.Error = "request_rejected"
		}
		return &ClientControlError{StatusCode: response.StatusCode, Code: failure.Error}
	}
	if output == nil {
		if response.StatusCode != 204 || len(raw) != 0 {
			return errors.New("relay returned invalid Client control response")
		}
		return nil
	}
	if err = clientcontrol.DecodeJSON(raw, output); err != nil {
		return errors.New("relay returned invalid Client control response")
	}
	return nil
}

func validateClientEnrollment(value clientcontrol.Enrollment, caPEM string) error {
	if !clientcontrol.ValidID(value.ID) || !clientcontrol.ValidID(value.NodeID) || !clientcontrol.ValidText(value.DisplayName, 128) || value.ExpiresAt.IsZero() || value.AppliedGeneration > math.MaxInt64 {
		return errors.New("relay returned invalid Client enrollment")
	}
	switch value.State {
	case "invited", "submitted", "approved", "delivered", "acknowledged", "canceled", "expired":
	default:
		return errors.New("relay returned invalid Client enrollment state")
	}
	if value.Bootstrap != nil {
		if err := value.Bootstrap.Validate(); err != nil {
			return errors.New("relay returned invalid Client bootstrap")
		}
	}
	if value.ClientSerial != "" {
		if value.Bootstrap == nil || !validSerial(value.ClientSerial) {
			return errors.New("relay returned invalid Client identity")
		}
		block, _ := pem.Decode([]byte(value.Bootstrap.CSRPEM))
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil {
			return errors.New("relay returned invalid Client request")
		}
		ca, err := pairing.CACertificate(caPEM)
		if err != nil {
			return err
		}
		if err = validateIssuedPublicIdentity("client", csr.PublicKey, ca, enrollResponse{Role: "client", Serial: value.ClientSerial, CertificatePEM: value.CertificatePEM, CACertificatePEM: value.CACertificatePEM}); err != nil {
			return err
		}
	}
	if value.Configuration != nil {
		if value.Configuration.Generation == 0 || clientcontrol.ValidateEnvelope(value.Configuration.Envelope) != nil {
			return errors.New("relay returned invalid Client configuration")
		}
	}
	return nil
}
