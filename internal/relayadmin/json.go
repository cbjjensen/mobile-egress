package relayadmin

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"unicode/utf8"
)

var (
	requestEnvelopeFields  = fieldSet("version", "requestId", "operation", "params")
	responseEnvelopeFields = fieldSet("version", "requestId", "operation", "ok", "result", "errorCode")
	setupRequestFields     = fieldSet("publicName", "publicUrl", "ownerCsrPem")
	rotateRequestFields    = fieldSet("publicName", "publicUrl")
	statusResultFields     = fieldSet("protocolVersion", "helperVersion", "initialized", "relayRunning")
	ownerResultFields      = fieldSet("certificatePem", "caCertificatePem", "serial", "role")
	endpointResultFields   = fieldSet("publicUrl", "serial")
	repairResultFields     = fieldSet("ready", "restarting")
)

type parsedObject struct {
	values   map[string]json.RawMessage
	counts   map[string]int
	complete bool
}

func fieldSet(fields ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		set[field] = struct{}{}
	}
	return set
}

func parseStrictObject(raw []byte, allowed map[string]struct{}) (parsedObject, error) {
	object := parsedObject{
		values: make(map[string]json.RawMessage, len(allowed)),
		counts: make(map[string]int, len(allowed)),
	}
	if !utf8.Valid(raw) {
		return object, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return object, ErrInvalidRequest
	}
	invalid := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return object, ErrInvalidRequest
		}
		key, ok := token.(string)
		if !ok {
			return object, ErrInvalidRequest
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return object, ErrInvalidRequest
		}
		object.counts[key]++
		if _, ok := allowed[key]; !ok || object.counts[key] != 1 {
			invalid = true
			continue
		}
		object.values[key] = append(json.RawMessage(nil), value...)
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return object, ErrInvalidRequest
	}
	object.complete = true
	if _, err := decoder.Token(); err != io.EOF {
		invalid = true
	}
	if invalid {
		return object, ErrInvalidRequest
	}
	return object, nil
}

func requireFields(object parsedObject, required ...string) error {
	for _, field := range required {
		if object.counts[field] != 1 {
			return ErrInvalidRequest
		}
	}
	return nil
}

func decodeRequired[T any](raw json.RawMessage) (T, error) {
	var zero T
	var value *T
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return zero, ErrInvalidRequest
	}
	return *value, nil
}

func MarshalRequest(requestID string, operation Operation, params any) ([]byte, error) {
	if ValidateRequestID(requestID) != nil || !operation.Valid() {
		return nil, ErrInvalidRequest
	}
	normalized, err := normalizeRequestParams(operation, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version   int       `json:"version"`
		RequestID string    `json:"requestId"`
		Operation Operation `json:"operation"`
		Params    any       `json:"params"`
	}{Version: Version, RequestID: requestID, Operation: operation, Params: normalized})
}

func ParseRequest(raw []byte) (Request, error) {
	object, objectErr := parseStrictObject(raw, requestEnvelopeFields)
	request := Request{}
	if object.complete {
		if requestID, err := decodeRequired[string](object.values["requestId"]); object.counts["requestId"] == 1 && err == nil && ValidateRequestID(requestID) == nil {
			request.RequestID = requestID
		}
		if operation, err := decodeRequired[string](object.values["operation"]); object.counts["operation"] == 1 && err == nil {
			request.Operation = Operation(operation)
		}
	}
	if objectErr != nil || requireFields(object, "version", "requestId", "operation", "params") != nil {
		return request, newProtocolError(ErrorInvalidRequest)
	}

	version, versionErr := decodeRequired[int](object.values["version"])
	requestID, requestIDErr := decodeRequired[string](object.values["requestId"])
	operationText, operationErr := decodeRequired[string](object.values["operation"])
	if versionErr != nil || requestIDErr != nil || operationErr != nil || ValidateRequestID(requestID) != nil {
		request.RequestID = ""
		return request, newProtocolError(ErrorInvalidRequest)
	}
	request.Version = version
	request.RequestID = requestID
	request.Operation = Operation(operationText)
	if version != Version {
		return request, newProtocolError(ErrorUnsupportedVersion)
	}
	if !request.Operation.Valid() {
		return request, newProtocolError(ErrorUnsupportedOperation)
	}

	params, err := parseRequestParams(request.Operation, object.values["params"])
	if err != nil {
		return request, newProtocolError(ErrorInvalidRequest)
	}
	request.Params = params
	canonical, err := MarshalRequest(request.RequestID, request.Operation, request.Params)
	if err != nil {
		return request, newProtocolError(ErrorInvalidRequest)
	}
	request.Digest = sha256.Sum256(canonical)
	return request, nil
}

