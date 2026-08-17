package scenario

import (
	"fmt"
	"strconv"
	"strings"
)

// HelmValues is the Kubernetes surface's configuration projection: the chart
// values that come from the SCENARIO, as `key=value` strings a harness turns
// into `--set` arguments.
//
// It exists because there is now a second consumer. The Kind harness used to
// build these three keys inline while walking the plan's observation records,
// which was fine while the cluster was the only surface — but the Compose
// surface configures the SAME sources under different keys, and the parity test
// between them has to read what each surface was actually told, not what both
// were derived from. Two consumers is the rule for a projection existing at all
// (docs/maintainers/testing.md); this now has them.
//
// What is NOT here is deliberate: the operator image, the insecure registry, the
// enabled components and the trust Secret are this RUN's values, with one
// consumer and no counterpart on any other surface. They stay in the harness.
func (s Scenario) HelmValues() ([]string, error) {
	var out []string
	i := 0
	for _, src := range s.Sources {
		if src.Kind != SourceObservation {
			continue
		}
		prefix := "dashboard.observation.sources[" + strconv.Itoa(i) + "]."
		for _, kv := range [][2]string{
			{prefix + "name", src.ID},
			{prefix + "file", observationFileKey},
			{prefix + "configMap", ObservationConfigMap(src.ID)},
		} {
			if err := checkHelmValue(kv[1]); err != nil {
				return nil, fmt.Errorf("scenario %s: %s: %w", s.Name, kv[0], err)
			}
			out = append(out, kv[0]+"="+kv[1])
		}
		i++
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("scenario %s: no observation source to configure the dashboard with", s.Name)
	}
	return out, nil
}

// checkHelmValue refuses a value `helm --set` would not carry verbatim.
//
// `--set` has its own grammar over the VALUE as well as the key: a comma starts
// the next assignment, a backslash escapes, and the bracket/dot pair addresses
// into the structure. A source id containing one would silently configure
// something else — the same forgery risk the plan's delimiter check exists for,
// one layer up. Refusing is honest: no fixture needs such a name, and quietly
// escaping here would make the projection disagree with every other place the id
// appears.
func checkHelmValue(v string) error {
	if v == "" {
		return fmt.Errorf("is empty")
	}
	if i := strings.IndexAny(v, `,=[]\`+"\t\n\r"); i >= 0 {
		return fmt.Errorf("contains %q, which helm --set would not carry verbatim", v[i:i+1])
	}
	return nil
}
