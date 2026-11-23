package fleetsrc

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func managedResult() detect.Result {
	return detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectionNow, Cluster: "uid-prod", ClusterName: "prod",
		Entries: []detect.Entry{{Kind: fleet.ProviderManagedResources, Completeness: detect.Complete}},
		ManagedResources: []fleet.RawManagedResource{{Source: "crossplane", Identity: "aws/rds/123456789012/eu-west-1/orders-db", Type: "rds.aws.upbound.io/Instance",
			Cluster: "forged", ClusterName: "forged", Name: "orders-db", ConnectionSecrets: []string{"shop/orders-db-conn"}}},
		ManagedResourcesTotal: 1,
		SecretRefs:            []fleet.RawSecretRef{{Cluster: "forged", Namespace: "shop", Kind: "Deployment", Name: "orders", Secret: "orders-db-conn"}},
	}
}

func TestDetectionSourceCarriesManagedResources(t *testing.T) {
	res := managedResult()
	col := collectDetection(t, NewDetectionResultSource("detection", res, clockAt(detectionNow)))
	if len(col.ManagedResources) != 1 || col.ManagedResources[0].Name != "orders-db" || len(col.SecretRefs) != 1 || col.SecretRefs[0].Secret != "orders-db-conn" {
		t.Fatalf("collection = %+v", col)
	}
	if len(col.Limitations) != 0 {
		t.Errorf("an uncapped Result is limited: %+v", col.Limitations)
	}
	res.ManagedResourcesTotal = 4
	capped := collectDetection(t, NewDetectionResultSource("detection", res, clockAt(detectionNow)))
	if len(capped.Limitations) != 1 || capped.Limitations[0].Code != fleet.LimitationManagedResourcesCapped || capped.Limitations[0].Source != "detection" {
		t.Errorf("capped limitations = %+v", capped.Limitations)
	}
	if row := capped.Providers[0]; row.Status != fleet.SourcePartial {
		t.Errorf("capped row = %+v", row)
	}
	raw, _ := json.Marshal(res)
	unread := collectDetection(t, NewDetectionFileSource("detection", writeDetection(t, append(raw[:len(raw)-1:len(raw)-1], []byte(`,"asOf":"2020-01-01T00:00:00Z"}`)...)), clockAt(detectionNow)))
	if unread.State.Status != fleet.SourceStale || len(unread.ManagedResources) != 1 {
		t.Errorf("a stale Result keeps its resources with a stale state: %+v", unread.State)
	}
	gone := collectDetection(t, NewDetectionFileSource("detection", writeDetection(t, []byte(`{`)), clockAt(detectionNow)))
	if gone.ManagedResources != nil || gone.SecretRefs != nil || gone.Limitations != nil {
		t.Errorf("an unreadable Result carries resources: %+v", gone)
	}
}

// TestDetectionSourceStampsResourceCluster: every resource and ref carries
// the cluster the Result was read in, whatever cluster the ConfigMap names,
// and the Result itself is left unchanged.
func TestDetectionSourceStampsResourceCluster(t *testing.T) {
	res := managedResult()
	col := collectDetection(t, NewDetectionResultSource("detection", res, clockAt(detectionNow)))
	m, r := col.ManagedResources[0], col.SecretRefs[0]
	if m.Cluster != "uid-prod" || m.ClusterName != "prod" || r.Cluster != "uid-prod" || r.ClusterName != "prod" {
		t.Fatalf("resource %+v, ref %+v: want cluster uid-prod/prod", m, r)
	}
	if res.ManagedResources[0].Cluster != "forged" || res.SecretRefs[0].Cluster != "forged" {
		t.Error("stamping changed the Result")
	}
}

func TestWorkloadSourceReportsSecretNamesOnly(t *testing.T) {
	deploy := `{"metadata":{"name":"orders","namespace":"shop"},"spec":{"template":{"spec":{
		"containers":[{"name":"orders","image":"orders:1","env":[{"name":"DB_PASSWORD","valueFrom":{"secretKeyRef":{"name":"orders-db-conn","key":"password"}}},{"name":"PLAIN","value":"hunter2"}]}],
		"volumes":[{"name":"tls","secret":{"secretName":"orders-tls"}}]}}}}`
	cronjob := `{"metadata":{"name":"nightly","namespace":"shop"},"spec":{"jobTemplate":{"spec":{"template":{"spec":{"containers":[{"name":"nightly","image":"batch:1","envFrom":[{"secretRef":{"name":"orders-db-conn"}}]}]}}}}}}`
	f := &workloadFake{pages: map[string][2]string{"deployments||": {deploy}, "cronjobs||": {cronjob}}}
	col, err := NewWorkloadSource("w", &fleet.Cluster{ID: "uid-prod", Name: "prod"}, f, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []fleet.RawSecretRef{
		{Cluster: "uid-prod", ClusterName: "prod", Namespace: "shop", Kind: "Deployment", Name: "orders", Secret: "orders-db-conn"},
		{Cluster: "uid-prod", ClusterName: "prod", Namespace: "shop", Kind: "Deployment", Name: "orders", Secret: "orders-tls"},
		{Cluster: "uid-prod", ClusterName: "prod", Namespace: "shop", Kind: "CronJob", Name: "nightly", Secret: "orders-db-conn"},
	}
	if !slices.Equal(col.SecretRefs, want) {
		t.Fatalf("refs = %+v\nwant %+v", col.SecretRefs, want)
	}
	data, _ := json.Marshal(col.SecretRefs)
	for _, leak := range []string{"password", "hunter2", "DB_PASSWORD"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("refs hold %q", leak)
		}
	}
	for _, asked := range f.asked {
		if strings.HasPrefix(asked, "secrets|") {
			t.Errorf("the workload source asked for a Secret: %s", asked)
		}
	}
	noCluster, err := NewWorkloadSource("w", nil, &workloadFake{pages: map[string][2]string{"deployments||": {deploy}}}, nil).Collect(context.Background())
	if err != nil || len(noCluster.SecretRefs) != 2 || noCluster.SecretRefs[0].Cluster != "" {
		t.Errorf("refs with no cluster = %+v, %v", noCluster.SecretRefs, err)
	}
}
