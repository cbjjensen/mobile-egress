// Package prerequisites checks desktop runtime requirements before Wails starts.
package prerequisites

import (
	"errors"
	"strings"
)

const webView2InstallMessage = "Microsoft Edge WebView2 Runtime is required; install the Evergreen Standalone Runtime and start Mobile Egress again"

func CheckWebView2(detector func() (string, error)) error {
	if detector == nil {
		return errors.New(webView2InstallMessage)
	}
	version, err := detector()
	if err != nil || strings.TrimSpace(version) == "" {
		return errors.New(webView2InstallMessage)
	}
	return nil
}
