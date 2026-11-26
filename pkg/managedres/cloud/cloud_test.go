package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"golang.org/x/oauth2"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/managedres"
)

var now = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

const gsa = "pacto@acme-prod.iam.gserviceaccount.com"

// fakeCloud is one httptest server standing in for every endpoint: it
// records each request and answers from routes by path, 404 otherwise.
type fakeCloud struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	routes   map[string]func(w http.ResponseWriter, r *http.Request, body string)
}

func (f *fakeCloud) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.requests))
	for _, r := range f.requests {
		out = append(out, r.Method+" "+r.URL.Path)
	}
	return out
}

// newFake swaps every package variable for a fake and points every
// endpoint at one httptest server.
func newFake(t *testing.T, env map[string]string) (*fakeCloud, *http.Client) {
	t.Helper()
	f := &fakeCloud{routes: map[string]func(http.ResponseWriter, *http.Request, string){}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests, f.bodies = append(f.requests, r), append(f.bodies, string(b))
		f.mu.Unlock()
		if h, ok := f.routes[r.URL.Path]; ok {
			h(w, r, string(b))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	u := srv.URL
	saved := endpoints
	endpoints.s3 = u + "/s3/{bucket}/{region}/{key}"
	endpoints.s3Path = u + "/s3path/{region}/{bucket}/{key}"
	endpoints.gcs = u + "/gcs/{bucket}/o/{key}?alt=media"
	endpoints.azureBlob = u + "/blob/{account}/{container}/{key}"
	endpoints.awsExplorer = u + "/explorer/{region}/Search"
	endpoints.gcpAsset = u + "/asset/{project}:searchAllResources"
	endpoints.azureGraph = u + "/graph?api-version=2022-10-01"
	endpoints.gcpMetadata = u + "/metadata/email"
	savedEnv, savedAWS, savedGCP, savedAzure, savedEmail := getenv, awsConfig, gcpTokenSource, azureCredential, gcpMetadataEmail
	t.Cleanup(func() {
		endpoints, getenv, awsConfig, gcpTokenSource, azureCredential, gcpMetadataEmail = saved, savedEnv, savedAWS, savedGCP, savedAzure, savedEmail
	})
	getenv = func(k string) string { return env[k] }
	awsConfig = func(context.Context) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "session"}, nil
		})}, nil
	}
	gcpTokenSource = func(context.Context, ...string) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "gcp-token"}), nil
	}
	azureCredential = func(string) (azcore.TokenCredential, error) { return fakeAzure{}, nil }
	f.routes["/metadata/email"] = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, gsa+"\n")
	}
	return f, srv.Client()
}

var staticKeys = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}, nil
})

var irsa = map[string]string{envAWSWebIdentity: "/var/run/secrets/eks.amazonaws.com/serviceaccount/token"}

type fakeAzure struct{ err error }

func (f fakeAzure) GetToken(_ context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "azure-token " + strings.Join(o.Scopes, ",")}, f.err
}

func TestWorkloadIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, flag string
		env        map[string]string
		want       Identity
		ok         bool
	}{
		{"aws flag", "aws=arn:aws:iam::1:role/pacto", irsa, Identity{CloudAWS, "arn:aws:iam::1:role/pacto", "serviceAccount annotation"}, true},
		{"aws flag, no token", "aws=arn:aws:iam::1:role/pacto", nil,
			Identity{CloudAWS, "arn:aws:iam::1:role/pacto", "workload identity not active: the pod has no EKS IRSA or Pod Identity token"}, false},
		{"aws flag, pod identity", "aws=arn:aws:iam::1:role/pacto", map[string]string{envAWSPodIdentity: "http://169.254.170.23/v1/credentials"},
			Identity{CloudAWS, "arn:aws:iam::1:role/pacto", "serviceAccount annotation"}, true},
		{"gcp flag", "gcp=" + gsa, nil, Identity{CloudGCP, gsa, "serviceAccount annotation"}, true},
		{"gcp flag, not an email", "gcp=pacto", nil, Identity{CloudGCP, "pacto", `"pacto" is not a Google service account email`}, false},
		{"azure flag", "azure=client-1", map[string]string{envAzureToken: "/token"}, Identity{CloudAzure, "client-1", "serviceAccount annotation"}, true},
		{"azure flag, no token", "azure=client-1", nil,
			Identity{CloudAzure, "client-1", "workload identity not active: the pod has no Azure federated token"}, false},
		{"other cloud", "oci=x", nil, Identity{Reason: `--workload-identity names cloud "oci"; want aws, gcp or azure`}, false},
		{"empty value", "aws=", irsa, Identity{Reason: noIdentity}, false},
		{"IRSA env", "", map[string]string{envAWSWebIdentity: "/token", "AWS_ROLE_ARN": "arn:aws:iam::1:role/x"},
			Identity{CloudAWS, "arn:aws:iam::1:role/x", "EKS IRSA environment"}, true},
		{"IRSA env, no role", "", irsa, Identity{CloudAWS, "EKS IRSA", "EKS IRSA environment"}, true},
		{"Pod Identity env", "", map[string]string{envAWSPodIdentity: "http://169.254.170.23/v1/credentials"},
			Identity{CloudAWS, "EKS Pod Identity", "EKS Pod Identity environment"}, true},
		{"Azure env", "", map[string]string{envAzureToken: "/token", "AZURE_CLIENT_ID": "client-2"},
			Identity{CloudAzure, "client-2", "Azure workload identity environment"}, true},
		{"none", "", nil, Identity{Reason: noIdentity}, false},
	} {
		got, ok := WorkloadIdentity(tc.flag, func(k string) string { return tc.env[k] })
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: WorkloadIdentity = %+v %v, want %+v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if got := (Identity{CloudAWS, "arn", ""}).String(); got != "aws identity arn" {
		t.Errorf("String = %q", got)
	}
}

// TestCloudCredentialDefaults runs every default credential source once
// with nothing to find: each fails at once, and none reaches a network.
func TestCloudCredentialDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "pacto-test-missing")
	if _, err := defaultAWSConfig(context.Background()); err == nil {
		t.Error("the AWS default loaded a missing profile")
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "missing.json"))
	if _, err := gcpTokenSource(context.Background(), gcsScope); err == nil {
		t.Error("the GCP default found credentials")
	}
	for _, k := range []string{"AZURE_FEDERATED_TOKEN_FILE", "AZURE_CLIENT_ID", "AZURE_TENANT_ID"} {
		t.Setenv(k, "")
	}
	if _, err := azureCredential("client-1"); err == nil {
		t.Error("the Azure default built a credential with no federated token")
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	saved := endpoints
	defer func() { endpoints = saved }()
	endpoints.gcpMetadata = srv.URL
	if _, err := gcpMetadataEmail(context.Background(), srv.Client()); err == nil {
		t.Error("the metadata default read a 404")
	}
	endpoints.gcpMetadata = "://bad"
	if _, err := gcpMetadataEmail(context.Background(), srv.Client()); err == nil {
		t.Error("the metadata default read a bad URL")
	}
	if os.Getenv("AWS_PROFILE") != "pacto-test-missing" {
		t.Fatal("environment not set")
	}
}

