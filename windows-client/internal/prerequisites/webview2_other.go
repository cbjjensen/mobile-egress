//go:build !windows

package prerequisites

import "errors"

func CheckWebView2Installed() error {
	return errors.New("the Mobile Egress Windows client requires Windows 10 or 11 and Microsoft Edge WebView2 Runtime")
}
