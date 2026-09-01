//go:build !windows

package securestore

var _ Store = (*DPAPIStore)(nil)
