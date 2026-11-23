/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// stateFixture is the state P4 reads: two RDS and SQS resources whose
// attributes hold secrets.
const stateFixture = "../../../../pkg/managedres/testdata/state-v4.json"

// rewrite sends every request to srv, whatever its host.
type rewrite struct{ srv *httptest.Server }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.srv.URL)
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

// fakeAWS is an S3 and Resource Explorer stand-in answering the fixture
// state at /app.tfstate, every other key 404, and rows at /Search; it
// counts requests per path.
func fakeAWS(t *testing.T, rows int) (*http.Client, map[string]*atomic.Int32) {
	t.Helper()
	state, err := os.ReadFile(stateFixture)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]*atomic.Int32{"/app.tfstate": {}, "/missing.tfstate": {}, "/Search": {}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := calls[r.URL.Path]; c != nil {
			c.Add(1)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/app.tfstate":
			_, _ = w.Write(state)
		case "/Search":
			res := make([]map[string]string, rows)
			for i := range res {
				res[i] = map[string]string{"Arn": fmt.Sprintf("arn:aws:sqs:eu-west-1:123456789012:q-%d", i), "ResourceType": "sqs:queue", "Region": "eu-west-1"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Resources": res})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	// Static keys come first in the AWS chain; the IRSA token file only
	// tells the client a workload identity is bound. No profile is read.
	dir := t.TempDir()
	for k, v := range map[string]string{"AWS_ACCESS_KEY_ID": "AKIDEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret", "AWS_REGION": "eu-west-1",
		"AWS_WEB_IDENTITY_TOKEN_FILE": filepath.Join(dir, "token"), "AWS_ROLE_ARN": "arn:aws:iam::123456789012:role/pacto",
		"AWS_CONFIG_FILE": filepath.Join(dir, "config"), "AWS_SHARED_CREDENTIALS_FILE": filepath.Join(dir, "credentials"), "AWS_PROFILE": ""} {
		t.Setenv(k, v)
	}
	return &http.Client{Transport: rewrite{srv}}, calls
}

// remoteDetector is a detector with no cluster managed reads and readers r.
func remoteDetector(t *testing.T, hc *http.Client, r Readers) *Detector {
	t.Helper()
	c := newClient(interceptor.Funcs{}, operatorDeployment())
	d := newDetector(c, servedAll)
	d.Readers.TerraformStates, d.Readers.CloudInventory, d.Readers.WorkloadIdentity = r.TerraformStates, r.CloudInventory, r.WorkloadIdentity
	d.HTTP = hc
	return d
}

func detectOnce(d *Detector) detect.Result { return d.detect(context.Background()) }

func hasKind(res detect.Result, kind string) bool {
	return slices.ContainsFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == kind })
}

func TestDetectorReadsListedState(t *testing.T) {
	hc, calls := fakeAWS(t, 0)
	d := remoteDetector(t, hc, Readers{TerraformStates: []string{"s3://acme-tf/app.tfstate?region=eu-west-1"}})
	res := detectOnce(d)
	e := entry(t, res, "terraform")
	if e.Completeness != detect.Complete || e.Evidence != "reads s3://acme-tf/app.tfstate?region=eu-west-1" {
		t.Fatalf("entry = %+v", e)
	}
	if len(res.ManagedResources) == 0 || res.ManagedResourcesTotal != len(res.ManagedResources) {
		t.Fatalf("resources = %+v", res.ManagedResources)
	}
	b, _ := json.Marshal(res.ManagedResources)
	for _, r := range res.ManagedResources {
		if r.Cluster != "cluster-uid" || r.ClusterName != "kind-pacto" || r.Location != "s3://acme-tf/app.tfstate?region=eu-west-1" || r.Source != "terraform" {
			t.Errorf("provenance %+v", r)
		}
	}
	if !strings.Contains(string(b), "aws/rds/123456789012/eu-west-1/orders-db") || strings.Contains(string(b), "hunter2") {
		t.Errorf("resources = %s", b)
	}
	// Within an Interval the state is not fetched again.
	_ = detectOnce(d)
	if n := calls["/app.tfstate"].Load(); n != 1 {
		t.Errorf("fetched %d times in one interval", n)
	}
	d.Now = func() time.Time { return now.Add(time.Hour) }
	_ = detectOnce(d)
	if n := calls["/app.tfstate"].Load(); n != 2 {
		t.Errorf("fetched %d times after an interval", n)
	}
	// No state listed and no identity: no entry, no read.
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	if res := detectOnce(remoteDetector(t, hc, Readers{})); hasKind(res, "terraform") || hasKind(res, "cloud-inventory") {
		t.Errorf("entries = %+v", res.Entries)
	}
}

func TestStateUnreachableIsUnavailableRow(t *testing.T) {
	hc, _ := fakeAWS(t, 0)
	d := remoteDetector(t, hc, Readers{TerraformStates: []string{"s3://acme-tf/missing.tfstate", "s3://acme-tf/a b"}})
	res := detectOnce(d)
	e := entry(t, res, "terraform")
	if e.Completeness != detect.Unknown || len(e.Details) != 2 || res.ManagedResources != nil {
		t.Fatalf("entry = %+v", e)
	}
	if !slices.Contains(e.Details, "s3://acme-tf/a b: not a listed state location") ||
		!slices.Contains(e.Details, "s3://acme-tf/missing.tfstate: not found, or the identity may not read it") {
		t.Errorf("details = %q", e.Details)
	}
	rows := detect.ProviderRows(res, "operator", now)
	if i := slices.IndexFunc(rows, func(p fleet.Provider) bool { return p.ID == "terraform" }); i < 0 || rows[i].Status != fleet.SourceUnavailable {
		t.Errorf("rows = %+v", rows)
	}
	// One read and one failed is partial.
	d.Readers.TerraformStates = []string{"s3://acme-tf/app.tfstate", "s3://acme-tf/missing.tfstate"}
	if e := entry(t, detectOnce(d), "terraform"); e.Completeness != detect.Partial {
		t.Errorf("partial = %+v", e)
	}
	// With no identity nothing is fetched; an unreachable endpoint names no address.
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	d = remoteDetector(t, hc, Readers{TerraformStates: []string{"s3://acme-tf/app.tfstate"}})
	if e := entry(t, detectOnce(d), "terraform"); e.Completeness != detect.Unknown ||
		!strings.Contains(e.Details[0], "no read-only workload identity") {
		t.Errorf("no identity = %+v", e)
	}
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/token")
	d = remoteDetector(t, nil, Readers{TerraformStates: []string{"s3://acme-tf/app.tfstate"}})
	defaultHTTP.Transport = rewrite{httptest.NewUnstartedServer(nil)}
	defer func() { defaultHTTP.Transport = nil }()
	if e := entry(t, detectOnce(d), "terraform"); !strings.HasSuffix(e.Details[0], ": the cloud endpoint did not answer") {
		t.Errorf("unreachable = %+v", e)
	}
}

func TestDetectorCloudInventoryOffByDefault(t *testing.T) {
	hc, calls := fakeAWS(t, 2)
	res := detectOnce(remoteDetector(t, hc, Readers{WorkloadIdentity: "aws=arn:aws:iam::123456789012:role/pacto"}))
	e := entry(t, res, "cloud-inventory")
	if e.Value != "off" || e.Completeness != detect.Unknown || !strings.Contains(e.Reason, "controller.cloudInventory") ||
		!strings.Contains(e.Reason, "arn:aws:iam::123456789012:role/pacto") || calls["/Search"].Load() != 0 {
		t.Errorf("entry = %+v, %d searches", e, calls["/Search"].Load())
	}
	// Opted in, the inventory is read with the identity.
	res = detectOnce(remoteDetector(t, hc, Readers{CloudInventory: "aws", WorkloadIdentity: "aws=arn:aws:iam::123456789012:role/pacto"}))
	if e := entry(t, res, "cloud-inventory"); e.Completeness != detect.Complete || len(res.ManagedResources) != 2 ||
		res.ManagedResources[0].Source != "aws-resource-explorer" || res.ManagedResources[0].Cluster != "cluster-uid" {
		t.Errorf("entry = %+v, resources %+v", e, res.ManagedResources)
	}
}

func TestDetectorCloudInventoryOffWithoutIdentity(t *testing.T) {
	hc, calls := fakeAWS(t, 2)
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	res := detectOnce(remoteDetector(t, hc, Readers{CloudInventory: "aws"}))
	e := entry(t, res, "cloud-inventory")
	if e.Value != "off" || e.Completeness != detect.Unknown || e.Reason != "no read-only workload identity on the operator ServiceAccount" || calls["/Search"].Load() != 0 {
		t.Errorf("entry = %+v", e)
	}
	// An identity of another cloud does not read this one.
	res = detectOnce(remoteDetector(t, hc, Readers{CloudInventory: "gcp", WorkloadIdentity: "azure=client-1"}))
	if e := entry(t, res, "cloud-inventory"); e.Value != "off" || e.Source != "controller.cloudInventory=gcp" {
		t.Errorf("other cloud = %+v", e)
	}
	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", "/token")
	res = detectOnce(remoteDetector(t, hc, Readers{CloudInventory: "gcp", WorkloadIdentity: "azure=client-1"}))
	if e := entry(t, res, "cloud-inventory"); !strings.Contains(e.Reason, "no read-only gcp workload identity on the operator ServiceAccount, only azure identity client-1") {
		t.Errorf("other cloud = %+v", e)
	}
	// A failed read is unknown and names no address.
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/token")
	t.Setenv("AWS_REGION", "")
	res = detectOnce(remoteDetector(t, hc, Readers{CloudInventory: "aws"}))
	if e := entry(t, res, "cloud-inventory"); e.Completeness != detect.Unknown || !strings.Contains(e.Reason, "region") {
		t.Errorf("failed = %+v", e)
	}
}

func TestDetectorBoundAcrossSources(t *testing.T) {
	hc, _ := fakeAWS(t, detect.MaxManagedResources)
	o := objects()
	d, _ := managedDetector(&cluster{objects: o})
	d.HTTP = hc
	d.Readers.TerraformStates, d.Readers.CloudInventory = []string{"s3://acme-tf/app.tfstate"}, "aws"
	res := detectOnce(d)
	if len(res.ManagedResources) != detect.MaxManagedResources || !res.ManagedCapped() {
		t.Fatalf("kept %d of %d", len(res.ManagedResources), res.ManagedResourcesTotal)
	}
	sources := []string{}
	for _, r := range res.ManagedResources {
		if len(sources) == 0 || sources[len(sources)-1] != r.Source {
			sources = append(sources, r.Source)
		}
	}
	// Cluster objects first, then the state, then the inventory.
	if !slices.Equal(sources, []string{"crossplane", "ack", "config-connector", "terraform", "aws-resource-explorer"}) ||
		res.ManagedResourcesTotal <= detect.MaxManagedResources {
		t.Errorf("sources = %v, total %d", sources, res.ManagedResourcesTotal)
	}
}