func TestAzureUsesWorkloadIdentityCredentialOnly(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("jwt"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", token)
	t.Setenv("AZURE_TENANT_ID", "00000000-0000-0000-0000-000000000001")
	cred, err := defaultAzureCredential("client-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cred.(*azidentity.WorkloadIdentityCredential); !ok {
		t.Fatalf("credential %T, want the workload identity credential", cred)
	}
	if _, err := azureToken(context.Background(), Identity{Cloud: CloudAWS}, azureStorageScope); err == nil {
		t.Error("an AWS identity read Azure")
	}
	saved := azureCredential
	defer func() { azureCredential = saved }()
	azureCredential = func(string) (azcore.TokenCredential, error) { return nil, errors.New("no token file") }
	if _, err := azureToken(context.Background(), Identity{Cloud: CloudAzure}, azureStorageScope); err == nil {
		t.Error("a failed credential gave a token")
	}
	azureCredential = func(string) (azcore.TokenCredential, error) { return fakeAzure{err: errors.New("denied")}, nil }
	if _, err := azureToken(context.Background(), Identity{Cloud: CloudAzure}, azureStorageScope); err == nil {
		t.Error("a failed token was used")
	}
}

func loc(t *testing.T, s string) managedres.Location {
	t.Helper()
	l, err := managedres.ParseLocation(s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func fetch(t *testing.T, l managedres.Location, id Identity, hc *http.Client) string {
	t.Helper()
	rc, err := FetchState(context.Background(), l, id, hc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func state(w http.ResponseWriter, _ *http.Request, _ string) {
	_, _ = io.WriteString(w, `{"version":4}`)
}

func TestFetchStateS3Signed(t *testing.T) {
	f, hc := newFake(t, irsa)
	f.routes["/s3/acme-tf/eu-central-1/prod/app.tfstate"] = state
	f.routes["/s3/acme-tf/eu-west-1/app.tfstate"] = state
	id := Identity{Cloud: CloudAWS, Name: "arn"}
	if got := fetch(t, loc(t, "s3://acme-tf/prod/app.tfstate?region=eu-central-1"), id, hc); got != `{"version":4}` {
		t.Fatalf("body = %q", got)
	}
	r := f.requests[0]
	if a := r.Header.Get("Authorization"); !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") ||
		!strings.Contains(a, "/eu-central-1/s3/aws4_request") || r.Header.Get("X-Amz-Security-Token") != "session" ||
		r.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Errorf("headers = %v", r.Header)
	}
	// With no region in the location the identity's region is used.
	_ = fetch(t, loc(t, "s3://acme-tf/app.tfstate"), id, hc)
	// A dotted bucket is read path-style.
	f.routes["/s3path/eu-west-1/acme.tf-state/prod.tfstate"] = state
	if got := fetch(t, loc(t, "s3://acme.tf-state/prod.tfstate"), id, hc); got != `{"version":4}` {
		t.Fatalf("dotted bucket body = %q", got)
	}
	awsConfig = func(context.Context) (aws.Config, error) {
		return aws.Config{Credentials: staticKeys}, nil
	}
	f.routes["/s3/acme-tf/us-east-1/app.tfstate"] = state
	_ = fetch(t, loc(t, "s3://acme-tf/app.tfstate"), id, hc)
	if _, err := FetchState(context.Background(), loc(t, "s3://acme-tf/app.tfstate"), Identity{Cloud: CloudGCP}, hc); err == nil {
		t.Error("a GCP identity read S3")
	}
	awsConfig = func(context.Context) (aws.Config, error) { return aws.Config{}, errors.New("bad profile") }
	if _, err := FetchState(context.Background(), loc(t, "s3://acme-tf/app.tfstate"), id, hc); err == nil {
		t.Error("a failed config read S3")
	}
	awsConfig = func(context.Context) (aws.Config, error) {
		return aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{}, errors.New("sts denied")
		})}, nil
	}
	if _, err := FetchState(context.Background(), loc(t, "s3://acme-tf/app.tfstate"), id, hc); err == nil {
		t.Error("failed credentials read S3")
	}
	awsConfig = func(context.Context) (aws.Config, error) { return aws.Config{Credentials: staticKeys}, nil }
	endpoints.s3 = "://bad/{bucket}{region}{key}"
	if _, err := FetchState(context.Background(), loc(t, "s3://acme-tf/app.tfstate"), id, hc); err == nil {
		t.Error("a bad endpoint read S3")
	}
}

func TestFetchStateGCS(t *testing.T) {
	f, hc := newFake(t, nil)
	f.routes["/gcs/acme-tf/o/env/prod.tfstate"] = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.Header.Get("Authorization") != "Bearer gcp-token" || r.URL.Query().Get("alt") != "media" || !strings.Contains(r.URL.RawPath, "env%2Fprod.tfstate") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		state(w, r, "")
	}
	id := Identity{Cloud: CloudGCP, Name: gsa}
	if got := fetch(t, loc(t, "gs://acme-tf/env/prod.tfstate"), id, hc); got != `{"version":4}` {
		t.Fatalf("body = %q", got)
	}
	gcpTokenSource = func(context.Context, ...string) (oauth2.TokenSource, error) { return nil, errors.New("no credentials") }
	if _, err := FetchState(context.Background(), loc(t, "gs://acme-tf/env/prod.tfstate"), id, hc); err == nil {
		t.Error("no token source read GCS")
	}
	gcpTokenSource = func(context.Context, ...string) (oauth2.TokenSource, error) {
		return oauth2.ReuseTokenSource(nil, failingTokens{}), nil
	}
	if _, err := FetchState(context.Background(), loc(t, "gs://acme-tf/env/prod.tfstate"), id, hc); err == nil {
		t.Error("a failed token read GCS")
	}
}

