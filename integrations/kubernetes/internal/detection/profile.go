/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"fmt"
	"slices"
	"strings"
)

// ParseProfile reads the --profile value the chart rendered: comma-separated
// names, trimmed, de-duplicated and sorted. Empty is [default]; an unknown name
// and default beside another name are errors, as the chart refuses them.
func ParseProfile(s string) ([]string, error) {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "":
			continue
		case "default", "hardened", "idp":
		default:
			return nil, fmt.Errorf("profile takes default, hardened or idp: got %s", p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"default"}, nil
	}
	if len(out) > 1 && slices.Contains(out, "default") {
		return nil, fmt.Errorf("profile default stands alone")
	}
	slices.Sort(out)
	return out, nil
}
