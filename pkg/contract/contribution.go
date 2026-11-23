package contract

import (
	"maps"
	"slices"

	"gopkg.in/yaml.v3"
)

// ContributionRefusal is why b, with its inferred record rec (nil when none),
// cannot be a producer's contribution, or "" when it can: a contribution
// declares only service.name and service.version and carries no declaration.
// The push refuses what every reader would.
func ContributionRefusal(b *Bundle, rec *InferredRecord) string {
	if rec != nil && rec.Declaration != nil {
		return "a contribution carries no declaration"
	}
	if field, declares := contributionDeclares(b); declares {
		return "it declares " + field + "; a contribution declares only service.name and service.version: move the rest to pacto.inferred.yaml"
	}
	return ""
}

// contributionDeclares is the first field a contributed pacto.yaml declares
// beyond pactoVersion, service.name and service.version, read from its raw
// keys so a field no Go type knows yet is refused too. An unreadable
// pacto.yaml declares "pacto.yaml": nothing it says can be checked.
func contributionDeclares(b *Bundle) (field string, declares bool) {
	data, err := b.Raw()
	var doc map[string]yaml.Node
	if err != nil || yaml.Unmarshal(data, &doc) != nil {
		return "pacto.yaml", true
	}
	for _, k := range slices.Sorted(maps.Keys(doc)) {
		if k != "pactoVersion" && k != "service" {
			return k, true
		}
	}
	var service map[string]yaml.Node
	if svc, ok := doc["service"]; ok && svc.Decode(&service) != nil {
		return "service", true
	}
	for _, k := range slices.Sorted(maps.Keys(service)) {
		if k != "name" && k != "version" {
			return "service." + k, true
		}
	}
	return "", false
}
