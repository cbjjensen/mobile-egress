package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/relay/internal/enrollment"
)

type clientInvitationRequest struct {
	NodeID      string `json:"nodeId"`
	DisplayName string `json:"displayName"`
}
type clientIDRequest struct {
	ID string `json:"id"`
}
type clientBootstrapRequest struct {
	ID         string                  `json:"id"`
	Capability string                  `json:"capability"`
	Bootstrap  clientcontrol.Bootstrap `json:"bootstrap"`
}
type clientConfigurationRequest struct {
	ID         string          `json:"id"`
	Generation uint64          `json:"generation"`
	Envelope   json.RawMessage `json:"envelope"`
}
type clientReceipt struct {
	ID             string `json:"id"`
	Generation     uint64 `json:"generation"`
	ServiceVersion string `json:"serviceVersion"`
}

func decodeClientControl(request *http.Request, value any) error {
	raw, err := io.ReadAll(io.LimitReader(request.Body, clientcontrol.MaxMessageBytes+1))
	if err != nil {
		return err
	}
	return clientcontrol.DecodeJSON(raw, value)
}

func (service *Service) clientOwner(writer http.ResponseWriter, request *http.Request) bool {
	_, role, status := service.authenticateRequest(request)
	if status != 0 {
		writeAPIError(writer, status, authErrorCode(status))
		return false
	}
	if role != enrollment.RoleOwner {
		writeAPIError(writer, http.StatusForbidden, "owner_required")
		return false
	}
	return true
}

func writeClientResult(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(writer).Encode(value)
	}
}

func writeClientError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCapabilityInvalid), errors.Is(err, errCapabilityExpired):
		writeAPIError(writer, http.StatusUnauthorized, "invalid_capability")
	case errors.Is(err, errIdentityLimit):
		writeAPIError(writer, http.StatusConflict, "client_limit")
	case errors.Is(err, errBootstrapConflict):
		writeAPIError(writer, http.StatusConflict, "bootstrap_conflict")
	case errors.Is(err, errEnrollmentState):
		writeAPIError(writer, http.StatusConflict, "enrollment_state")
	case errors.Is(err, errGeneration):
		writeAPIError(writer, http.StatusConflict, "configuration_generation")
	default:
		writeAPIError(writer, http.StatusInternalServerError, "internal_error")
	}
}

func (service *Service) handleCreateClientInvitation(writer http.ResponseWriter, request *http.Request) {
	if !service.clientOwner(writer, request) {
		return
	}
	var input clientInvitationRequest
	if decodeClientControl(request, &input) != nil || !clientcontrol.ValidID(input.NodeID) || !clientcontrol.ValidText(input.DisplayName, 128) {
		writeAPIError(writer, 400, "invalid_request")
		return
	}
	id, _, err := newCapability()
	if err != nil {
		writeClientError(writer, err)
		return
	}
	origin, err := service.store.relayURL(request.Context())
	if err != nil {
		writeClientError(writer, err)
		return
	}
	now := time.Now().UTC()
	expires := now.Add(clientcontrol.InvitationLifetime).Truncate(time.Second)
	value := clientcontrol.Enrollment{ID: id, NodeID: input.NodeID, DisplayName: input.DisplayName, State: "invited", ExpiresAt: expires}
	capability, err := service.clientInvitationCapability(id)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	value, err = service.store.createClientInvitation(request.Context(), value, capability, now)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	capability, err = service.clientInvitationCapability(value.ID)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeClientResult(writer, 201, clientcontrol.Invitation{Version: clientcontrol.Version, Type: clientcontrol.InvitationType, ID: value.ID, NodeID: value.NodeID, DisplayName: value.DisplayName, RelayURL: origin, CACertificatePEM: string(service.caCertPEM), Capability: capability, ExpiresAt: value.ExpiresAt})
}

