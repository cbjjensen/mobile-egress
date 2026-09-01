//go:build darwin && !cgo

package adminservice

import (
	"context"
	"testing"
)

func TestDarwinNoCGOACLAdapterFailsClosed(t *testing.T) {
	t.Parallel()

	inspector := newDarwinACLInspector()
	if inspector == nil {
		t.Fatal("newDarwinACLInspector() returned nil")
	}
	if err := inspector.Validate(context.Background(), nil, pathACLRejectExtended); err == nil {
		t.Fatal("cgo-disabled Darwin ACL inspection returned usable success")
	}
}
