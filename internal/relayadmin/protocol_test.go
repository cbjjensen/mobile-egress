package relayadmin

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const testRequestID = "00112233445566778899aabbccddeeff"

func TestRequestIDIsExactly128BitCanonicalLowerHex(t *testing.T) {
	t.Parallel()

	got, err := GenerateRequestID(bytes.NewReader([]byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
	}))
	if err != nil {
		t.Fatalf("GenerateRequestID() returned an error: %v", err)
	}
	if got != testRequestID {
		t.Fatalf("GenerateRequestID() = %q, want %q", got, testRequestID)
	}

	for _, invalid := range []string{
		"00112233445566778899aabbccddee",
		"00112233445566778899aabbccddeeff00",
		"00112233445566778899AABBCCDDEEFF",
		"00112233-4455-6677-8899-aabbccddeeff",
		" 00112233445566778899aabbccddeeff",
		"00112233445566778899aabbccddeefg",
	} {
		if err := ValidateRequestID(invalid); err == nil {
			t.Errorf("ValidateRequestID(%q) accepted a noncanonical ID", invalid)
		}
	}
	if err := ValidateRequestID(testRequestID); err != nil {
		t.Fatalf("ValidateRequestID() rejected the canonical ID: %v", err)
	}
}

func TestRequestIDGenerationDoesNotExposeEntropySourceErrors(t *testing.T) {
	t.Parallel()

	_, err := GenerateRequestID(failingReader{err: errors.New("secret entropy provider path")})
	if !errors.Is(err, ErrRequestIDGeneration) {
		t.Fatalf("GenerateRequestID() error = %v, want ErrRequestIDGeneration", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "path") {
		t.Fatalf("GenerateRequestID() exposed raw entropy error: %v", err)
	}
}

func TestProtocolV1RoundTripsOnlyStatusSetupRotateRepair(t *testing.T) {
	t.Parallel()

	tests := []struct {
		operation Operation
		params    any
		result    any
	}{
		{
			operation: OperationStatus,
			params:    StatusRequest{},
			result: StatusResult{
				ProtocolVersion: Version,
				HelperVersion:   "1.2.3",
				Initialized:     true,
				RelayRunning:    true,
			},
		},
		{
			operation: OperationSetup,
			params: SetupRequest{
				PublicName:  "relay.example.ts.net",
				PublicURL:   "https://relay.example.ts.net",
				OwnerCSRPEM: "-----BEGIN CERTIFICATE REQUEST-----\npublic\n-----END CERTIFICATE REQUEST-----\n",
			},
			result: OwnerBootstrapResult{
				CertificatePEM:   "owner-certificate",
				CACertificatePEM: "ca-certificate",
				Serial:           "1234",
				Role:             "owner",
			},
		},
		{
			operation: OperationRotate,
			params: RotateRequest{
				PublicName: "relay-2.example.ts.net",
				PublicURL:  "https://relay-2.example.ts.net",
			},
			result: EndpointRotationResult{PublicURL: "https://relay-2.example.ts.net", Serial: "5678"},
		},
		{
			operation: OperationRepair,
			params:    RepairRequest{},
			result:    RepairResult{Ready: false, Restarting: true},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(string(test.operation), func(t *testing.T) {
			t.Parallel()

			rawRequest, err := MarshalRequest(testRequestID, test.operation, test.params)
			if err != nil {
				t.Fatalf("MarshalRequest() returned an error: %v", err)
			}
			request, err := ParseRequest(rawRequest)
			if err != nil {
				t.Fatalf("ParseRequest() returned an error: %v", err)
			}
			if request.Version != Version || request.RequestID != testRequestID || request.Operation != test.operation {
				t.Fatalf("ParseRequest() correlation = %#v", request)
			}
			if !reflect.DeepEqual(request.Params, test.params) {
				t.Fatalf("ParseRequest() params = %#v, want %#v", request.Params, test.params)
			}

			rawResponse, err := MarshalSuccessResponse(testRequestID, test.operation, test.result)
			if err != nil {
				t.Fatalf("MarshalSuccessResponse() returned an error: %v", err)
			}
			response, err := ParseResponse(rawResponse)
			if err != nil {
				t.Fatalf("ParseResponse() returned an error: %v", err)
			}
			if !response.OK || response.RequestID != testRequestID || response.Operation != test.operation {
				t.Fatalf("ParseResponse() correlation = %#v", response)
			}
			if !reflect.DeepEqual(response.Result, test.result) {
				t.Fatalf("ParseResponse() result = %#v, want %#v", response.Result, test.result)
			}

			rawError, err := MarshalErrorResponse(testRequestID, test.operation, ErrorUnauthorized)
			if err != nil {
				t.Fatalf("MarshalErrorResponse() returned an error: %v", err)
			}
			errorResponse, err := ParseResponse(rawError)
			if err != nil {
				t.Fatalf("ParseResponse(error) returned an error: %v", err)
			}
			if errorResponse.OK || errorResponse.ErrorCode != ErrorUnauthorized || errorResponse.Result != nil {
				t.Fatalf("ParseResponse(error) = %#v", errorResponse)
			}
		})
	}

	if _, err := MarshalRequest(testRequestID, Operation("delete"), StatusRequest{}); err == nil {
		t.Fatal("MarshalRequest() accepted a fifth operation")
	}
}

func TestStrictRequestRejectsUnknownDuplicateAlternateCaseMissingNullWrongTypeAndTrailingFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown envelope", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"status","params":{},"extra":true}`},
		{name: "duplicate envelope", raw: `{"version":1,"version":1,"requestId":"` + testRequestID + `","operation":"status","params":{}}`},
		{name: "alternate case", raw: `{"Version":1,"requestId":"` + testRequestID + `","operation":"status","params":{}}`},
		{name: "missing params", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"status"}`},
		{name: "null request ID", raw: `{"version":1,"requestId":null,"operation":"status","params":{}}`},
		{name: "wrong version type", raw: `{"version":"1","requestId":"` + testRequestID + `","operation":"status","params":{}}`},
		{name: "trailing value", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"status","params":{}}null`},
		{name: "status params field", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"status","params":{"ready":true}}`},
		{name: "repair params field", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"repair","params":{"ready":true}}`},
		{name: "setup unknown", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"setup","params":{"publicName":"name","publicUrl":"url","ownerCsrPem":"csr","privateKey":"secret"}}`},
		{name: "setup duplicate", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"setup","params":{"publicName":"name","publicName":"other","publicUrl":"url","ownerCsrPem":"csr"}}`},
		{name: "setup missing", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"setup","params":{"publicName":"name","publicUrl":"url"}}`},
		{name: "setup null", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"setup","params":{"publicName":"name","publicUrl":"url","ownerCsrPem":null}}`},
		{name: "setup wrong type", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"setup","params":{"publicName":"name","publicUrl":3,"ownerCsrPem":"csr"}}`},
		{name: "rotate alternate case", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"rotate","params":{"PublicName":"name","publicUrl":"url"}}`},
		{name: "rotate duplicate", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"rotate","params":{"publicName":"name","publicUrl":"url","publicUrl":"other"}}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseRequest([]byte(test.raw)); protocolErrorCode(err) != ErrorInvalidRequest {
				t.Fatalf("ParseRequest() error = %v, want %q", err, ErrorInvalidRequest)
			}
		})
	}
}