type failingTokens struct{}

func (failingTokens) Token() (*oauth2.Token, error) { return nil, errors.New("expired") }

func TestFetchStateAzure(t *testing.T) {
	f, hc := newFake(t, nil)
	f.routes["/blob/acmetf/tfstate/prod.terraform.tfstate"] = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.Header.Get("Authorization") != "Bearer azure-token "+azureStorageScope || r.Header.Get("x-ms-version") != azureBlobVersion {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		state(w, r, "")
	}
	id := Identity{Cloud: CloudAzure, Name: "client-1"}
	if got := fetch(t, loc(t, "azurerm://acmetf/tfstate/prod.terraform.tfstate"), id, hc); got != `{"version":4}` {
		t.Fatalf("body = %q", got)
	}
	if _, err := FetchState(context.Background(), loc(t, "azurerm://acmetf/tfstate/prod.terraform.tfstate"), Identity{Cloud: CloudGCP}, hc); err == nil {
		t.Error("a GCP identity read Azure Blob")
	}
	endpoints.azureBlob = "://bad{account}{container}{key}"
	if _, err := FetchState(context.Background(), loc(t, "azurerm://acmetf/tfstate/prod.terraform.tfstate"), id, hc); err == nil {
		t.Error("a bad endpoint read Azure Blob")
	}
}

func TestFetchStateNotFound(t *testing.T) {
	f, hc := newFake(t, irsa)
	_, err := FetchState(context.Background(), loc(t, "s3://acme-tf/missing.tfstate"), Identity{Cloud: CloudAWS}, hc)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing = %v", err)
	}
	f.routes["/s3/acme-tf/eu-west-1/denied.tfstate"] = func(w http.ResponseWriter, _ *http.Request, _ string) { w.WriteHeader(http.StatusForbidden) }
	if _, err := FetchState(context.Background(), loc(t, "s3://acme-tf/denied.tfstate"), Identity{Cloud: CloudAWS}, hc); err == nil || err.Error() != "status 403" {
		t.Errorf("denied = %v", err)
	}
	endpoints.s3 = "http://127.0.0.1:1/{bucket}{region}{key}"
	if _, err := FetchState(context.Background(), loc(t, "s3://acme-tf/x.tfstate"), Identity{Cloud: CloudAWS}, hc); err == nil {
		t.Error("an unreachable endpoint read")
	}
}

func TestAWSInventoryRefusesNodeRole(t *testing.T) {
	f, hc := newFake(t, nil) // no IRSA, no Pod Identity
	called := false
	awsConfig = func(context.Context) (aws.Config, error) { called = true; return aws.Config{}, nil }
	_, err := Inventory(context.Background(), Identity{Cloud: CloudAWS, Name: "arn"}, externalcatalog.Default(), hc, now)
	if err == nil || !strings.Contains(err.Error(), "node role") || called || len(f.paths()) != 0 {
		t.Errorf("err = %v, config loaded %v, requests %v", err, called, f.paths())
	}
	if _, err := FetchState(context.Background(), loc(t, "s3://acme-tf/app.tfstate"), Identity{Cloud: CloudAWS}, hc); err == nil || called {
		t.Error("the state read without a workload identity")
	}
}

func TestGCPInventoryNeedsAnnotation(t *testing.T) {
	f, hc := newFake(t, nil)
	if _, err := Inventory(context.Background(), Identity{Cloud: CloudGCP}, externalcatalog.Default(), hc, now); err == nil || len(f.paths()) != 0 {
		t.Errorf("err = %v, requests %v", err, f.paths())
	}
}

