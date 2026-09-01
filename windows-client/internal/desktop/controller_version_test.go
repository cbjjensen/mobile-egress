package desktop

import "testing"

func TestControllerVersionReportsLinkedReleaseValue(t *testing.T) {
	previous := controllerVersion
	controllerVersion = "1.1.0"
	t.Cleanup(func() { controllerVersion = previous })
	if got := ControllerVersion(); got != "1.1.0" {
		t.Fatalf("ControllerVersion() = %q, want 1.1.0", got)
	}
}