func normalizeRequestParams(operation Operation, params any) (any, error) {
	switch operation {
	case OperationStatus:
		switch params.(type) {
		case StatusRequest, *StatusRequest:
			return StatusRequest{}, nil
		}
	case OperationSetup:
		switch value := params.(type) {
		case SetupRequest:
			if !validUTF8Strings(value.PublicName, value.PublicURL, value.OwnerCSRPEM) {
				return nil, ErrInvalidRequest
			}
			return value, nil
		case *SetupRequest:
			if value != nil && validUTF8Strings(value.PublicName, value.PublicURL, value.OwnerCSRPEM) {
				return *value, nil
			}
		}
	case OperationRotate:
		switch value := params.(type) {
		case RotateRequest:
			if !validUTF8Strings(value.PublicName, value.PublicURL) {
				return nil, ErrInvalidRequest
			}
			return value, nil
		case *RotateRequest:
			if value != nil && validUTF8Strings(value.PublicName, value.PublicURL) {
				return *value, nil
			}
		}
	case OperationRepair:
		switch params.(type) {
		case RepairRequest, *RepairRequest:
			return RepairRequest{}, nil
		}
	}
	return nil, ErrInvalidRequest
}

func validUTF8Strings(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return true
}

func parseRequestParams(operation Operation, raw json.RawMessage) (any, error) {
	switch operation {
	case OperationStatus:
		if err := parseEmptyObject(raw); err != nil {
			return nil, err
		}
		return StatusRequest{}, nil
	case OperationSetup:
		object, err := parseStrictObject(raw, setupRequestFields)
		if err != nil || requireFields(object, "publicName", "publicUrl", "ownerCsrPem") != nil {
			return nil, ErrInvalidRequest
		}
		publicName, errName := decodeRequired[string](object.values["publicName"])
		publicURL, errURL := decodeRequired[string](object.values["publicUrl"])
		csr, errCSR := decodeRequired[string](object.values["ownerCsrPem"])
		if errName != nil || errURL != nil || errCSR != nil {
			return nil, ErrInvalidRequest
		}
		return SetupRequest{PublicName: publicName, PublicURL: publicURL, OwnerCSRPEM: csr}, nil
	case OperationRotate:
		object, err := parseStrictObject(raw, rotateRequestFields)
		if err != nil || requireFields(object, "publicName", "publicUrl") != nil {
			return nil, ErrInvalidRequest
		}
		publicName, errName := decodeRequired[string](object.values["publicName"])
		publicURL, errURL := decodeRequired[string](object.values["publicUrl"])
		if errName != nil || errURL != nil {
			return nil, ErrInvalidRequest
		}
		return RotateRequest{PublicName: publicName, PublicURL: publicURL}, nil
	case OperationRepair:
		if err := parseEmptyObject(raw); err != nil {
			return nil, err
		}
		return RepairRequest{}, nil
	default:
		return nil, ErrInvalidRequest
	}
}

func parseEmptyObject(raw json.RawMessage) error {
	object, err := parseStrictObject(raw, map[string]struct{}{})
	if err != nil || len(object.counts) != 0 {
		return ErrInvalidRequest
	}
	return nil
}

func MarshalSuccessResponse(requestID string, operation Operation, result any) ([]byte, error) {
	if ValidateRequestID(requestID) != nil || !operation.Valid() {
		return nil, ErrInvalidResponse
	}
	normalized, err := normalizeResponseResult(operation, result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version   int       `json:"version"`
		RequestID string    `json:"requestId"`
		Operation Operation `json:"operation"`
		OK        bool      `json:"ok"`
		Result    any       `json:"result"`
	}{Version: Version, RequestID: requestID, Operation: operation, OK: true, Result: normalized})
}

func MarshalErrorResponse(requestID string, operation Operation, code ErrorCode) ([]byte, error) {
	if ValidateRequestID(requestID) != nil || !operation.Valid() || !code.Valid() {
		return nil, ErrInvalidResponse
	}
	return marshalErrorResponseUnchecked(requestID, operation, code)
}

func marshalErrorResponseUnchecked(requestID string, operation Operation, code ErrorCode) ([]byte, error) {
	if ValidateRequestID(requestID) != nil || !code.Valid() {
		return nil, ErrInvalidResponse
	}
	return json.Marshal(struct {
		Version   int       `json:"version"`
		RequestID string    `json:"requestId"`
		Operation Operation `json:"operation"`
		OK        bool      `json:"ok"`
		ErrorCode ErrorCode `json:"errorCode"`
	}{Version: Version, RequestID: requestID, Operation: operation, OK: false, ErrorCode: code})
}

func ParseResponse(raw []byte) (Response, error) {
	object, err := parseStrictObject(raw, responseEnvelopeFields)
	if err != nil || requireFields(object, "version", "requestId", "operation", "ok") != nil {
		return Response{}, ErrInvalidResponse
	}
	version, versionErr := decodeRequired[int](object.values["version"])
	requestID, requestIDErr := decodeRequired[string](object.values["requestId"])
	operationText, operationErr := decodeRequired[string](object.values["operation"])
	ok, okErr := decodeRequired[bool](object.values["ok"])
	operation := Operation(operationText)
	if versionErr != nil || requestIDErr != nil || operationErr != nil || okErr != nil ||
		version != Version || ValidateRequestID(requestID) != nil || !operation.Valid() {
		return Response{}, ErrInvalidResponse
	}
	response := Response{Version: version, RequestID: requestID, Operation: operation, OK: ok}
	if ok {
		if object.counts["result"] != 1 || object.counts["errorCode"] != 0 {
			return Response{}, ErrInvalidResponse
		}
		result, err := parseResponseResult(operation, object.values["result"])
		if err != nil {
			return Response{}, ErrInvalidResponse
		}
		response.Result = result
		return response, nil
	}
	if object.counts["errorCode"] != 1 || object.counts["result"] != 0 {
		return Response{}, ErrInvalidResponse
	}
	code, err := decodeRequired[ErrorCode](object.values["errorCode"])
	if err != nil || !code.Valid() {
		return Response{}, ErrInvalidResponse
	}
	response.ErrorCode = code
	return response, nil
}