func TestGCPInventoryRefusesNodeIdentity(t *testing.T) {
	f, hc := newFake(t, nil)
	f.routes["/metadata/email"] = func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = io.WriteString(w, "123-compute@developer.gserviceaccount.com")
	}
	id := Identity{Cloud: CloudGCP, Name: gsa}
	_, err := Inventory(context.Background(), id, externalcatalog.Default(), hc, now)
	if err == nil || err.Error() != "workload identity not active: metadata returns 123-compute@developer.gserviceaccount.com" {
		t.Errorf("err = %v", err)
	}
	if _, err := FetchState(context.Background(), loc(t, "gs://acme-tf/x.tfstate"), id, hc); err == nil {
		t.Error("the state read as the node identity")
	}
	if got := f.paths(); len(got) != 2 || got[0] != "GET /metadata/email" || got[1] != "GET /metadata/email" {
		t.Errorf("requests = %v", got)
	}
	delete(f.routes, "/metadata/email")
	if _, err := Inventory(context.Background(), id, externalcatalog.Default(), hc, now); err == nil {
		t.Error("an unanswered metadata server read")
	}
	// An annotation that is not an email never reaches a project.
	f.routes["/metadata/email"] = func(w http.ResponseWriter, _ *http.Request, _ string) { _, _ = io.WriteString(w, "pacto@acme") }
	if _, err := Inventory(context.Background(), Identity{Cloud: CloudGCP, Name: "pacto@acme"}, externalcatalog.Default(), hc, now); err == nil {
		t.Error("an annotation with no project read")
	}
}

// explorerSearch answers a signed Resource Explorer Search in two pages.
func explorerSearch(w http.ResponseWriter, r *http.Request, body string) {
	var q struct {
		QueryString string
		MaxResults  int
		NextToken   string
	}
	_ = json.Unmarshal([]byte(body), &q)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") || !strings.Contains(r.Header.Get("Authorization"), "/resource-explorer-2/") ||
		!strings.Contains(q.QueryString, "resourcetype:rds:db resourcetype:") || q.MaxResults != MaxInventoryItems {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if q.NextToken == "" {
		_, _ = io.WriteString(w, `{"Resources":[{"Arn":"arn:aws:rds:eu-west-1:123456789012:db:orders-db","ResourceType":"rds:db","Region":"eu-west-1","OwningAccountId":"123456789012"}],"NextToken":"p2"}`)
		return
	}
	_, _ = io.WriteString(w, `{"Resources":[{"Arn":"arn:aws:sqs:eu-west-1:123456789012:events","ResourceType":"sqs:queue","Region":"eu-west-1"},`+
		`{"Arn":"arn:aws:ec2:eu-west-1:123456789012:instance/i-1","ResourceType":"ec2:instance","Region":"eu-west-1"},{"Arn":"bad","ResourceType":"sqs:queue"}]}`)
}

func TestAWSInventorySearch(t *testing.T) {
	f, hc := newFake(t, irsa)
	f.routes["/explorer/eu-west-1/Search"] = explorerSearch
	got, err := Inventory(context.Background(), Identity{Cloud: CloudAWS, Name: "arn"}, externalcatalog.Default(), hc, now)
	if err != nil || len(got) != 3 || got[0].Identity != "aws/rds/123456789012/eu-west-1/orders-db" || got[0].Source != SourceAWS ||
		got[1].Identity != "aws/sqs/123456789012/eu-west-1/events" || got[2].Identity != "aws/sqs" || len(got[2].Missing) != 3 {
		t.Fatalf("inventory = %+v, %v", got, err)
	}
	awsConfig = func(context.Context) (aws.Config, error) { return aws.Config{Credentials: staticKeys}, nil }
	if _, err := Inventory(context.Background(), Identity{Cloud: CloudAWS}, externalcatalog.Default(), hc, now); err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("no region = %v", err)
	}
	awsConfig = func(context.Context) (aws.Config, error) { return aws.Config{}, errors.New("bad profile") }
	if _, err := Inventory(context.Background(), Identity{Cloud: CloudAWS}, externalcatalog.Default(), hc, now); err == nil {
		t.Error("a failed config read the inventory")
	}
}

