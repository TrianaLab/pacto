package workloadmanager

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"

	"gopkg.in/yaml.v3"
)

// External names a manager an installation configured: a tool Pacto knows
// only from a fingerprint the installation wrote down, never from code.
const External = "external"

// Fingerprint names a product by one thing it leaves on what it manages: a
// managedFields manager, a label key or an annotation key on a workload, or,
// for a promoter only, a CRD (<plural>.<group>) the cluster serves.
type Fingerprint struct {
	Product      string `yaml:"product"`
	FieldManager string `yaml:"fieldManager,omitempty"`
	Label        string `yaml:"label,omitempty"`
	Annotation   string `yaml:"annotation,omitempty"`
	CRD          string `yaml:"crd,omitempty"`
}

// Fingerprints are an installation's open list of workload managers and
// promoters. Products live here as data, so core names no vendor.
type Fingerprints struct {
	Managers  []Fingerprint `yaml:"managers"`
	Promoters []Fingerprint `yaml:"promoters"`
}

// MaxFingerprints bounds the entries of one Fingerprints, both lists together.
const MaxFingerprints = 64

// maxFingerprintsBytes bounds the file ParseFingerprints reads.
const maxFingerprintsBytes = 64 << 10

// ParseFingerprints decodes a Fingerprints file (YAML or JSON) strictly:
// unknown fields, a second document, more than MaxFingerprints entries, an
// entry with no product or with other than exactly one matcher, a CRD on a
// manager, a field manager on a promoter and a value sanitize would change
// are refused. Empty input is no fingerprint.
func ParseFingerprints(r io.Reader) (Fingerprints, error) {
	var fp Fingerprints
	data, err := io.ReadAll(io.LimitReader(r, maxFingerprintsBytes+1))
	switch {
	case err != nil:
		return fp, err
	case len(data) > maxFingerprintsBytes:
		return fp, fmt.Errorf("fingerprints exceed %d bytes", maxFingerprintsBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&fp); err != nil && !errors.Is(err, io.EOF) {
		return Fingerprints{}, fmt.Errorf("decoding fingerprints: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Fingerprints{}, errors.New("fingerprints hold more than one document")
	}
	if n := len(fp.Managers) + len(fp.Promoters); n > MaxFingerprints {
		return Fingerprints{}, fmt.Errorf("%d fingerprints exceed the bound of %d", n, MaxFingerprints)
	}
	for i, f := range fp.Managers {
		if err := f.check(false); err != nil {
			return Fingerprints{}, fmt.Errorf("managers[%d]: %w", i, err)
		}
	}
	for i, f := range fp.Promoters {
		if err := f.check(true); err != nil {
			return Fingerprints{}, fmt.Errorf("promoters[%d]: %w", i, err)
		}
	}
	return fp, nil
}

func (f Fingerprint) check(promoter bool) error {
	set := 0
	for _, v := range []string{f.FieldManager, f.Label, f.Annotation, f.CRD} {
		if v != "" {
			set++
		}
		if sanitize(v) != v {
			return fmt.Errorf("value %q is not printable ASCII of at most %d bytes", sanitize(v), maxValue)
		}
	}
	switch {
	case f.Product == "" || sanitize(f.Product) != f.Product:
		return errors.New("product must be printable ASCII of at most 253 bytes")
	case set != 1:
		return fmt.Errorf("product %s: set exactly one of fieldManager, label, annotation or crd", f.Product)
	case f.CRD != "" && !promoter:
		return fmt.Errorf("product %s: crd names a promoter, never a workload manager", f.Product)
	case f.FieldManager != "" && promoter:
		return fmt.Errorf("product %s: a promoter is named by a crd, label or annotation", f.Product)
	}
	return nil
}

// Match reports whether f's field manager, label or annotation is on o and
// what it found. A CRD fingerprint never matches a workload.
func (f Fingerprint) Match(o Object) (string, bool) {
	_, label := o.Labels[f.Label]
	_, annotation := o.Annotations[f.Annotation]
	switch {
	case f.FieldManager != "" && manager(o, func(n string) bool { return n == f.FieldManager }) != "":
		return "field manager " + f.FieldManager, true
	case f.Label != "" && label:
		return "label " + f.Label, true
	case f.Annotation != "" && annotation:
		return "annotation " + f.Annotation, true
	}
	return "", false
}

// Detector detects managers with an installation's own fingerprints added to
// the strong signals. The zero Detector is the package-level Detect.
type Detector struct {
	Extra []Fingerprint
}

// Detect names the manager of o. A configured fingerprint on o names an
// External manager on its own; beside any other strong signal it reads
// ambiguous, like two built-in ones.
func (d Detector) Detect(o Object) Manager {
	var hits []Manager
	for _, f := range strong {
		if m, ok := f(o); ok {
			hits = append(hits, m)
		}
	}
	// Flux's helm-controller installs through Helm: the release annotations
	// under its labels are the same chain.
	if slices.ContainsFunc(hits, func(m Manager) bool { return m.Name == FluxHelmRelease }) {
		hits = slices.DeleteFunc(hits, func(m Manager) bool { return m.Name == Helm })
	}
	for _, f := range d.Extra {
		ev, ok := f.Match(o)
		if ok && !slices.ContainsFunc(hits, func(m Manager) bool { return m.Name == External && m.Product == f.Product }) {
			hits = append(hits, Manager{Name: External, Product: f.Product, Evidence: ev})
		}
	}
	return pickManager(hits, o)
}