// Deriving the scoped capability from existing relay secret material permits
// Owner retries after a lost response while SQLite stores only its hash.
func (service *Service) clientInvitationCapability(id string) (string, error) {
	key, err := x509.MarshalPKCS8PrivateKey(service.caKey)
	if err != nil {
		return "", err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("mobile-egress/client-invitation/v1\x00" + id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (service *Service) handleClientBootstrap(writer http.ResponseWriter, request *http.Request) {
	var input clientBootstrapRequest
	if decodeClientControl(request, &input) != nil || !clientcontrol.ValidID(input.ID) || len(input.Capability) > 128 || input.Bootstrap.Validate() != nil {
		writeAPIError(writer, 400, "invalid_request")
		return
	}
	if _, err := parseDevicePublicKey(input.Bootstrap.CSRPEM, ""); err != nil {
		writeAPIError(writer, 400, "invalid_public_key")
		return
	}
	value, err := service.store.mutateClientEnrollment(request.Context(), input.ID, func(tx *sql.Tx, value *clientcontrol.Enrollment, hash []byte) error {
		capabilityErr := checkClientCapability(*value, hash, input.Capability, time.Now())
		if capabilityErr != nil && !errors.Is(capabilityErr, errCapabilityExpired) {
			return capabilityErr
		}
		if err := activeClientEnrollment(request.Context(), tx, *value); err != nil {
			return err
		}
		if capabilityErr != nil {
			// Expiry still prevents every new claim or submission. A previously
			// approved, active identity may only recover its existing handoff,
			// using the same capability and the exact durably bound public keys.
			canRecover := request.URL.Path == "/v1/client-enrollments/poll" &&
				(value.State == "approved" || value.State == "delivered") &&
				value.ClientSerial != "" && value.Bootstrap != nil && *value.Bootstrap == input.Bootstrap
			if !canRecover {
				return capabilityErr
			}
		}
		if value.Bootstrap != nil {
			if *value.Bootstrap != input.Bootstrap {
				return errBootstrapConflict
			}
			return nil
		}
		if request.URL.Path == "/v1/client-enrollments/poll" {
			return errEnrollmentState
		}
		value.Bootstrap = &input.Bootstrap
		value.ServiceVersion = input.Bootstrap.ServiceVersion
		value.State = "submitted"
		return nil
	})
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeClientResult(writer, 200, value)
}

func (service *Service) handleClientEnrollments(writer http.ResponseWriter, request *http.Request) {
	if !service.clientOwner(writer, request) {
		return
	}
	values, err := service.store.clientEnrollments(request.Context())
	if err != nil {
		writeClientError(writer, err)
		return
	}
	// The Owner already retains envelopes. Listing only needs public bootstrap
	// and certificate material to resume a partially completed handoff.
	for i := range values {
		values[i].Configuration = nil
	}
	writeClientResult(writer, 200, values)
}

func (service *Service) handleApproveClientEnrollment(writer http.ResponseWriter, request *http.Request) {
	if !service.clientOwner(writer, request) {
		return
	}
	var input clientIDRequest
	if decodeClientControl(request, &input) != nil || !clientcontrol.ValidID(input.ID) {
		writeAPIError(writer, 400, "invalid_request")
		return
	}
	value, err := service.store.mutateClientEnrollment(request.Context(), input.ID, func(tx *sql.Tx, value *clientcontrol.Enrollment, _ []byte) error {
		if err := activeClientEnrollment(request.Context(), tx, *value); err != nil {
			return err
		}
		if value.ClientSerial != "" {
			return nil
		}
		if !time.Now().Before(value.ExpiresAt) {
			return errCapabilityExpired
		}
		if value.Bootstrap == nil || value.State != "submitted" {
			return errEnrollmentState
		}
		publicKey, err := parseDevicePublicKey(value.Bootstrap.CSRPEM, "")
		if err != nil {
			return err
		}
		serialNumber, err := randomSerial()
		if err != nil {
			return err
		}
		serial := strings.ToUpper(serialNumber.Text(16))
		now := time.Now().UTC()
		certificate, err := service.signDeviceCertificate(publicKey, enrollment.RoleClient, serialNumber, serial, now)
		if err != nil {
			return err
		}
		// The invitation already owns a capacity slot. Replace that reservation
		// with the identity in the same transaction, without a second admission.
		if _, err = tx.ExecContext(request.Context(), `INSERT INTO identities(serial,role,created_at) VALUES(?, 'client', ?)`, serial, now.Unix()); err != nil {
			return err
		}
		value.ClientSerial = serial
		value.CertificatePEM = string(certificate) + string(service.caCertPEM)
		value.CACertificatePEM = string(service.caCertPEM)
		value.State = "approved"
		return nil
	})
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeClientResult(writer, 200, value)
}

func (service *Service) handleDeliverClientConfiguration(writer http.ResponseWriter, request *http.Request) {
	if !service.clientOwner(writer, request) {
		return
	}
	var input clientConfigurationRequest
	if decodeClientControl(request, &input) != nil || !clientcontrol.ValidID(input.ID) || input.Generation == 0 || input.Generation > math.MaxInt64 || clientcontrol.ValidateEnvelope(input.Envelope) != nil {
		writeAPIError(writer, 400, "invalid_request")
		return
	}
	_, err := service.store.mutateClientEnrollment(request.Context(), input.ID, func(tx *sql.Tx, value *clientcontrol.Enrollment, _ []byte) error {
		if err := activeClientEnrollment(request.Context(), tx, *value); err != nil {
			return err
		}
		if value.ClientSerial == "" {
			return errEnrollmentState
		}
		if input.Generation < value.AppliedGeneration {
			return errGeneration
		}
		if input.Generation == value.AppliedGeneration {
			return nil
		}
		if value.Configuration != nil {
			if input.Generation < value.Configuration.Generation {
				return errGeneration
			}
			if input.Generation == value.Configuration.Generation {
				return nil
			}
		}
		value.Configuration = &clientcontrol.ConfigurationDelivery{Generation: input.Generation, Envelope: input.Envelope}
		value.State = "delivered"
		return nil
	})
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeClientResult(writer, 204, nil)
}

func (service *Service) handleAcknowledgeClientConfiguration(writer http.ResponseWriter, request *http.Request) {
	serial, role, status := service.authenticateRequest(request)
	if status != 0 {
		writeAPIError(writer, status, authErrorCode(status))
		return
	}
	if role != enrollment.RoleClient {
		writeAPIError(writer, 403, "client_required")
		return
	}
	var input clientReceipt
	if decodeClientControl(request, &input) != nil || !clientcontrol.ValidID(input.ID) || input.Generation == 0 || input.Generation > math.MaxInt64 || !clientcontrol.ValidText(input.ServiceVersion, 64) {
		writeAPIError(writer, 400, "invalid_request")
		return
	}
	_, err := service.store.mutateClientEnrollment(request.Context(), input.ID, func(tx *sql.Tx, value *clientcontrol.Enrollment, _ []byte) error {
		if value.ClientSerial != serial {
			return errCapabilityInvalid
		}
		if err := activeClientEnrollment(request.Context(), tx, *value); err != nil {
			return err
		}
		if input.Generation < value.AppliedGeneration {
			return errGeneration
		}
		value.AppliedGeneration = input.Generation
		value.ServiceVersion = input.ServiceVersion
		if value.Configuration == nil || value.Configuration.Generation <= input.Generation {
			value.Configuration = nil
			value.State = "acknowledged"
		}
		return nil
	})
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeClientResult(writer, 204, nil)
}

func (service *Service) handleCancelClientInvitation(writer http.ResponseWriter, request *http.Request) {
	if !service.clientOwner(writer, request) {
		return
	}
	var input clientIDRequest
	if decodeClientControl(request, &input) != nil || !clientcontrol.ValidID(input.ID) {
		writeAPIError(writer, 400, "invalid_request")
		return
	}
	value, err := service.store.mutateClientEnrollment(request.Context(), input.ID, func(tx *sql.Tx, value *clientcontrol.Enrollment, _ []byte) error {
		if value.ClientSerial != "" {
			if _, err := tx.ExecContext(request.Context(), `UPDATE identities SET revoked_at = COALESCE(revoked_at, ?) WHERE serial = ?`, time.Now().Unix(), value.ClientSerial); err != nil {
				return err
			}
		}
		value.State = "canceled"
		value.Configuration = nil
		return nil
	})
	if err != nil {
		writeClientError(writer, err)
		return
	}
	// Revocation is already durable; now remove any live tunnel immediately.
	if value.ClientSerial != "" {
		if err = service.revokeIdentity(context.WithoutCancel(request.Context()), value.ClientSerial, time.Now()); err != nil {
			writeClientError(writer, err)
			return
		}
	}
	writeClientResult(writer, 204, nil)
}

func (service *Service) handleOwnerClientStatuses(writer http.ResponseWriter, request *http.Request) {
	if !service.clientOwner(writer, request) {
		return
	}
	values, err := service.store.clientStatuses(request.Context())
	if err != nil {
		writeClientError(writer, err)
		return
	}
	service.mu.RLock()
	for i := range values {
		active := service.sessions[values[i].ClientSerial]
		values[i].Connected = active != nil && active.registered
	}
	service.mu.RUnlock()
	writeClientResult(writer, 200, values)
}
