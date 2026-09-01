//go:build darwin && !cgo

package adminservice

func newDarwinACLInspector() pathACLInspector {
	return unavailablePathACLInspector{}
}
