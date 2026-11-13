package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// pinVisibleRef maps a reference an HTTP caller names onto a revision in the
// caller's fleet view and returns that revision's own digest-pinned
// ResolvedRef. The caller's string never reaches a resolver: a ref matches a
// revision by its RequestedRef or ResolvedRef, or by the @sha256: digest it
// carries, so a visible digest on a foreign host still resolves the visible
// revision's registry and repository. A revision with no oci:// ResolvedRef
// (a local source) is refused like a ref that matches nothing.
func pinVisibleRef(snap *fleet.FleetSnapshot, ref string) (string, error) {
	if snap != nil && ref != "" {
		digest := ""
		if i := strings.LastIndex(ref, "@sha256:"); i >= 0 {
			digest = ref[i+1:]
		}
		for _, rev := range snap.Revisions {
			if rev.RequestedRef != ref && rev.ResolvedRef != ref && (digest == "" || rev.Digest != digest) {
				continue
			}
			if strings.HasPrefix(rev.ResolvedRef, contract.OCIScheme) {
				return rev.ResolvedRef, nil
			}
		}
	}
	var asOf time.Time
	if snap != nil {
		asOf = snap.GeneratedAt
	}
	return "", errors.New("is not a revision in your fleet view (asOf " + asOf.UTC().Format(time.RFC3339) +
		"); over HTTP, name a revision you can see, or run pacto impact locally")
}

// pinVisibleRefs pins an old and a new ref with pinVisibleRef, naming the
// argument a refusal is about.
func pinVisibleRefs(snap *fleet.FleetSnapshot, oldRef, newRef, oldName, newName string) (string, string, error) {
	pinnedOld, err := pinVisibleRef(snap, oldRef)
	if err != nil {
		return "", "", fmt.Errorf("%s %w", oldName, err)
	}
	pinnedNew, err := pinVisibleRef(snap, newRef)
	if err != nil {
		return "", "", fmt.Errorf("%s %w", newName, err)
	}
	return pinnedOld, pinnedNew, nil
}
