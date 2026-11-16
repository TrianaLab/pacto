package contract

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUndeclaredContract is returned by RequireDeclared for a published or
// cluster-supplied contract that leaves its pactoVersion, service.name or
// service.version to be implied. Publishing always declares all three.
var ErrUndeclaredContract = errors.New("contract does not declare its identity")

// RequireDeclared passes only when raw's root mapping declares pactoVersion and
// c declares service.name and service.version. Readers of published bundles and
// of contracts supplied in a cluster call it, so the relaxed parser never hands
// them a nameless, versionless or version-floating contract.
func RequireDeclared(c *Contract, raw []byte) error {
	var missing []string
	var root yaml.Node
	_ = yaml.Unmarshal(raw, &root) // c parsed from raw: a decode error cannot happen here
	if root.Kind != yaml.DocumentNode || root.Content[0].Kind != yaml.MappingNode || !hasKey(root.Content[0], "pactoVersion") {
		missing = append(missing, "pactoVersion")
	}
	if c.Service.Name == "" {
		missing = append(missing, "service.name")
	}
	if c.Service.Version == "" {
		missing = append(missing, "service.version")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s not declared", ErrUndeclaredContract, strings.Join(missing, ", "))
	}
	return nil
}
