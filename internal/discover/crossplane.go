package discover

import (
	"strings"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// xpObject is an object a catalog may name as a managed resource kind
// (Crossplane, for one): its API group, kind, name, external-name
// annotation, spec.forProvider.region, connection Secret name, file and line.
// These fixed fields are all the scan keeps of it.
type xpObject struct {
	group, kind, name, externalName, region, connSecret, file string
	line                                                      int
}

// crossplaneCandidate is the object doc when its API group is dotted and it
// is no ConfigMap or Secret: the scan records it before any catalog is known,
// and crossplaneUses keeps the ones a catalog names. A workload is never one.
func crossplaneCandidate(group, kind, name string, doc map[string]any) (xpObject, bool) {
	if !strings.Contains(group, ".") || kind == "ConfigMap" || kind == "Secret" {
		return xpObject{}, false
	}
	o := xpObject{group: group, kind: kind, name: name}
	o.externalName, _ = Dig(doc, "metadata", "annotations", "crossplane.io/external-name").(string)
	o.region, _ = Dig(doc, "spec", "forProvider", "region").(string)
	o.connSecret, _ = Dig(doc, "spec", "writeConnectionSecretToRef", "name").(string)
	return o, true
}

// crossplaneUses are the managed cloud services the recorded objects whose
// group and kind cat lists provision. The id is the external-name
// annotation, else the object's name. The account lives in the provider
// configuration, so it is always missing. An object is likely only when the
// workload reads its connection Secret, else possible.
func (sc *Scan) crossplaneUses(cat *externalcatalog.Catalog) []External {
	var out []External
	for _, o := range sc.xpObjects {
		m, ok := cat.MatchCrossplane(o.group, o.kind)
		if !ok {
			continue
		}
		if o.connSecret != "" && sc.readsSecret(o.connSecret) {
			m.Confidence = externalcatalog.ConfidenceLikely
		}
		x, ok := managedExternal(m, managedSource{kind: "crossplane", path: o.file, line: o.line, key: o.kind + "." + o.name,
			typ: o.group + "/" + o.kind, ids: []string{o.externalName, o.name}, region: o.region})
		if ok {
			out = append(out, x)
		}
	}
	return out
}

// readsSecret reports whether the workload's environment reads the Secret
// name, through a secretKeyRef or an envFrom secretRef.
func (sc *Scan) readsSecret(name string) bool {
	if sc.Workload == nil {
		return false
	}
	for _, e := range sc.Workload.Env {
		if e.From == "secret" && e.Ref == name {
			return true
		}
	}
	return false
}
