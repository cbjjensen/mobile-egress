// Package relayadmin implements the portable, strictly typed relay
// administration protocol trust boundary.
package relayadmin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"
)

const (
	Version          = 1
	MaximumFrameSize = 512 << 10
	OperationTimeout = 5 * time.Minute
)

var (
	ErrInvalidRequest      = errors.New("invalid relay admin request")
	ErrInvalidResponse     = errors.New("invalid relay admin response")
	ErrRequestIDGeneration = errors.New("relay admin request ID generation failed")
)

// Operation is one of the four protocol v1 administrative operations.
type Operation string

const (
	OperationStatus Operation = "status"
	OperationSetup  Operation = "setup"
	OperationRotate Operation = "rotate"
	OperationRepair Operation = "repair"
)

func (operation Operation) Valid() bool {
	switch operation {
	case OperationStatus, OperationSetup, OperationRotate, OperationRepair:
		return true
	default:
		return false
	}
}

func (operation Operation) mutation() bool {
	return operation == OperationSetup || operation == OperationRotate || operation == OperationRepair
}

// ErrorCode is a redacted, allowlisted public error classification.
type ErrorCode string

const (
	ErrorInvalidRequest       ErrorCode = "invalid_request"
	ErrorUnsupportedVersion   ErrorCode = "unsupported_version"
	ErrorUnsupportedOperation ErrorCode = "unsupported_operation"
	ErrorUnauthorized         ErrorCode = "unauthorized"
	ErrorDuplicateRequest     ErrorCode = "duplicate_request"
	ErrorBusy                 ErrorCode = "busy"
	ErrorDeadlineExceeded     ErrorCode = "deadline_exceeded"
	ErrorNotInitialized       ErrorCode = "not_initialized"
	ErrorAlreadyInitialized   ErrorCode = "already_initialized"
	ErrorStateIncompatible    ErrorCode = "state_incompatible"
	ErrorOperationFailed      ErrorCode = "operation_failed"
	ErrorUnavailable          ErrorCode = "unavailable"
)

func (code ErrorCode) Valid() bool {
	switch code {
	case ErrorInvalidRequest,
		ErrorUnsupportedVersion,
		ErrorUnsupportedOperation,
		ErrorUnauthorized,
		ErrorDuplicateRequest,
		ErrorBusy,
		ErrorDeadlineExceeded,
		ErrorNotInitialized,
		ErrorAlreadyInitialized,
		ErrorStateIncompatible,
		ErrorOperationFailed,
		ErrorUnavailable:
		return true
	default:
		return false
	}
}

// ProtocolError classifies a request rejection without carrying raw input or
// implementation error text.
type ProtocolError struct {
	Code ErrorCode
}

func (protocolError *ProtocolError) Error() string {
	if protocolError == nil || !protocolError.Code.Valid() {
		return string(ErrorInvalidRequest)
	}
	return string(protocolError.Code)
}

func newProtocolError(code ErrorCode) error {
	return &ProtocolError{Code: code}
}

// PublicError is the only handler or remote error shape exposed across the
// protocol boundary. It deliberately carries no arbitrary message.
type PublicError struct {
	Code ErrorCode
}

func (publicError *PublicError) Error() string {
	if publicError == nil || !publicError.Code.Valid() {
		return string(ErrorOperationFailed)
	}
	return string(publicError.Code)
}

// GenerateRequestID reads exactly 16 random bytes and renders their canonical
// lowercase hexadecimal request identifier. A nil reader uses crypto/rand.
func GenerateRequestID(reader io.Reader) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(reader, bytes); err != nil {
		return "", ErrRequestIDGeneration
	}
	return hex.EncodeToString(bytes), nil
}

// ValidateRequestID accepts only 32 lowercase hexadecimal characters.
func ValidateRequestID(requestID string) error {
	if len(requestID) != 32 {
		return ErrInvalidRequest
	}
	for _, character := range requestID {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return ErrInvalidRequest
		}
	}
	decoded, err := hex.DecodeString(requestID)
	if err != nil || len(decoded) != 16 {
		return ErrInvalidRequest
	}
	return nil
}

type StatusRequest struct{}

type SetupRequest struct {
	PublicName  string `json:"publicName"`
	PublicURL   string `json:"publicUrl"`
	OwnerCSRPEM string `json:"ownerCsrPem"`
}

type RotateRequest struct {
	PublicName string `json:"publicName"`
	PublicURL  string `json:"publicUrl"`
}

type RepairRequest struct{}

type StatusResult struct {
	ProtocolVersion int    `json:"protocolVersion"`
	HelperVersion   string `json:"helperVersion"`
	Initialized     bool   `json:"initialized"`
	RelayRunning    bool   `json:"relayRunning"`
}

type OwnerBootstrapResult struct {
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
	Serial           string `json:"serial"`
	Role             string `json:"role"`
}

type EndpointRotationResult struct {
	PublicURL string `json:"publicUrl"`
	Serial    string `json:"serial"`
}

type RepairResult struct {
	Ready      bool `json:"ready"`
	Restarting bool `json:"restarting"`
}

// Request is a fully validated typed request. Digest is SHA-256 over its
// canonical typed JSON encoding and is independent of input object key order.
type Request struct {
	Version   int               `json:"-"`
	RequestID string            `json:"-"`
	Operation Operation         `json:"-"`
	Params    any               `json:"-"`
	Digest    [sha256.Size]byte `json:"-"`
}

// Response is a fully validated typed success or allowlisted error response.
type Response struct {
	Version   int       `json:"-"`
	RequestID string    `json:"-"`
	Operation Operation `json:"-"`
	OK        bool      `json:"-"`
	Result    any       `json:"-"`
	ErrorCode ErrorCode `json:"-"`
}
