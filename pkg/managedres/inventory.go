package managedres

import (
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Inventoried is the managed resource a cloud inventory row of typ reports
// through source, classified with cat and observed at now: only its type,
// account, region and id are read, the name is the id, and an unread part
// keeps the catalog entry's identity with the part listed in Missing. False
// when no catalog row names typ.
func Inventoried(cat *externalcatalog.Catalog, source, typ, account, region, id string, now time.Time) (fleet.RawManagedResource, bool) {
	m, ok := cat.MatchInventory(typ)
	if !ok {
		return fleet.RawManagedResource{}, false
	}
	_, ref, _ := cat.Entry(m.Identity) // ponytail: a match always names an entry of cat
	r := fleet.RawManagedResource{Source: source, Entry: m.Entry, Catalog: ref.Digest, Type: typ, Name: id, ObservedAt: &now}
	identify(&r, m, strings.ToLower(account), strings.ToLower(region), strings.ToLower(id))
	return r, true
}
