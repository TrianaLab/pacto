package backstage

import (
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// EntityRef splits the Backstage entity reference ref, [kind:][namespace/]name,
// into its kind, lowercased, and its name, which keeps a namespace other than
// default.
func EntityRef(ref string) (kind, name string) {
	kind, name, ok := strings.Cut(ref, ":")
	if !ok {
		kind, name = "", ref
	}
	return strings.ToLower(kind), strings.TrimPrefix(name, "default/")
}

// OwnerFromRef reads the owner reference ref: a group, the default kind, is a
// team and a user is a person. Any other kind, or a name the contract does not
// accept, is no owner.
func OwnerFromRef(ref string) (contract.Owner, bool) {
	kind, name := EntityRef(ref)
	switch {
	case (kind == "" || kind == "group") && contract.ValidTeamName(name):
		return contract.Owner{Team: name}, true
	case kind == "user" && contract.ValidDRIName(name):
		return contract.Owner{DRI: name}, true
	}
	return contract.Owner{}, false
}

// DomainFromRef reads the system reference ref as a business domain, made a
// slug; one that is still not a domain is none.
func DomainFromRef(ref string) (string, bool) {
	_, name := EntityRef(ref)
	domain := contract.SanitizeName(name)
	if !contract.ValidSlug(domain) {
		return "", false
	}
	return domain, true
}
