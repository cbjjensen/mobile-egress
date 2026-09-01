//go:build !darwin

package adminservice

import "errors"

var errStatePlatformUnavailable = errors.New("native relay state protection is unavailable on this platform")

func newPlatformStateFilesystem() (stateFilesystem, error) {
	return nil, errStatePlatformUnavailable
}

func newPlatformACLInspector() pathACLInspector {
	return unavailablePathACLInspector{}
}
