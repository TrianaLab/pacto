package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// semverRE matches a core semver, optionally with a prerelease suffix — the same
// shape apply-release-plan.mjs propagates into every tag / version field.
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

type releasePlan struct {
	Schema string               `json:"schema"`
	Groups map[string]planGroup `json:"groups"`
}

type planGroup struct {
	Version       string         `json:"version"`
	Tags          []string       `json:"tags"`
	GoModPin      *goModPin      `json:"goModPin,omitempty"`
	Compatibility *planCompat    `json:"compatibility,omitempty"`
	Artifacts     []planArtifact `json:"artifacts"`
}

type goModPin struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

type planCompat struct {
	PactoCore string `json:"pactoCore"`
}

type planArtifact struct {
	Unit            string `json:"unit"`
	Kind            string `json:"kind"`
	Coordinate      string `json:"coordinate"`
	Tag             string `json:"tag"`
	Release         string `json:"release"`
	Version         string `json:"version"`
	ChartVersion    string `json:"chartVersion"`
	AppVersion      string `json:"appVersion"`
	DefaultImageTag string `json:"defaultImageTag"`
}

func loadPlan(t *testing.T, root string) releasePlan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "release", "release-plan.json"))
	if err != nil {
		t.Fatalf("read release plan: %v", err)
	}
	var p releasePlan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("parse release plan: %v", err)
	}
	return p
}

func artifactByUnit(g planGroup, unit string) (planArtifact, bool) {
	for _, a := range g.Artifacts {
		if a.Unit == unit {
			return a, true
		}
	}
	return planArtifact{}, false
}

// TestReleasePlanVersionPropagation is the version-propagation invariant for the
// release plan (the same coupling apply-release-plan.mjs enforces): a single group
// version must flow, unchanged, into every tag, image tag, chart version, go.mod
// pin and compatibility bound derived from it.
func TestReleasePlanVersionPropagation(t *testing.T) {
	root := repoRoot(t)
	p := loadPlan(t, root)

	core := p.Groups["core"]
	k8s := p.Groups["kubernetes"]
	if !semverRE.MatchString(core.Version) {
		t.Fatalf("core version %q is not semver", core.Version)
	}
	if !semverRE.MatchString(k8s.Version) {
		t.Fatalf("kubernetes version %q is not semver", k8s.Version)
	}

	// Core group: root tag + go-module tag are v<core>; dashboard image is <core>.
	if want := "v" + core.Version; len(core.Tags) != 1 || core.Tags[0] != want {
		t.Errorf("core tags = %v, want [%s]", core.Tags, want)
	}
	if a, ok := artifactByUnit(core, "core"); !ok || a.Tag != "v"+core.Version {
		t.Errorf("core go-module tag = %q, want v%s", a.Tag, core.Version)
	}
	if a, ok := artifactByUnit(core, "dashboard-image"); !ok || a.Tag != core.Version {
		t.Errorf("dashboard-image tag = %q, want %s", a.Tag, core.Version)
	}

	// Kubernetes group: nested tag, operator image, chart trio, docs, pin, compat.
	if want := "integrations/kubernetes/v" + k8s.Version; len(k8s.Tags) != 1 || k8s.Tags[0] != want {
		t.Errorf("kubernetes tags = %v, want [%s]", k8s.Tags, want)
	}
	if a, ok := artifactByUnit(k8s, "operator-image"); !ok || a.Tag != k8s.Version {
		t.Errorf("operator-image tag = %q, want %s", a.Tag, k8s.Version)
	}
	if a, ok := artifactByUnit(k8s, "operator-chart"); !ok {
		t.Error("missing operator-chart artifact")
	} else if a.ChartVersion != k8s.Version || a.AppVersion != k8s.Version || a.DefaultImageTag != k8s.Version {
		t.Errorf("operator-chart trio = (%q,%q,%q), want all %s", a.ChartVersion, a.AppVersion, a.DefaultImageTag, k8s.Version)
	}
	// The core Go module path carries its major (…/pacto/vN). The pin +
	// compatibility track that PATH major — v<N>.0.0 until the first vN release is
	// published, then the unit version — not the last-published (vN-1) unit
	// version. This is what lets a cross-major bump build under go.work and land on
	// v<N>.0.0 when the major changeset applies.
	coreArt, _ := artifactByUnit(core, "core")
	majorOf := func(v string) int { n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0]); return n }
	pathMajor := majorOf(core.Version)
	if m := regexp.MustCompile(`/v(\d+)$`).FindStringSubmatch(coreArt.Coordinate); m != nil {
		pathMajor, _ = strconv.Atoi(m[1])
	}
	unitMajor := majorOf(core.Version)
	wantPin := "v" + core.Version
	if pathMajor > unitMajor {
		wantPin = fmt.Sprintf("v%d.0.0", pathMajor)
	}
	if k8s.GoModPin == nil || k8s.GoModPin.Version != wantPin {
		t.Errorf("k8s goModPin = %+v, want version %s", k8s.GoModPin, wantPin)
	}
	compatMajor := pathMajor
	if unitMajor > compatMajor {
		compatMajor = unitMajor
	}
	if wantCompat := fmt.Sprintf(">=%d.0.0", compatMajor); k8s.Compatibility == nil || k8s.Compatibility.PactoCore != wantCompat {
		t.Errorf("k8s compatibility = %+v, want pactoCore %s", k8s.Compatibility, wantCompat)
	}

	// The manifest each unit records must agree with the plan's group versions.
	m := loadManifestVersions(t, root)
	for unit, wantVer := range map[string]string{"core": core.Version, "cli": core.Version, "k8s-module": k8s.Version, "operator-chart": k8s.Version} {
		if got := m[unit]; got != wantVer {
			t.Errorf("manifest unit %q version = %q, want %q (plan drift)", unit, got, wantVer)
		}
	}
}

