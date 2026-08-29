package prerequisites

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckWebView2ReturnsClearPrerequisiteErrorWhenRuntimeIsMissing(t *testing.T) {
	t.Parallel()

	for _, detector := range []func() (string, error){
		func() (string, error) { return "", nil },
		func() (string, error) { return "", errors.New("loader failure") },
	} {
		err := CheckWebView2(detector)
		if err == nil {
			t.Fatal("CheckWebView2 accepted a missing runtime")
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "webview2") || !strings.Contains(message, "install") {
			t.Fatalf("prerequisite error is unclear: %q", err)
		}
	}
}

func TestCheckWebView2AcceptsInstalledRuntime(t *testing.T) {
	t.Parallel()

	if err := CheckWebView2(func() (string, error) { return "140.0.3485.54", nil }); err != nil {
		t.Fatal(err)
	}
}
