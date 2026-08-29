// Package relaytransport exposes the testable relay tunnel transport without
// coupling callers to the desktop UI layer.
package relaytransport

import (
	"context"

	"mobile-egress/windows-client/internal/relayclient"
)

type Identity = relayclient.Identity
type Session = relayclient.Session

func DialSession(ctx context.Context, identity Identity) (*Session, error) {
	return relayclient.DialSession(ctx, identity)
}
