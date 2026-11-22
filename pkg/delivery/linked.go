package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// The linked targets: the workloads the operator linked to a published
// contract with no Pacto, written by its linker as one more key of the
// detection ConfigMap and read by every fleet reader.
const (
	// LinkedSchemaVersion is the schemaVersion of [LinkedTargets].
	LinkedSchemaVersion = "delivery.pacto.trianalab.io/linked-targets/v1"
	// LinkedConfigMapKey is the detection ConfigMap key the linker writes.
	LinkedConfigMapKey = "linked.json"
	// LinkerFieldManager is the server-side-apply field manager of the
	// linker. A reader trusts LinkedConfigMapKey only when this manager owns
	// it.
	LinkerFieldManager = "pacto-linker"
	// LinkedStaleAfter is the age past which linked targets read stale: three
	// missed link cycles.
	LinkedStaleAfter = 3 * time.Minute
	// MaxLinkedBytes bounds one encoded [LinkedTargets], written or read, so
	// it fits the ConfigMap beside the detection key.
	MaxLinkedBytes = 640 << 10
)

// The labels a linked target's link rides in. A label map is free text on a
// Pacto view, so released readers read a linked target unchanged and only
// [LinkedCollection] reads these.
const (
	LinkedByLabel            = "pacto.trianalab.io/linked-by"
	LinkedValueLabel         = "pacto.trianalab.io/linked-value"
	LinkedSinceLabel         = "pacto.trianalab.io/linked-since"
	LinkedOtherVersionsLabel = "pacto.trianalab.io/linked-other-versions"
	linkLabelPrefix          = "pacto.trianalab.io/linked-"
)

// LinkedTargets is delivery.pacto.trianalab.io/linked-targets/v1: the
// workloads one cluster's linker linked, each a Pacto view named
// <Kind>/<name> whose link rides in the Linked*Label labels.
type LinkedTargets struct {
	SchemaVersion string             `json:"schemaVersion"`
	Cluster       string             `json:"cluster"`
	AsOf          string             `json:"asOf"`
	Completeness  fleet.Completeness `json:"completeness"`
	Limitations   []fleet.Limitation `json:"limitations,omitempty"`
	Targets       []PactoView        `json:"targets"`
}

// LinkLabels are the labels that carry l on a linked target's view.
func LinkLabels(l fleet.ObservedLink) map[string]string {
	out := map[string]string{LinkedByLabel: l.Signal, LinkedValueLabel: l.Value}
	if l.Since != nil {
		out[LinkedSinceLabel] = l.Since.UTC().Format(time.RFC3339)
	}
	if len(l.OtherVersions) > 0 {
		out[LinkedOtherVersionsLabel] = strings.Join(l.OtherVersions, ",")
	}
	return out
}

// EncodeLinked encodes l, refusing a payload over [MaxLinkedBytes].
func EncodeLinked(l LinkedTargets) ([]byte, error) {
	b, _ := json.Marshal(l) // plain data; marshalling cannot fail
	if len(b) > MaxLinkedBytes {
		return nil, fmt.Errorf("the linked targets are %d bytes, over the %d-byte bound", len(b), MaxLinkedBytes)
	}
	return b, nil
}

// DecodeLinked decodes linked targets strictly: within the bound, of
// [LinkedSchemaVersion], with an RFC 3339 asOf and a known completeness.
func DecodeLinked(b []byte) (LinkedTargets, error) {
	var l LinkedTargets
	if len(b) > MaxLinkedBytes {
		return l, fmt.Errorf("the linked targets are %d bytes, over the %d-byte bound", len(b), MaxLinkedBytes)
	}
	if err := strictjson.Unmarshal(b, &l); err != nil {
		return LinkedTargets{}, fmt.Errorf("the linked targets: %w", err)
	}
	if l.SchemaVersion != LinkedSchemaVersion {
		return LinkedTargets{}, fmt.Errorf("the linked targets have schemaVersion %q, want %s", l.SchemaVersion, LinkedSchemaVersion)
	}
	if err := checkRead(l.AsOf, l.Completeness); err != nil {
		return LinkedTargets{}, fmt.Errorf("the linked targets: %w", err)
	}
	return l, nil
}

// LinkedCollection is [PactoCollection] over views a linker wrote: a view
// named <Kind>/<name> is a linked target whose link labels become its
// [fleet.RawTarget.Link]. Views named like a Pacto keep the Pacto rules.
func LinkedCollection(source string, views []PactoView, now time.Time) *fleet.Collection {
	return pactoCollection(source, views, nil, now, true)
}

// linkedName reports whether a view's name is a linked target's <Kind>/<name>:
// a Pacto name cannot hold "/".
func linkedName(p PactoView) bool { return strings.Contains(p.Metadata.Name, "/") }

var errNoLinkedBy = errors.New("no " + LinkedByLabel + " label")

// viewLink is the link of view p as source reads it. A linked target (linked
// set and a <Kind>/<name> name) yields its link, or drop with the reason its
// labels are invalid. Any other view carrying a link label keeps being a Pacto
// target with the labels ignored, and lim says so: only the linker writes
// them.
func viewLink(source string, p PactoView, linked bool) (link *fleet.ObservedLink, lim *fleet.Limitation, drop bool) {
	at := p.Metadata.Namespace + "/" + p.Metadata.Name
	if !linked || !linkedName(p) {
		if hasLinkLabel(p.Metadata.Labels) {
			lim = &fleet.Limitation{Code: fleet.LimitationSourceRecordInvalid, Source: source,
				Message: "Pacto " + at + " carries " + linkLabelPrefix + "* labels, which only the operator's linker writes; they were ignored"}
		}
		return nil, lim, false
	}
	link, err := parseLink(p.Metadata.Labels)
	if err != nil {
		return nil, &fleet.Limitation{Code: fleet.LimitationSourceRecordInvalid, Source: source,
			Message: "linked target " + at + " was not used: " + err.Error()}, true
	}
	return link, nil, false
}

// parseLink reads the link labels: a known signal and, when present, an
// RFC 3339 linked-since.
func parseLink(labels map[string]string) (*fleet.ObservedLink, error) {
	l := &fleet.ObservedLink{Signal: labels[LinkedByLabel], Value: labels[LinkedValueLabel]}
	switch l.Signal {
	case fleet.LinkSignalImageDigest, fleet.LinkSignalAnnotation:
	case "":
		return nil, errNoLinkedBy
	default:
		return nil, fmt.Errorf("unknown %s %q", LinkedByLabel, l.Signal)
	}
	if s, ok := labels[LinkedSinceLabel]; ok {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("%s %q is not RFC 3339", LinkedSinceLabel, s)
		}
		l.Since = &t
	}
	for _, v := range strings.Split(labels[LinkedOtherVersionsLabel], ",") {
		if v != "" {
			l.OtherVersions = append(l.OtherVersions, v)
		}
	}
	return l, nil
}

// hasLinkLabel reports whether labels hold any link label.
func hasLinkLabel(labels map[string]string) bool {
	for k := range labels {
		if strings.HasPrefix(k, linkLabelPrefix) {
			return true
		}
	}
	return false
}
