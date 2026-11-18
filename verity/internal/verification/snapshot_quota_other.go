//go:build !darwin && !linux

package verification

import (
	"context"
)

// Other platforms do not run the rootless Linux container verifier. Keep the
// portable snapshot APIs available without claiming cross-process quota.
func acquireSnapshotSlot(context.Context) (func(), error) {
	return func() {}, nil
}
