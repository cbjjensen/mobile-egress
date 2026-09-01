package desktop

import (
	"strings"
	"testing"
)

func TestDarwinFatalMessagesAreFixedAndDoNotIncludeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class   darwinFatalClass
		heading string
		body    string
		stderr  string
	}{
		{
			class:   fatalStartup,
			heading: "ZFNF Mobile Egress could not start.",
			body:    "The desktop controller could not be initialized. Reopen the app and try again.",
			stderr:  "ZFNF Mobile Egress: startup failed.",
		},
		{
			class:   fatalRuntime,
			heading: "ZFNF Mobile Egress stopped unexpectedly.",
			body:    "Reopen the desktop controller and try again.",
			stderr:  "ZFNF Mobile Egress: desktop runtime failed.",
		},
	}
	for _, test := range tests {
		message := darwinFatalMessage(test.class)
		if message.heading != test.heading || message.body != test.body || message.stderr != test.stderr {
			t.Fatalf("darwinFatalMessage(%d) = %#v", test.class, message)
		}
		for _, secret := range []string{"NSError", "/var/run/private.sock", "owner-private-key", "aws-secret"} {
			if strings.Contains(message.heading+message.body+message.stderr, secret) {
				t.Fatalf("fatal message exposed %q", secret)
			}
		}
	}
}
