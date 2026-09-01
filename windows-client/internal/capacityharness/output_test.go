//go:build capacityharness

package capacityharness

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONEmitterWritesOnlyAllowlistedBoundedFields(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	emitter := NewJSONEmitter(&output)
	event := Event{Phase: PhaseVerify, Attempted: 256, Open: 256, Verified: 256, Closed: 0, Failure: FailureNone}
	if err := emitter.Emit(event); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 6 {
		t.Fatalf("output fields = %#v, want exactly six allowlisted fields", decoded)
	}
	for _, key := range []string{"phase", "attempted", "open", "verified", "closed", "failure"} {
		if _, exists := decoded[key]; !exists {
			t.Fatalf("output is missing allowlisted field %q", key)
		}
	}
}

func TestJSONEmitterRejectsNonAllowlistedOrUnboundedEventsWithoutWriting(t *testing.T) {
	t.Parallel()

	for _, event := range []Event{
		{Phase: Phase("SECRET-PHASE"), Failure: FailureNone},
		{Phase: PhaseVerify, Attempted: -1, Failure: FailureNone},
		{Phase: PhaseVerify, Attempted: maxReportedCount + 1, Failure: FailureNone},
		{Phase: PhaseVerify, Failure: FailureCategory("SECRET-ERROR")},
	} {
		var output bytes.Buffer
		if err := NewJSONEmitter(&output).Emit(event); err == nil {
			t.Fatalf("Emit(%#v) succeeded", event)
		}
		if output.Len() != 0 || strings.Contains(output.String(), "SECRET") {
			t.Fatalf("Emit(%#v) wrote %q", event, output.String())
		}
	}
}