func TestGCPInventorySearch(t *testing.T) {
	f, hc := newFake(t, nil)
	f.routes["/asset/acme-prod:searchAllResources"] = func(w http.ResponseWriter, r *http.Request, _ string) {
		q := r.URL.Query()
		if r.Header.Get("Authorization") != "Bearer gcp-token" || len(q["assetTypes"]) != 3 || q.Get("pageSize") != "500" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if q.Get("pageToken") == "" {
			_, _ = io.WriteString(w, `{"results":[{"name":"//sqladmin.googleapis.com/projects/acme-prod/instances/orders","assetType":"sqladmin.googleapis.com/Instance","location":"europe-west1","displayName":"orders"}],"nextPageToken":"p2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"results":[{"name":"//pubsub.googleapis.com/projects/acme-prod/topics/events","assetType":"pubsub.googleapis.com/Topic","location":"global"}]}`)
	}
	got, err := Inventory(context.Background(), Identity{Cloud: CloudGCP, Name: gsa}, externalcatalog.Default(), hc, now)
	if err != nil || len(got) != 2 || got[0].Identity != "gcp/cloudsql/acme-prod/europe-west1/orders" || got[0].Source != SourceGCP ||
		got[1].Identity != "gcp/pubsub/acme-prod/global/events" {
		t.Fatalf("inventory = %+v, %v", got, err)
	}
	if p := f.paths(); p[0] != "GET /metadata/email" {
		t.Errorf("the metadata server was not asked first: %v", p)
	}
}

func TestAzureResourceGraph(t *testing.T) {
	f, hc := newFake(t, nil)
	f.routes["/graph"] = func(w http.ResponseWriter, r *http.Request, body string) {
		var q struct {
			Query   string
			Options map[string]any
		}
		_ = json.Unmarshal([]byte(body), &q)
		if r.Header.Get("Authorization") != "Bearer azure-token "+azureManagementScope || r.Method != http.MethodPost ||
			!strings.HasPrefix(q.Query, "Resources | where type in~ ('microsoft.") || !strings.HasSuffix(q.Query, "| project id, type, location, subscriptionId, name") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if q.Options["$skipToken"] == nil {
			_, _ = io.WriteString(w, `{"data":[{"id":"/subscriptions/sub-1/x","type":"microsoft.servicebus/namespaces","location":"westeurope","subscriptionId":"sub-1","name":"orders-bus"}],"$skipToken":"p2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}
	got, err := Inventory(context.Background(), Identity{Cloud: CloudAzure, Name: "client-1"}, externalcatalog.Default(), hc, now)
	if err != nil || len(got) != 1 || got[0].Identity != "azure/servicebus/sub-1/westeurope/orders-bus" || got[0].Source != SourceAzure {
		t.Fatalf("inventory = %+v, %v", got, err)
	}
	azureCredential = func(string) (azcore.TokenCredential, error) { return nil, errors.New("no token") }
	if _, err := Inventory(context.Background(), Identity{Cloud: CloudAzure}, externalcatalog.Default(), hc, now); err == nil {
		t.Error("no credential read the graph")
	}
}

func TestInventoryTruncates(t *testing.T) {
	f, hc := newFake(t, nil)
	f.routes["/graph"] = func(w http.ResponseWriter, _ *http.Request, _ string) {
		rows := make([]map[string]string, 600)
		for i := range rows {
			rows[i] = map[string]string{"type": "microsoft.servicebus/namespaces", "location": "westeurope", "subscriptionId": "sub-1", "name": "bus"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "$skipToken": "more"})
	}
	got, err := Inventory(context.Background(), Identity{Cloud: CloudAzure}, externalcatalog.Default(), hc, now)
	if !errors.Is(err, ErrTruncated) || len(got) != MaxInventoryItems {
		t.Errorf("kept %d, %v", len(got), err)
	}
}

func TestInventoryErrors(t *testing.T) {
	f, hc := newFake(t, nil)
	id := Identity{Cloud: CloudAzure}
	if _, err := Inventory(context.Background(), Identity{Cloud: "oci"}, externalcatalog.Default(), hc, now); err == nil {
		t.Error("an unknown cloud read")
	}
	// A cloud the catalog lists no type for asks nothing.
	empty, err := externalcatalog.ParseFile([]byte("apiVersion: catalog.pacto.trianalab.io/v1\nentries: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	cat := externalcatalog.Merge(empty, externalcatalog.Ref{}, nil, nil)
	if got, err := Inventory(context.Background(), id, cat, hc, now); got != nil || err != nil || len(f.paths()) != 0 {
		t.Errorf("no types = %v, %v, %v", got, err, f.paths())
	}
	for name, h := range map[string]func(http.ResponseWriter, *http.Request, string){
		"not json": func(w http.ResponseWriter, _ *http.Request, _ string) { _, _ = io.WriteString(w, "<html>") },
		"too big": func(w http.ResponseWriter, _ *http.Request, _ string) {
			_, _ = w.Write([]byte(`{"data":"`))
			_, _ = w.Write(make([]byte, maxPageBytes))
		},
		"denied": func(w http.ResponseWriter, _ *http.Request, _ string) { w.WriteHeader(http.StatusForbidden) },
		"truncated": func(w http.ResponseWriter, _ *http.Request, _ string) {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "{")
		},
	} {
		f.routes["/graph"] = h
		if _, err := Inventory(context.Background(), id, externalcatalog.Default(), hc, now); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	endpoints.azureGraph = "://bad"
	if _, err := Inventory(context.Background(), id, externalcatalog.Default(), hc, now); err == nil {
		t.Error("a bad endpoint read")
	}
	endpoints.awsExplorer = "://bad{region}"
	getenv = func(k string) string { return irsa[k] }
	if _, err := Inventory(context.Background(), Identity{Cloud: CloudAWS}, externalcatalog.Default(), hc, now); err == nil {
		t.Error("a bad AWS endpoint read")
	}
	if _, _, err := decodeAWS([]byte("x")); err == nil {
		t.Error("bad AWS answer")
	}
	if _, _, err := decodeGCP([]byte("x"), "p"); err == nil {
		t.Error("bad GCP answer")
	}
}

// TestInventoryKeepsOnlyTypeIDRegion: rows carry tags, properties, labels
// and attributes with secret-looking values; none survives a read.
func TestInventoryKeepsOnlyTypeIDRegion(t *testing.T) {
	const secret = "s3cr3t-hunter2"
	f, hc := newFake(t, irsa)
	f.routes["/explorer/eu-west-1/Search"] = func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = io.WriteString(w, `{"Resources":[{"Arn":"arn:aws:rds:eu-west-1:123456789012:db:orders-db","ResourceType":"rds:db","Region":"eu-west-1",`+
			`"Properties":[{"Name":"tags","Data":[{"Key":"password","Value":"`+secret+`"}]}]}]}`)
	}
	f.routes["/asset/acme-prod:searchAllResources"] = func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = io.WriteString(w, `{"results":[{"name":"//x/orders","assetType":"sqladmin.googleapis.com/Instance","location":"europe-west1",`+
			`"labels":{"token":"`+secret+`"},"additionalAttributes":{"connection":"`+secret+`"}}]}`)
	}
	f.routes["/graph"] = func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = io.WriteString(w, `{"data":[{"type":"microsoft.sql/servers/databases","location":"westeurope","subscriptionId":"sub-1","name":"orders",`+
			`"tags":{"key":"`+secret+`"},"properties":{"administratorLoginPassword":"`+secret+`"}}]}`)
	}
	for _, id := range []Identity{{Cloud: CloudAWS}, {Cloud: CloudGCP, Name: gsa}, {Cloud: CloudAzure}} {
		got, err := Inventory(context.Background(), id, externalcatalog.Default(), hc, now)
		b, _ := json.Marshal(got)
		if err != nil || len(got) != 1 || strings.Contains(string(b), secret) {
			t.Errorf("%s: %s, %v", id.Cloud, b, err)
		}
	}
}