func normalizeResponseResult(operation Operation, result any) (any, error) {
	switch operation {
	case OperationStatus:
		switch value := result.(type) {
		case StatusResult:
			if !validUTF8Strings(value.HelperVersion) {
				return nil, ErrInvalidResponse
			}
			return value, nil
		case *StatusResult:
			if value != nil && validUTF8Strings(value.HelperVersion) {
				return *value, nil
			}
		}
	case OperationSetup:
		switch value := result.(type) {
		case OwnerBootstrapResult:
			if !validUTF8Strings(value.CertificatePEM, value.CACertificatePEM, value.Serial, value.Role) {
				return nil, ErrInvalidResponse
			}
			return value, nil
		case *OwnerBootstrapResult:
			if value != nil && validUTF8Strings(value.CertificatePEM, value.CACertificatePEM, value.Serial, value.Role) {
				return *value, nil
			}
		}
	case OperationRotate:
		switch value := result.(type) {
		case EndpointRotationResult:
			if !validUTF8Strings(value.PublicURL, value.Serial) {
				return nil, ErrInvalidResponse
			}
			return value, nil
		case *EndpointRotationResult:
			if value != nil && validUTF8Strings(value.PublicURL, value.Serial) {
				return *value, nil
			}
		}
	case OperationRepair:
		switch value := result.(type) {
		case RepairResult:
			return value, nil
		case *RepairResult:
			if value != nil {
				return *value, nil
			}
		}
	}
	return nil, ErrInvalidResponse
}

func parseResponseResult(operation Operation, raw json.RawMessage) (any, error) {
	switch operation {
	case OperationStatus:
		object, err := parseStrictObject(raw, statusResultFields)
		if err != nil || requireFields(object, "protocolVersion", "helperVersion", "initialized", "relayRunning") != nil {
			return nil, ErrInvalidResponse
		}
		protocolVersion, errProtocol := decodeRequired[int](object.values["protocolVersion"])
		helperVersion, errHelper := decodeRequired[string](object.values["helperVersion"])
		initialized, errInitialized := decodeRequired[bool](object.values["initialized"])
		relayRunning, errRunning := decodeRequired[bool](object.values["relayRunning"])
		if errProtocol != nil || errHelper != nil || errInitialized != nil || errRunning != nil {
			return nil, ErrInvalidResponse
		}
		return StatusResult{ProtocolVersion: protocolVersion, HelperVersion: helperVersion, Initialized: initialized, RelayRunning: relayRunning}, nil
	case OperationSetup:
		object, err := parseStrictObject(raw, ownerResultFields)
		if err != nil || requireFields(object, "certificatePem", "caCertificatePem", "serial", "role") != nil {
			return nil, ErrInvalidResponse
		}
		certificate, errCertificate := decodeRequired[string](object.values["certificatePem"])
		caCertificate, errCA := decodeRequired[string](object.values["caCertificatePem"])
		serial, errSerial := decodeRequired[string](object.values["serial"])
		role, errRole := decodeRequired[string](object.values["role"])
		if errCertificate != nil || errCA != nil || errSerial != nil || errRole != nil {
			return nil, ErrInvalidResponse
		}
		return OwnerBootstrapResult{CertificatePEM: certificate, CACertificatePEM: caCertificate, Serial: serial, Role: role}, nil
	case OperationRotate:
		object, err := parseStrictObject(raw, endpointResultFields)
		if err != nil || requireFields(object, "publicUrl", "serial") != nil {
			return nil, ErrInvalidResponse
		}
		publicURL, errURL := decodeRequired[string](object.values["publicUrl"])
		serial, errSerial := decodeRequired[string](object.values["serial"])
		if errURL != nil || errSerial != nil {
			return nil, ErrInvalidResponse
		}
		return EndpointRotationResult{PublicURL: publicURL, Serial: serial}, nil
	case OperationRepair:
		object, err := parseStrictObject(raw, repairResultFields)
		if err != nil || requireFields(object, "ready", "restarting") != nil {
			return nil, ErrInvalidResponse
		}
		ready, errReady := decodeRequired[bool](object.values["ready"])
		restarting, errRestarting := decodeRequired[bool](object.values["restarting"])
		if errReady != nil || errRestarting != nil {
			return nil, ErrInvalidResponse
		}
		return RepairResult{Ready: ready, Restarting: restarting}, nil
	default:
		return nil, ErrInvalidResponse
	}
}
