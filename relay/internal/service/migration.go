package service

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"mobile-egress/relay/internal/enrollment"
)

const endpointMigrationVersion = 1

type endpointMigrationIssueRequest struct{}

type endpointMigrationIssueResponse struct {
	Version          int    `json:"version"`
	Type             string `json:"type"`
	RelayURL         string `json:"relayUrl"`
	CACertificatePEM string `json:"caCertificatePem"`
	Capability       string `json:"capability"`
	ExpiresAt        string `json:"expiresAt"`
}

type endpointMigrationConsumeRequest struct {
	Capability string `json:"capability"`
}

type endpointMigrationConsumeResponse struct {
	RelayURL string `json:"relayUrl"`
}

func (service *Service) handleIssueEndpointMigration(writer http.ResponseWriter, request *http.Request) {
	_, role, status := service.authenticateRequest(request)
	if status != 0 {
		writeAPIError(writer, status, authErrorCode(status))
		return
	}
	if role != enrollment.RoleOwner {
		writeAPIError(writer, http.StatusForbidden, "owner_required")
		return
	}
	var input endpointMigrationIssueRequest
	if err := decodeControlJSON(request.Body, &input); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	relayURL, err := service.store.relayURL(request.Context())
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	capability, hash, err := newCapability()
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(capabilityLifetime)
	if err := service.store.insertEndpointMigration(request.Context(), hash, relayURL, now, expiresAt); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(endpointMigrationIssueResponse{
		Version: endpointMigrationVersion, Type: "agent-endpoint-migration", RelayURL: relayURL,
		CACertificatePEM: string(service.caCertPEM), Capability: capability, ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

func (service *Service) handleConsumeEndpointMigration(writer http.ResponseWriter, request *http.Request) {
	_, role, status := service.authenticateRequest(request)
	if status != 0 {
		writeAPIError(writer, status, authErrorCode(status))
		return
	}
	if role != enrollment.RoleAgent {
		writeAPIError(writer, http.StatusForbidden, "agent_required")
		return
	}
	var input endpointMigrationConsumeRequest
	if err := decodeControlJSON(request.Body, &input); err != nil || strings.TrimSpace(input.Capability) == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	hash := sha256.Sum256([]byte(input.Capability))
	relayURL, err := service.store.consumeEndpointMigration(request.Context(), hash, time.Now().UTC())
	if err != nil {
		if errors.Is(err, errCapabilityInvalid) || errors.Is(err, errCapabilityExpired) {
			writeAPIError(writer, http.StatusUnauthorized, "invalid_capability")
		} else {
			writeAPIError(writer, http.StatusInternalServerError, "internal_error")
		}
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(endpointMigrationConsumeResponse{RelayURL: relayURL})
}
