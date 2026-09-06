//go:build windows

package setup

import (
	"errors"
	"os"
	"path/filepath"
)

func PrepareEmbeddedPayload() (string, func(), error) {
	if len(embeddedPayload) == 0 {
		return "", func() {}, nil
	}
	// Use the protected installation volume, not the user's writable temp parent,
	// so an unelevated process cannot replace the staging directory itself.
	directory, err := os.MkdirTemp(filepath.Dir(filepath.Dir(InstallRoot)), "MobileEgressSetup-")
	if err != nil {
		return "", nil, errors.New("create setup payload staging")
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	if err := restrictRecoveryDirectory(directory); err != nil {
		cleanup()
		return "", nil, errors.New("protect setup payload staging")
	}
	if err := extractPayload(embeddedPayload, directory); err != nil {
		cleanup()
		return "", nil, err
	}
	return directory, cleanup, nil
}
