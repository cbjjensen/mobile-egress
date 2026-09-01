//go:build !darwin

package adminservice

func NewDarwinStatePathGuard() (PreparedPathGuard, error) {
	return nil, errStatePlatformUnavailable
}
