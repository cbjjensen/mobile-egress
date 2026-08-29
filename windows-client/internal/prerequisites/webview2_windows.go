//go:build windows

package prerequisites

import "github.com/wailsapp/go-webview2/webviewloader"

func CheckWebView2Installed() error {
	return CheckWebView2(func() (string, error) {
		return webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	})
}
