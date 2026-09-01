//go:build darwin && !cgo

package adminservice

func NewDarwinStatePathGuard() (PreparedPathGuard, error) {
	return nil, errStateACLUnavailable
}
