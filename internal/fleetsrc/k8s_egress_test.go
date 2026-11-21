package fleetsrc

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// egressItem is a reconciled Pacto at status format 4 in namespace ns,
// with an egress coverage of state and reason on nodes of which withAgent
// run the node agent, and one stripe egress entry.
func egressItem(service, ns, state, reason, nodes, withAgent string) string {
	fresh := time.Now().UTC().Format(time.RFC3339)
	return `{"metadata":{"name":"` + service + `","namespace":"` + ns + `"},"status":{"statusFormat":` + strconv.Itoa(delivery.StatusFormatEgress) + `,"lastReconciledAt":"` + fresh + `",
	  "contract":{"serviceName":"` + service + `","resolvedRef":"oci://reg.example/pactos/` + service + `@sha256:` + strings.Repeat("a", 64) + `"},
	  "externalsCoverage":{"state":"complete","refreshSeconds":30},
	  "observedEgress":[{"identity":"stripe","class":"vendor-api","host":"api.stripe.com","port":443,"source":"ebpf"}],
	  "observedEgressCoverage":{"state":"` + state + `","reason":"` + reason + `","refreshSeconds":30,"nodes":` + nodes + `,"nodesWithAgent":` + withAgent + `}}}`
}

func kernelRows(col *fleet.Collection) []fleet.Provider {
	return slices.DeleteFunc(slices.Clone(col.Providers), func(p fleet.Provider) bool { return p.ID != fleet.ProviderKernelEgress })
}

func TestK8sSourceReportsKernelEgressRow(t *testing.T) {
	col := collectList(t, `{"items":[`+egressItem("payments", "prod", "partial", "node-without-agent", "4", "3")+`]}`)
	rows := kernelRows(col)
	if len(rows) != 1 || rows[0].Evidence != "node agent on 3/4 nodes" || rows[0].Status != fleet.SourcePartial || rows[0].Scope != fleet.ScopeCluster ||
		len(rows[0].Limitations) != 1 || rows[0].Limitations[0].Source != "prod-cluster" {
		t.Fatalf("rows = %+v", rows)
	}
	if want := []fleet.ObservedEdge{{From: "payments", To: "api.stripe.com", Count: 1}}; !reflect.DeepEqual(col.Observed, want) {
		t.Fatalf("observed = %+v", col.Observed)
	}
	// Scoped to two namespaces: one row, built from both namespaces' views.
	c := &nsFakeClient{found: true, lists: map[string]string{
		"prod":    `{"items":[` + egressItem("payments", "prod", "complete", "", "4", "4") + `]}`,
		"billing": `{"items":[` + egressItem("ledger", "billing", "partial", "dns-unavailable", "4", "4") + `]}`,
	}}
	rows = kernelRows(collectScoped(t, c, "prod", "billing"))
	want := []fleet.ServiceKey{fleet.NewServiceKeyDomain("reg.example/pactos", "ledger"), fleet.NewServiceKeyDomain("reg.example/pactos", "payments")}
	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Services, want) || rows[0].Status != fleet.SourcePartial {
		t.Fatalf("scoped rows = %+v", rows)
	}
	// No egress coverage anywhere: the node agent is off, no row.
	if rows := kernelRows(collectList(t, `{"items":[`+externalsItem("payments", "prod", stripeExternal)+`]}`)); len(rows) != 0 {
		t.Fatalf("rows with the node agent off = %+v", rows)
	}
	if col, _ := NewK8sSource("c", nil, &fakeK8sClient{disc: &k8sclient.CRDDiscovery{}}, nil).Collect(t.Context()); len(kernelRows(col)) != 0 {
		t.Fatal("a cluster with no Pacto CRD reports a kernel-egress row")
	}
}

func TestK8sSourceEgressLimitationOnTarget(t *testing.T) {
	col := collectList(t, `{"items":[`+egressItem("payments", "prod", "partial", "unnamed-public", "4", "4")+`,`+egressItem("ledger", "prod", "complete", "", "4", "4")+`]}`)
	lims := col.Targets[0].Limitations
	if len(lims) != 1 || lims[0].Code != fleet.LimitationExternalsUnreported || lims[0].Source != "prod-cluster" || !strings.Contains(lims[0].Message, "unnamed-public") {
		t.Fatalf("partial egress limitations = %+v", lims)
	}
	if col.Targets[1].Limitations != nil {
		t.Fatalf("complete egress limitations = %+v", col.Targets[1].Limitations)
	}
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := fleet.NewQuery(snap).GetExternal("stripe"); err != nil || v.Meta.Completeness != fleet.CompletenessPartial {
		t.Fatalf("external = %+v, %v", v, err)
	}
}
