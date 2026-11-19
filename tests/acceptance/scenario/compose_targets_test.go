package scenario

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// The demo publishes a signed target snapshot, so `pacto impact` against its
// registry reads what the demo cluster runs without a kubeconfig.
func TestComposeRendersTargetsService(t *testing.T) {
	f := composeFileOf(t, OperationalGraph)
	svc := composeSvc(t, f, "targets")
	if svc["image"] != composeOpts.PactoImage {
		t.Errorf("targets runs %v, want the pacto image", svc["image"])
	}
	script := argsOf(t, svc, "command")
	for _, want := range []string{
		"pacto evidence keygen --out " + composeTargetsDir + " --key-id " + composeTargetsKeyID,
		"/g\" " + ComposeTargetSnapshotFile + " >" + composeTargetsStamped,
		"pacto delivery targets publish " + composeTargetsStamped + " --registry " + ComposeDomain + " --key " + composeTargetsKey,
		"sleep 300",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the targets script lacks %q:\n%s", want, script)
		}
	}
	mounted := false
	for _, c := range svc["configs"].([]any) {
		m := c.(map[string]any)
		if m["target"] == ComposeTargetSnapshotFile {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("targets does not mount its snapshot document at %s", ComposeTargetSnapshotFile)
	}
	hc, _ := svc["healthcheck"].(map[string]any)
	if test := argsOf(t, hc, "test"); !strings.Contains(test, composeTargetsKey+".pub.pem") {
		t.Errorf("targets is healthy on %q; want the public key its first publish writes", test)
	}
	deps, _ := svc["depends_on"].(map[string]any)
	if _, ok := deps["registry"]; !ok {
		t.Error("targets does not wait for the registry it pushes to")
	}
	if _, taken := composeReservedServices["targets"]; !taken {
		t.Error("a workload could take the targets service name")
	}
}

// composeSnapshotDoc is the snapshot document the projection carries inline.
func composeSnapshotDoc(t *testing.T) delivery.TargetSnapshot {
	t.Helper()
	body := inlineConfig(t, composeFileOf(t, OperationalGraph), "targets_snapshot.json")
	var s delivery.TargetSnapshot
	if err := strictjson.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("the snapshot document does not decode strictly: %v\n%s", err, body)
	}
	return s
}

func TestComposeTargetSnapshotDocumentChecks(t *testing.T) {
	s := composeSnapshotDoc(t)
	if err := s.Check(); err != nil {
		t.Fatalf("Check refuses the snapshot document: %v", err)
	}
	if s.Cluster != ComposeTargetsCluster || s.Publisher != "compose:targets" || s.Completeness != fleet.CompletenessComplete {
		t.Errorf("snapshot cluster %q publisher %q completeness %q", s.Cluster, s.Publisher, s.Completeness)
	}
}

func TestComposeTargetSnapshotPlacesScenarioServices(t *testing.T) {
	s := composeSnapshotDoc(t)
	files, err := OperationalGraph.MaterializeFiles(ComposeDomain)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := OperationalGraph.Digests(files)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]delivery.PactoView{}
	for _, p := range s.Pactos {
		byName[p.Status.Contract.ServiceName] = p
	}
	placed := 0
	for _, svc := range OperationalGraph.Services {
		if svc.Workload == nil {
			if _, ok := byName[svc.Name]; ok {
				t.Errorf("%s runs nothing, yet the snapshot places it", svc.Name)
			}
			continue
		}
		placed++
		p, ok := byName[svc.Name]
		if !ok {
			t.Errorf("the snapshot does not place %s", svc.Name)
			continue
		}
		rev, _ := svc.DeployedRevision()
		c := p.Status.Contract
		wantRef := ComposeDomain + "/" + svc.Repo + "@" + digests[DigestKey(svc.Name, rev.Version)]
		if c.Version != rev.Version || c.ResolvedRef != wantRef {
			t.Errorf("%s: version %q ref %q, want %q %q", svc.Name, c.Version, c.ResolvedRef, rev.Version, wantRef)
		}
		if p.Status.LastReconciledAt != composeReconciledPlaceholder {
			t.Errorf("%s: lastReconciledAt %q, want the placeholder each publish stamps", svc.Name, p.Status.LastReconciledAt)
		}
		if c.Owner == nil || c.Owner.Team != "commerce" {
			t.Errorf("%s: owner %+v, want team commerce", svc.Name, c.Owner)
		}
		if w := p.Spec.Target.Workload(); w == nil || w.Name != svc.Workload.Name {
			t.Errorf("%s: target workload %+v, want %s", svc.Name, w, svc.Workload.Name)
		}
	}
	if len(s.Pactos) != placed {
		t.Errorf("the snapshot holds %d Pactos, want %d", len(s.Pactos), placed)
	}
	if got := byName["checkout"].Status.Interfaces; len(got) != 1 || got[0].Name != "api" {
		t.Errorf("checkout interfaces %+v, want api", got)
	}
	deps := byName["orders"].Status.Dependencies
	if len(deps) != 2 || deps[0].Name != "checkout" || deps[0].Compatibility != "^1.0.0" || deps[0].Ref != "oci://"+ComposeDomain+"/checkout" {
		t.Errorf("orders dependencies %+v, want checkout ^1.0.0 first", deps)
	}
}
