package contract

import (
	"fmt"
	"maps"
	"slices"

	"gopkg.in/yaml.v3"
)

// ContributionRefusal is why b, with its inferred record rec (nil when none),
// cannot be a producer's contribution, or "" when it can: a contribution
// declares only service.name and service.version, plus an offering section
// when it declares service.offering, and carries no declaration. The push
// refuses what every reader would.
func ContributionRefusal(b *Bundle, rec *InferredRecord) string {
	if rec != nil && rec.Declaration != nil {
		return "a contribution carries no declaration"
	}
	if field, declares := contributionDeclares(b); declares {
		return "it declares " + field + "; a contribution declares only service.name and service.version " +
			"(with service.offering also service.summary and configurations of name, schema and required): move the rest to pacto.inferred.yaml"
	}
	// An accepted offering's inferredFrom names the contribution's service,
	// which must be the offering it accepts: an offering contribution is
	// named after its offering.
	var doc struct {
		Service struct{ Name, Offering string } `yaml:"service"`
	}
	if data, err := b.Raw(); err == nil && yaml.Unmarshal(data, &doc) == nil && doc.Service.Offering != "" && doc.Service.Name != doc.Service.Offering {
		return "its service.name " + doc.Service.Name + " is not its service.offering " + doc.Service.Offering + ": an offering contribution is named after its offering"
	}
	return ""
}

// contributionDeclares is the first field a contributed pacto.yaml declares
// beyond pactoVersion, service.name and service.version, read from its raw
// keys so a field no Go type knows yet is refused too. A pacto.yaml whose
// service.offering is a non-empty string may also declare service.offering,
// service.summary and configurations, each entry holding only name, schema
// and required: the offering section an inferred offering carries. An
// unreadable pacto.yaml declares "pacto.yaml": nothing it says can be
// checked.
func contributionDeclares(b *Bundle) (field string, declares bool) {
	data, err := b.Raw()
	var doc map[string]yaml.Node
	if err != nil || yaml.Unmarshal(data, &doc) != nil {
		return "pacto.yaml", true
	}
	var service map[string]yaml.Node
	if svc, ok := doc["service"]; ok && svc.Decode(&service) != nil {
		return "service", true
	}
	offering := service["offering"]
	isOffering := offering.Kind == yaml.ScalarNode && offering.Tag == "!!str" && offering.Value != ""
	top, svcKeys := []string{"pactoVersion", "service"}, []string{"name", "version"}
	if isOffering {
		top, svcKeys = append(top, "configurations"), append(svcKeys, "offering", "summary")
	}
	for _, k := range slices.Sorted(maps.Keys(doc)) {
		if !slices.Contains(top, k) {
			return k, true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(service)) {
		if !slices.Contains(svcKeys, k) {
			return "service." + k, true
		}
	}
	if cfgs, ok := doc["configurations"]; ok {
		return configurationsDeclare(cfgs)
	}
	return "", false
}

// configurationsDeclare is the first field an offering contribution's
// configurations declares beyond a sequence of mappings holding name, schema
// and required.
func configurationsDeclare(cfgs yaml.Node) (field string, declares bool) {
	var entries []map[string]yaml.Node
	if cfgs.Decode(&entries) != nil {
		return "configurations", true
	}
	for i, e := range entries {
		for _, k := range slices.Sorted(maps.Keys(e)) {
			if k != "name" && k != "schema" && k != "required" {
				return fmt.Sprintf("configurations[%d].%s", i, k), true
			}
		}
	}
	return "", false
}