func TestStrictResponseRejectsUnknownDuplicateAlternateCaseMissingNullWrongTypeAndTrailingFields(t *testing.T) {
	t.Parallel()

	validStatus := `{"version":1,"requestId":"` + testRequestID + `","operation":"status","ok":true,"result":{"protocolVersion":1,"helperVersion":"dev","initialized":true,"relayRunning":true}}`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown envelope", raw: strings.TrimSuffix(validStatus, "}") + `,"message":"secret"}`},
		{name: "duplicate envelope", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"status","ok":true,"ok":false,"result":{"protocolVersion":1,"helperVersion":"dev","initialized":true,"relayRunning":true}}`},
		{name: "alternate case", raw: strings.Replace(validStatus, `"requestId"`, `"RequestId"`, 1)},
		{name: "missing ok", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"status","result":{"protocolVersion":1,"helperVersion":"dev","initialized":true,"relayRunning":true}}`},
		{name: "null result", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"status","ok":true,"result":null}`},
		{name: "wrong ok type", raw: strings.Replace(validStatus, `"ok":true`, `"ok":"true"`, 1)},
		{name: "trailing value", raw: validStatus + `[]`},
		{name: "both result and error", raw: strings.TrimSuffix(validStatus, "}") + `,"errorCode":"unavailable"}`},
		{name: "success missing result", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"repair","ok":true}`},
		{name: "error missing code", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"repair","ok":false}`},
		{name: "error with result", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"repair","ok":false,"result":{"ready":true,"restarting":false},"errorCode":"unavailable"}`},
		{name: "status result unknown", raw: strings.Replace(validStatus, `"relayRunning":true`, `"relayRunning":true,"uid":501`, 1)},
		{name: "status result duplicate", raw: strings.Replace(validStatus, `"helperVersion":"dev"`, `"helperVersion":"dev","helperVersion":"other"`, 1)},
		{name: "status result missing", raw: strings.Replace(validStatus, `,"relayRunning":true`, "", 1)},
		{name: "status result null", raw: strings.Replace(validStatus, `"helperVersion":"dev"`, `"helperVersion":null`, 1)},
		{name: "status result wrong type", raw: strings.Replace(validStatus, `"initialized":true`, `"initialized":1`, 1)},
		{name: "setup result invalid", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"setup","ok":true,"result":{"certificatePem":"owner","caCertificatePem":"ca","serial":"1","role":"owner","path":"/secret"}}`},
		{name: "rotate result invalid", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"rotate","ok":true,"result":{"publicUrl":"url","serial":"1","serial":"2"}}`},
		{name: "repair result invalid", raw: `{"version":1,"requestId":"` + testRequestID + `","operation":"repair","ok":true,"result":{"ready":true,"restarting":null}}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseResponse([]byte(test.raw)); err == nil {
				t.Fatalf("ParseResponse() accepted %s", test.name)
			}
		})
	}
}

func TestStrictProtocolRejectsInvalidUTF8InsteadOfNormalizingIt(t *testing.T) {
	t.Parallel()

	request := []byte(`{"version":1,"requestId":"` + testRequestID + `","operation":"setup","params":{"publicName":"`)
	request = append(request, 0xff)
	request = append(request, []byte(`","publicUrl":"url","ownerCsrPem":"csr"}}`)...)
	if _, err := ParseRequest(request); err == nil {
		t.Fatal("ParseRequest() normalized invalid UTF-8")
	}

	response := []byte(`{"version":1,"requestId":"` + testRequestID + `","operation":"status","ok":true,"result":{"protocolVersion":1,"helperVersion":"`)
	response = append(response, 0xff)
	response = append(response, []byte(`","initialized":true,"relayRunning":true}}`)...)
	if _, err := ParseResponse(response); err == nil {
		t.Fatal("ParseResponse() normalized invalid UTF-8")
	}
}

func TestStrictProtocolMarshalRejectsInvalidUTF8TypedFields(t *testing.T) {
	t.Parallel()

	invalid := string([]byte{0xff})
	if _, err := MarshalRequest(testRequestID, OperationSetup, SetupRequest{
		PublicName: invalid, PublicURL: "url", OwnerCSRPEM: "csr",
	}); err == nil {
		t.Fatal("MarshalRequest() normalized invalid UTF-8")
	}
	if _, err := MarshalSuccessResponse(testRequestID, OperationStatus, StatusResult{
		ProtocolVersion: 1, HelperVersion: invalid,
	}); err == nil {
		t.Fatal("MarshalSuccessResponse() normalized invalid UTF-8")
	}
}

func TestProtocolClassifiesUnsupportedVersionOperationAndNonAllowlistedError(t *testing.T) {
	t.Parallel()

	unsupportedVersion := `{"version":2,"requestId":"` + testRequestID + `","operation":"status","params":{}}`
	if request, err := ParseRequest([]byte(unsupportedVersion)); request.RequestID != testRequestID || protocolErrorCode(err) != ErrorUnsupportedVersion {
		t.Fatalf("ParseRequest(version) = (%#v, %v)", request, err)
	}

	unsupportedOperation := `{"version":1,"requestId":"` + testRequestID + `","operation":"delete","params":{}}`
	if request, err := ParseRequest([]byte(unsupportedOperation)); request.RequestID != testRequestID || protocolErrorCode(err) != ErrorUnsupportedOperation {
		t.Fatalf("ParseRequest(operation) = (%#v, %v)", request, err)
	}

	nonAllowlisted := `{"version":1,"requestId":"` + testRequestID + `","operation":"status","ok":false,"errorCode":"raw_daemon_error"}`
	if _, err := ParseResponse([]byte(nonAllowlisted)); err == nil {
		t.Fatal("ParseResponse() accepted a non-allowlisted error code")
	}

	for _, code := range []ErrorCode{
		ErrorInvalidRequest,
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
		ErrorUnavailable,
	} {
		if !code.Valid() {
			t.Errorf("ErrorCode(%q).Valid() = false", code)
		}
	}
	if ErrorCode("raw_daemon_error").Valid() {
		t.Fatal("unknown error code is allowlisted")
	}
}

func TestCanonicalRequestDigestIgnoresInputObjectKeyOrder(t *testing.T) {
	t.Parallel()

	first := `{"version":1,"requestId":"` + testRequestID + `","operation":"setup","params":{"publicName":"name","publicUrl":"url","ownerCsrPem":"csr"}}`
	second := `{"params":{"ownerCsrPem":"csr","publicUrl":"url","publicName":"name"},"operation":"setup","requestId":"` + testRequestID + `","version":1}`
	requestA, err := ParseRequest([]byte(first))
	if err != nil {
		t.Fatalf("ParseRequest(first) returned an error: %v", err)
	}
	requestB, err := ParseRequest([]byte(second))
	if err != nil {
		t.Fatalf("ParseRequest(second) returned an error: %v", err)
	}
	if requestA.Digest != requestB.Digest {
		t.Fatalf("canonical digests differ: %x != %x", requestA.Digest, requestB.Digest)
	}
}

func TestProtocolNeverSerializesPrivateOrRawErrorFields(t *testing.T) {
	t.Parallel()

	results := []struct {
		operation Operation
		result    any
	}{
		{OperationStatus, StatusResult{ProtocolVersion: 1, HelperVersion: "dev", Initialized: true, RelayRunning: true}},
		{OperationSetup, OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}},
		{OperationRotate, EndpointRotationResult{PublicURL: "https://relay.example", Serial: "2"}},
		{OperationRepair, RepairResult{Ready: true, Restarting: false}},
	}
	for _, item := range results {
		raw, err := MarshalSuccessResponse(testRequestID, item.operation, item.result)
		if err != nil {
			t.Fatalf("MarshalSuccessResponse(%s) returned an error: %v", item.operation, err)
		}
		assertNoSecretSchemaNames(t, raw)
	}
	raw, err := MarshalErrorResponse(testRequestID, OperationRepair, ErrorOperationFailed)
	if err != nil {
		t.Fatalf("MarshalErrorResponse() returned an error: %v", err)
	}
	assertNoSecretSchemaNames(t, raw)
	if strings.Contains(string(raw), "boom /Library/private stderr") {
		t.Fatal("error response serialized raw error text")
	}
}

func TestValidatedCarrierTypesCannotBypassStrictMarshalWithArbitraryJSON(t *testing.T) {
	t.Parallel()

	requestRaw, err := json.Marshal(Request{
		Version:   Version,
		RequestID: testRequestID,
		Operation: OperationSetup,
		Params:    map[string]any{"ownerPrivateKey": "secret"},
	})
	if !errors.Is(err, ErrStrictMarshalRequired) {
		t.Fatalf("json.Marshal(Request) = (%q, %v), want ErrStrictMarshalRequired", requestRaw, err)
	}
	responseRaw, err := json.Marshal(Response{
		Version:   Version,
		RequestID: testRequestID,
		Operation: OperationSetup,
		OK:        true,
		Result:    map[string]any{"stderr": "secret"},
	})
	if !errors.Is(err, ErrStrictMarshalRequired) {
		t.Fatalf("json.Marshal(Response) = (%q, %v), want ErrStrictMarshalRequired", responseRaw, err)
	}
	if strings.Contains(string(requestRaw), "ownerPrivateKey") || strings.Contains(string(requestRaw), "secret") {
		t.Fatalf("Request default JSON bypassed the strict schema: %s", requestRaw)
	}
	if strings.Contains(string(responseRaw), "stderr") || strings.Contains(string(responseRaw), "secret") {
		t.Fatalf("Response default JSON bypassed the strict schema: %s", responseRaw)
	}
}

func protocolErrorCode(err error) ErrorCode {
	var protocolError *ProtocolError
	if errors.As(err, &protocolError) {
		return protocolError.Code
	}
	return ""
}

func assertNoSecretSchemaNames(t *testing.T, raw []byte) {
	t.Helper()
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{
		"privatekey", "private_key", "aws", "metadata", "message", "details", "headers", "payload", "stderr", "filesystem", `"path"`, `"uid"`, `"gid"`,
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("public response schema %q contains forbidden name %q", raw, forbidden)
		}
	}
}

type failingReader struct{ err error }

func (reader failingReader) Read([]byte) (int, error) { return 0, reader.err }
