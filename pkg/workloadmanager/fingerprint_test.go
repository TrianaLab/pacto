package workloadmanager

import (
	"errors"
	"strings"
	"testing"
	"testing/iotest"
)

var platform = Detector{Extra: []Fingerprint{
	{Product: "Platform", FieldManager: "platform-applier"},
	{Product: "Platform", Label: "platform.example.com/app"},
	{Product: "Portal", Annotation: "portal.example.com/owner"},
}}

func TestConfiguredManagerFingerprintIsExternal(t *testing.T) {
	check(t, platform.Detect(with(nil, nil, "platform-applier")), Manager{Name: External, Product: "Platform", Evidence: "field manager platform-applier"})
	check(t, platform.Detect(with(map[string]string{"platform.example.com/app": "x"}, nil)), Manager{Name: External, Product: "Platform", Evidence: "label platform.example.com/app"})
	check(t, platform.Detect(with(nil, map[string]string{"portal.example.com/owner": "x"})), Manager{Name: External, Product: "Portal", Evidence: "annotation portal.example.com/owner"})
	// Two marks of one product are one manager.
	check(t, platform.Detect(with(map[string]string{"platform.example.com/app": "x"}, nil, "platform-applier")),
		Manager{Name: External, Product: "Platform", Evidence: "field manager platform-applier"})
	// A status write is no manager, and a CRD fingerprint never matches a workload.
	o := deploy()
	o.Managers = []FieldManager{{Manager: "platform-applier", Subresource: "status"}}
	check(t, platform.Detect(o), Manager{Name: None})
	check(t, Detector{Extra: []Fingerprint{{Product: "P", CRD: "stages.example.com"}}}.Detect(deploy()), Manager{Name: None})
	if got := Describe(platform.Detect(with(nil, nil, "platform-applier"))); got != "external: Platform (field manager platform-applier)" {
		t.Errorf("Describe = %q", got)
	}
}

func TestConfiguredFingerprintAmbiguousWithArgo(t *testing.T) {
	o := tracking("payments:apps/Deployment:payments/payments")
	o.Managers = []FieldManager{{Manager: "platform-applier"}}
	check(t, platform.Detect(o), Manager{Name: Ambiguous, Evidence: "Argo CD tracking annotation and Platform field manager platform-applier"})
	check(t, platform.Detect(with(map[string]string{"platform.example.com/app": "x"}, map[string]string{"portal.example.com/owner": "x"})),
		Manager{Name: Ambiguous, Evidence: "Platform label platform.example.com/app and Portal annotation portal.example.com/owner"})
}

func TestDetectUnchangedWithoutFingerprints(t *testing.T) {
	for _, o := range []Object{
		deploy(), tracking("payments:apps/Deployment:payments/payments"), tracking("bad"),
		with(map[string]string{kustomizeName: "a", kustomizeNamespace: "b"}, nil),
		with(map[string]string{helmReleaseName: "a", helmReleaseNamespace: "b"}, map[string]string{helmName: "a", helmNamespace: "b"}),
		with(nil, nil, "helm"), with(nil, nil, "Terraform"), with(map[string]string{instanceLabel: "x"}, nil, "argocd-controller"),
		with(map[string]string{"kapp.k14s.io/app": "1"}, nil),
	} {
		check(t, Detector{}.Detect(o), Detect(o))
		check(t, platform.Detect(o), Detect(o))
	}
}

func TestFingerprintsDecodeBounds(t *testing.T) {
	good := "managers:\n- product: Platform\n  fieldManager: platform-applier\npromoters:\n- product: Promoter\n  crd: stages.example.com\n- product: Updater\n  annotation: updater.example.com/images\n"
	fp, err := ParseFingerprints(strings.NewReader(good))
	if err != nil || len(fp.Managers) != 1 || len(fp.Promoters) != 2 || fp.Promoters[0].CRD != "stages.example.com" {
		t.Fatalf("good: %+v %v", fp, err)
	}
	if fp, err := ParseFingerprints(strings.NewReader(`{"managers":[{"product":"P","label":"l"}],"promoters":null}`)); err != nil || len(fp.Managers) != 1 || fp.Promoters != nil {
		t.Errorf("json: %+v %v", fp, err)
	}
	if fp, err := ParseFingerprints(strings.NewReader("")); err != nil || len(fp.Managers)+len(fp.Promoters) != 0 {
		t.Errorf("empty: %+v %v", fp, err)
	}
	many := "managers:\n" + strings.Repeat("- {product: P, label: l}\n", MaxFingerprints+1)
	for name, in := range map[string]string{
		"unknown field":        "managers:\n- product: P\n  label: l\n  value: x\n",
		"two documents":        "managers: []\n---\nmanagers: []\n",
		"not yaml":             "managers: [",
		"too many":             many,
		"no product":           "managers:\n- label: l\n",
		"unprintable product":  "managers:\n- product: \"P\\u00e9\"\n  label: l\n",
		"no matcher":           "managers:\n- product: P\n",
		"two matchers":         "managers:\n- product: P\n  label: l\n  annotation: a\n",
		"crd on a manager":     "managers:\n- product: P\n  crd: a.b\n",
		"manager on promoter":  "promoters:\n- product: P\n  fieldManager: m\n",
		"unprintable value":    "managers:\n- product: P\n  label: \"l\\tx\"\n",
		"too long value":       "managers:\n- product: P\n  label: " + strings.Repeat("l", 254) + "\n",
		"oversized":            strings.Repeat("#", maxFingerprintsBytes+1),
		"bad promoter matcher": "promoters:\n- product: P\n",
	} {
		if _, err := ParseFingerprints(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseFingerprints(iotest.ErrReader(errors.New("boom"))); err == nil {
		t.Error("read error accepted")
	}
}
