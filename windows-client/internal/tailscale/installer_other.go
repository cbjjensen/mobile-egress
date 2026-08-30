//go:build !windows

package tailscale

import "errors"

func DefaultInstaller() Installer {
	return Installer{
		VerifyAuthenticode: func(string) (Signature, error) {
			return Signature{}, errors.New("Authenticode is only available on Windows")
		},
		ElevatedInstall: func(string) error { return errors.New("MSI installation is only available on Windows") },
	}
}
