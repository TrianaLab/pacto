package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// deploymentsOnly is a cluster whose only workloads are the Deployments of
// body; the workload source calls nothing but discovery, which finds no
// GitOps API, and ListPage.
type deploymentsOnly struct {
	k8sclient.K8sClient
	body string
}

func (c deploymentsOnly) ListPage(_ context.Context, gvr schema.GroupVersionResource, _, _ string) ([]byte, string, error) {
	if gvr.Resource == "deployments" {
		return []byte(c.body), "", nil
	}
	return []byte(`{"items":[]}`), "", nil
}

func (deploymentsOnly) PreferredVersions(context.Context, ...string) (map[string]string, error) {
	return nil, nil
}

// inferredSnapshot reads the Decision 9 object
// (internal/testutil/testdata/leak-deployment.json) and pwn, whose image is a
// terminal escape, through the workload source.
func inferredSnapshot(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	obj, err := os.ReadFile("../testutil/testdata/leak-deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	pwn := strings.ReplaceAll(strings.ReplaceAll(string(obj), `"registry.example/billing:1.4.2"`, `"\u001b]0;pwn\u0007"`), `"name": "billing",`, `"name": "pwn",`)
	src := fleetsrc.NewWorkloadSource("k8s-workloads", nil, deploymentsOnly{body: `{"items":[` + string(obj) + "," + pwn + `]}`}, nil)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestListMarksInferred(t *testing.T) {
	c := newContextOver(t, inferredSnapshot(t))
	l := newListScreen(c).(*listScreen)
	l.entities = append(l.entities, fleet.EntityRef{Kind: fleet.KindService, Key: "app", Label: "app"})
	rows := l.rows(c)
	if len(rows) != 3 || !strings.Contains(rows[0][1], "k8s:shop/Deployment/billing inferred") || strings.Contains(rows[2][1], "inferred") {
		t.Errorf("rows = %q, want the inferred rows marked", rows)
	}
	l.tbl.SetRows(rows)
	l.tbl.SetCursor(0)
	if pane := l.paneBody(c, 60); !strings.Contains(pane, "inferred service") || !strings.Contains(pane, "Deployment") {
		t.Errorf("pane lacks the inferred kind:\n%s", pane)
	}
}

// TestDetailRendersInferredWorkload: an inferred service page shows its
// workload with where each value was read and that its calls are unknown,
// and carries nothing of the Decision 9 object's environment, Secrets,
// ConfigMaps, headers, exec, arguments or annotations, and no escape.
func TestDetailRendersInferredWorkload(t *testing.T) {
	q := fleet.NewQuery(inferredSnapshot(t))
	for _, name := range []string{"billing", "pwn"} {
		d, err := q.EntityDetail(fleet.KindService, string(fleet.NewServiceKey("k8s:shop/Deployment/"+name)))
		if err != nil {
			t.Fatal(err)
		}
		out := renderDetail(d)
		if !strings.Contains(out, "Workload: Deployment shop/"+name) || !strings.Contains(out, "Calls unknown: Pacto reads no traffic source here") || strings.Contains(out, "Summary") {
			t.Errorf("%s detail:\n%s", name, out)
		}
		if name == "billing" && !strings.Contains(out, "image registry.example/billing:1.4.2 (from k8s-workloads: Deployment spec.template.spec.containers[name=billing].image)") {
			t.Errorf("billing lacks its image:\n%s", out)
		}
		for _, leak := range []string{"DB_PASSWORD", "hunter2", "API_KEY", "billing-creds", "token", "billing-config", "s3cr3t-hdr", "s3cr3t-query", "s3cr3t-exec", "s3cr3t-arg", "last-applied", "\x1b]0;pwn"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s detail holds %q:\n%s", name, leak, out)
			}
		}
	}
}
