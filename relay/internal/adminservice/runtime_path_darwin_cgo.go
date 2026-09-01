//go:build darwin && cgo

package adminservice

import "path/filepath"

func NewDarwinStatePathGuard() (PreparedPathGuard, error) {
	return newNativeStatePathGuard(nativeStatePathGuardConfig{
		ProductDir: filepath.Dir(DarwinRelayStateDir),
		StateDir:   DarwinRelayStateDir,
		TrustedAncestors: []string{
			"/",
			"/Library",
			"/Library/Application Support",
		},
	})
}