func loadManifestVersions(t *testing.T, root string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "release", "release-manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Units map[string]struct {
			Version string `json:"version"`
		} `json:"units"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	out := map[string]string{}
	for k, v := range m.Units {
		out[k] = v.Version
	}
	return out
}

// validateManifest is a total parser+validator for a release manifest: any bytes
// are either rejected with an error or accepted as a manifest whose every unit
// carries a semver version and a non-empty coordinate/tag/artifactKind.
func validateManifest(data []byte) error {
	var m struct {
		Schema string `json:"schema"`
		Units  map[string]struct {
			ArtifactKind string `json:"artifactKind"`
			Coordinate   string `json:"coordinate"`
			Tag          string `json:"tag"`
			Version      string `json:"version"`
		} `json:"units"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if !strings.HasPrefix(m.Schema, "pacto-release-manifest/") {
		return fmt.Errorf("unexpected schema %q", m.Schema)
	}
	if len(m.Units) == 0 {
		return fmt.Errorf("no units")
	}
	for name, u := range m.Units {
		if !semverRE.MatchString(u.Version) {
			return fmt.Errorf("unit %q version %q not semver", name, u.Version)
		}
		if u.Coordinate == "" || u.Tag == "" || u.ArtifactKind == "" {
			return fmt.Errorf("unit %q missing coordinate/tag/artifactKind", name)
		}
	}
	return nil
}

// TestManifestValidatorAcceptsCommitted anchors the fuzz validator against reality:
// the committed release manifest must pass validateManifest.
func TestManifestValidatorAcceptsCommitted(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "release", "release-manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := validateManifest(b); err != nil {
		t.Fatalf("committed manifest rejected by validator: %v", err)
	}
}

// FuzzReleaseManifestValidate proves the manifest validator is total (never panics)
// on any input; well-formed shapes are accepted, everything else is rejected.
func FuzzReleaseManifestValidate(f *testing.F) {
	f.Add([]byte(`{"schema":"pacto-release-manifest/v1","units":{"core":{"artifactKind":"go-module","coordinate":"c","tag":"v2.7.0","version":"2.7.0"}}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"pacto-release-manifest/v1","units":{"x":{"version":"nope"}}}`))
	f.Add([]byte(`garbage`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = validateManifest(data) // must not panic for any input
	})
}
