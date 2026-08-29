package service

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"mobile-egress/relay/internal/enrollment"
)

const maxControlRequestBytes = 256 << 10

type enrollRequest struct {
	Code         string          `json:"code"`
	Role         enrollment.Role `json:"role"`
	CSRPEM       string          `json:"csrPem,omitempty"`
	PublicKeyPEM string          `json:"publicKeyPem,omitempty"`
}

type enrollResponse struct {
	CertificatePEM   string          `json:"certificatePem"`
	CACertificatePEM string          `json:"caCertificatePem"`
	Serial           string          `json:"serial"`
	Role             enrollment.Role `json:"role"`
}

type pairingRequest struct {
	Role enrollment.Role `json:"role"`
}

type pairingResponse struct {
	Code      string          `json:"code"`
	Role      enrollment.Role `json:"role"`
	ExpiresAt string          `json:"expiresAt"`
}

type revokeRequest struct {
	Serial string `json:"serial"`
}

func (service *Service) handleEnroll(writer http.ResponseWriter, request *http.Request) {
	var input enrollRequest
	if err := decodeControlJSON(request.Body, &input); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	if !validEnrollmentRole(input.Role) || strings.TrimSpace(input.Code) == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	publicKey, err := parseDevicePublicKey(input.CSRPEM, input.PublicKeyPEM)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_public_key")
		return
	}

	now := time.Now().UTC()
	serialNumber, err := randomSerial()
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	serial := strings.ToUpper(serialNumber.Text(16))
	certificatePEM, err := service.signDeviceCertificate(publicKey, input.Role, serialNumber, serial, now)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_public_key")
		return
	}
	capabilityHash := sha256.Sum256([]byte(input.Code))
	if err := service.store.redeemCapabilityAndCreateIdentity(request.Context(), capabilityHash, input.Role, serial, now); err != nil {
		switch {
		case errors.Is(err, errCapabilityRole):
			writeAPIError(writer, http.StatusForbidden, "role_mismatch")
		case errors.Is(err, errCapabilityInvalid), errors.Is(err, errCapabilityExpired):
			writeAPIError(writer, http.StatusUnauthorized, "invalid_capability")
		default:
			writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		}
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(enrollResponse{
		CertificatePEM:   string(certificatePEM) + string(service.caCertPEM),
		CACertificatePEM: string(service.caCertPEM),
		Serial:           serial,
		Role:             input.Role,
	})
}

func (service *Service) handlePairing(writer http.ResponseWriter, request *http.Request) {
	_, role, status := service.authenticateRequest(request)
	if status != 0 {
		writeAPIError(writer, status, authErrorCode(status))
		return
	}
	if role != enrollment.RoleOwner {
		writeAPIError(writer, http.StatusForbidden, "owner_required")
		return
	}

	var input pairingRequest
	if err := decodeControlJSON(request.Body, &input); err != nil || (input.Role != enrollment.RoleAgent && input.Role != enrollment.RoleClient) {
		writeAPIError(writer, http.StatusBadRequest, "invalid_role")
		return
	}
	code, hash, err := newCapability()
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(capabilityLifetime)
	if err := service.store.insertCapability(request.Context(), hash, input.Role, now, expiresAt); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(pairingResponse{Code: code, Role: input.Role, ExpiresAt: expiresAt.Format(time.RFC3339)})
}

func (service *Service) handleRevoke(writer http.ResponseWriter, request *http.Request) {
	_, role, status := service.authenticateRequest(request)
	if status != 0 {
		writeAPIError(writer, status, authErrorCode(status))
		return
	}
	if role != enrollment.RoleOwner {
		writeAPIError(writer, http.StatusForbidden, "owner_required")
		return
	}
	var input revokeRequest
	if err := decodeControlJSON(request.Body, &input); err != nil || !validSerial(input.Serial) {
		writeAPIError(writer, http.StatusBadRequest, "invalid_serial")
		return
	}
	serial := strings.ToUpper(input.Serial)
	if err := service.revokeIdentity(request.Context(), serial, time.Now().UTC()); err != nil {
		if errors.Is(err, errIdentityNotFound) {
			writeAPIError(writer, http.StatusNotFound, "identity_not_found")
		} else {
			writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		}
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (service *Service) authenticateRequest(request *http.Request) (string, enrollment.Role, int) {
	if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.VerifiedChains[0]) == 0 {
		return "", "", http.StatusUnauthorized
	}
	leaf := request.TLS.VerifiedChains[0][0]
	serial := strings.ToUpper(leaf.SerialNumber.Text(16))
	role, revoked, err := service.store.identityStatus(request.Context(), serial)
	if err != nil || revoked {
		return "", "", http.StatusUnauthorized
	}
	_ = service.store.touchIdentity(request.Context(), serial, time.Now().UTC())
	return serial, role, 0
}

func (service *Service) signDeviceCertificate(publicKey crypto.PublicKey, role enrollment.Role, serialNumber *big.Int, serial string, now time.Time) ([]byte, error) {
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: fmt.Sprintf("mobile-egress-%s-%s", role, serial[:min(12, len(serial))])},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, service.caCert, publicKey, service.caKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), nil
}

func parseDevicePublicKey(csrPEM, publicKeyPEM string) (crypto.PublicKey, error) {
	if (csrPEM == "") == (publicKeyPEM == "") {
		return nil, errors.New("exactly one public key representation is required")
	}
	var publicKey crypto.PublicKey
	if csrPEM != "" {
		block, rest := pem.Decode([]byte(csrPEM))
		if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("invalid certificate request")
		}
		request, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil || request.CheckSignature() != nil {
			return nil, errors.New("invalid certificate request")
		}
		publicKey = request.PublicKey
	} else {
		block, rest := pem.Decode([]byte(publicKeyPEM))
		if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("invalid public key")
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid public key")
		}
		publicKey = parsed
	}
	switch key := publicKey.(type) {
	case *ecdsa.PublicKey:
		if key.Curve == nil || key.X == nil || key.Y == nil || !key.Curve.IsOnCurve(key.X, key.Y) {
			return nil, errors.New("invalid ECDSA public key")
		}
	case *rsa.PublicKey:
		if key.N.BitLen() < 2048 {
			return nil, errors.New("RSA public key is too small")
		}
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid Ed25519 public key")
		}
	default:
		return nil, errors.New("unsupported public key")
	}
	return publicKey, nil
}

func decodeControlJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxControlRequestBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func validEnrollmentRole(role enrollment.Role) bool {
	return role == enrollment.RoleOwner || role == enrollment.RoleAgent || role == enrollment.RoleClient
}

func validSerial(serial string) bool {
	serial = strings.TrimSpace(serial)
	if serial == "" || len(serial) > 64 {
		return false
	}
	for _, character := range serial {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func authErrorCode(status int) string {
	if status == http.StatusForbidden {
		return "forbidden"
	}
	return "unauthorized"
}

func writeAPIError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": code})
}
