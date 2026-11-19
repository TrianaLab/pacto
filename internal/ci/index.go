package ci

import (
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// IndexText says where an impact answer's consumers came from in the
// organization index, the text after "Consumers:" in pacto impact and
// "Index:" in pacto ci; "" when no index was involved (a pair of local
// folders). It says complete only when the listing finished and a target
// source checked it.
func IndexText(res *impact.Result) string {
	ix := res.ConsumerIndex
	if ix == nil {
		return ""
	}
	switch ix.Status {
	case impact.IndexNotFound:
		return ix.Reason + " (partial)"
	case impact.IndexUnavailable:
		return "the organization index " + ix.Repository + " could not be listed: " + ix.Reason + " (partial)"
	case impact.IndexSkipped:
		return "organization index not read (" + ix.Reason + ")"
	}
	how := "complete"
	switch {
	case !ix.Checked:
		how = strconv.Itoa(ix.Services) + " " + plural(ix.Services, "entry", "entries") + " read, unchecked"
	case ix.Completeness != fleet.CompletenessComplete:
		how = "partial: " + indexReason(res.Limitations)
	}
	if ix.Reason != "" {
		how += ", " + ix.Reason
	}
	return strconv.Itoa(ix.Consumers) + " from " + ix.Repository + " (listed by tag, " + how + ")"
}

// indexReason is the first limitation the index read or its cross-check
// added.
func indexReason(ls []fleet.Limitation) string {
	for _, l := range ls {
		if l.Source == "index" || l.Code == impact.LimitationConsumerNotIndexed {
			return strings.TrimSuffix(l.Message, ".")
		}
	}
	return "see the limitations"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
