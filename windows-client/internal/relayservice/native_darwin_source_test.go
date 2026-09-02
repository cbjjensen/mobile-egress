package relayservice

import (
	"os"
	"strings"
	"testing"
)

func TestDarwinServiceManagementAvoidsSynchronousMainThreadDispatch(t *testing.T) {
	source, err := os.ReadFile("native_darwin.m")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "mobileEgressRunOnMainSync") {
		t.Fatal("Darwin Service Management calls synchronously dispatch to the UI thread")
	}
}
