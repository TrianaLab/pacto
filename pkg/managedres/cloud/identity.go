// Package cloud reads remote Terraform states and the cloud inventory for
// the operator, only through the read-only workload identity bound to its
// ServiceAccount: the AWS client never falls back to the node's instance
// role, the GCP client refuses when the metadata server answers with
// another identity and the Azure client uses workload identity only. It
// issues read calls only. Only the operator imports it: it links the cloud
// SDKs, which the CLI, the dashboard and the WASM demo never need.
package cloud

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"golang.org/x/oauth2/google"
)

// The clouds controller.cloudInventory and --workload-identity name.
const (
	CloudAWS   = "aws"
	CloudGCP   = "gcp"
	CloudAzure = "azure"
)

// Identity is the workload identity of one cloud: Name is the role ARN, the
// Google service account email or the Azure client id; Reason says where it
// was found, or why none holds.
type Identity struct {
	Cloud, Name, Reason string
}

// String names the identity in a row.
func (id Identity) String() string { return id.Cloud + " identity " + id.Name }

// The environment the identity webhooks inject.
const (
	envAWSWebIdentity = "AWS_WEB_IDENTITY_TOKEN_FILE"        // EKS IRSA
	envAWSPodIdentity = "AWS_CONTAINER_CREDENTIALS_FULL_URI" // EKS Pod Identity
	envAzureToken     = "AZURE_FEDERATED_TOKEN_FILE"         // Azure workload identity
)

// The credential sources and endpoints sit behind package variables so
// tests swap in fakes and never reach a token or metadata endpoint. None of
// the defaults is a function literal, so every default line runs in a test.
var (
	getenv           = os.Getenv
	awsConfig        = defaultAWSConfig
	gcpTokenSource   = google.DefaultTokenSource
	azureCredential  = defaultAzureCredential
	gcpMetadataEmail = defaultGCPMetadataEmail
	endpoints        = struct{ s3, s3Path, gcs, azureBlob, awsExplorer, gcpAsset, azureGraph, gcpMetadata string }{
		s3: "https://{bucket}.s3.{region}.amazonaws.com/{key}",
		// A bucket name with a dot adds DNS labels the S3 wildcard
		// certificate does not cover, so it is read path-style.
		s3Path:      "https://s3.{region}.amazonaws.com/{bucket}/{key}",
		gcs:         "https://storage.googleapis.com/storage/v1/b/{bucket}/o/{key}?alt=media",
		azureBlob:   "https://{account}.blob.core.windows.net/{container}/{key}",
		awsExplorer: "https://resource-explorer-2.{region}.amazonaws.com/Search",
		gcpAsset:    "https://cloudasset.googleapis.com/v1/projects/{project}:searchAllResources",
		azureGraph:  "https://management.azure.com/providers/Microsoft.ResourceGraph/resources?api-version=2022-10-01",
		gcpMetadata: "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/email",
	}
)

// endpoint is the endpoint template with each {key} replaced by its value.
func endpoint(template string, kv ...string) string {
	return strings.NewReplacer(kv...).Replace(template)
}

// noIdentity is the reason no workload identity holds.
const noIdentity = "no read-only workload identity on the operator ServiceAccount"

// WorkloadIdentity is the workload identity of the operator: the
// --workload-identity=<cloud>=<value> flag the chart renders from the
// ServiceAccount annotation, else what the identity webhooks injected into
// the environment (EKS IRSA or Pod Identity, Azure workload identity). An
// AWS or Azure annotation the webhook has not acted on does not hold: the
// pod has no token for it. GCP has no environment of its own, so only its
// annotation counts, and the metadata server is asked before every call.
func WorkloadIdentity(flag string, getenv func(string) string) (Identity, bool) {
	if flag != "" {
		return flagIdentity(flag, getenv)
	}
	switch {
	case getenv(envAWSWebIdentity) != "":
		return Identity{Cloud: CloudAWS, Name: cmp.Or(getenv("AWS_ROLE_ARN"), "EKS IRSA"), Reason: "EKS IRSA environment"}, true
	case getenv(envAWSPodIdentity) != "":
		return Identity{Cloud: CloudAWS, Name: "EKS Pod Identity", Reason: "EKS Pod Identity environment"}, true
	case getenv(envAzureToken) != "":
		return Identity{Cloud: CloudAzure, Name: getenv("AZURE_CLIENT_ID"), Reason: "Azure workload identity environment"}, true
	}
	return Identity{Reason: noIdentity}, false
}

// flagIdentity reads <cloud>=<value>.
func flagIdentity(flag string, getenv func(string) string) (Identity, bool) {
	cloud, name, _ := strings.Cut(flag, "=")
	id := Identity{Cloud: cloud, Name: name, Reason: "serviceAccount annotation"}
	switch {
	case name == "":
		return Identity{Reason: noIdentity}, false
	case cloud == CloudAWS && getenv(envAWSWebIdentity) == "" && getenv(envAWSPodIdentity) == "":
		id.Reason = "workload identity not active: the pod has no EKS IRSA or Pod Identity token"
		return id, false
	case cloud == CloudAzure && getenv(envAzureToken) == "":
		id.Reason = "workload identity not active: the pod has no Azure federated token"
		return id, false
	case cloud == CloudGCP:
		if _, err := gcpProject(name); err != nil {
			id.Reason = err.Error()
			return id, false
		}
	case cloud != CloudAWS && cloud != CloudAzure:
		return Identity{Reason: fmt.Sprintf("--workload-identity names cloud %q; want aws, gcp or azure", cloud)}, false
	}
	return id, true
}

// gcpProject is the project of a Google service account email,
// <name>@<project>.iam.gserviceaccount.com.
func gcpProject(email string) (string, error) {
	_, host, ok := strings.Cut(email, "@")
	project, ok2 := strings.CutSuffix(host, ".iam.gserviceaccount.com")
	if !ok || !ok2 || project == "" || strings.ContainsAny(project, "./@") {
		return "", fmt.Errorf("%q is not a Google service account email", email)
	}
	return project, nil
}

// defaultAWSConfig is the default AWS chain with the instance metadata
// client disabled: the node's instance role is never a source.
func defaultAWSConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, config.WithEC2IMDSClientEnableState(imds.ClientDisabled))
}

// awsCredentials are the credentials of an AWS workload identity; with no
// IRSA or Pod Identity token in the environment nothing is asked.
func awsCredentials(ctx context.Context) (aws.Credentials, string, error) {
	if getenv(envAWSWebIdentity) == "" && getenv(envAWSPodIdentity) == "" {
		return aws.Credentials{}, "", errors.New("refusing to read without EKS IRSA or Pod Identity: the node role is never used")
	}
	cfg, err := awsConfig(ctx)
	if err != nil {
		return aws.Credentials{}, "", fmt.Errorf("aws credentials: %w", err)
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return aws.Credentials{}, "", fmt.Errorf("aws credentials: %w", err)
	}
	return creds, cfg.Region, nil
}

// defaultAzureCredential is the Azure workload identity of clientID; it
// never falls back to a managed or developer identity.
func defaultAzureCredential(clientID string) (azcore.TokenCredential, error) {
	return azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{ClientID: clientID})
}

// azureToken is an Azure workload identity token of id for scope.
func azureToken(ctx context.Context, id Identity, scope string) (string, error) {
	if id.Cloud != CloudAzure {
		return "", fmt.Errorf("an Azure read needs an Azure workload identity, have %q", id.Cloud)
	}
	cred, err := azureCredential(id.Name)
	if err != nil {
		return "", fmt.Errorf("azure credentials: %w", err)
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return "", fmt.Errorf("azure credentials: %w", err)
	}
	return tok.Token, nil
}

// defaultGCPMetadataEmail asks the metadata server which service account
// the pod's default credentials are.
func defaultGCPMetadataEmail(ctx context.Context, hc *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoints.gcpMetadata, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	body, err := do(hc, req)
	if err != nil {
		return "", fmt.Errorf("gcp metadata: %w", err)
	}
	defer func() { _ = body.Close() }()
	b, err := io.ReadAll(io.LimitReader(body, 512))
	return strings.TrimSpace(string(b)), err
}

// gcpToken is a token of the annotated Google service account for scope,
// asked only once the metadata server says the pod runs as it: on a node
// pool without GKE Workload Identity it answers with the node's account.
func gcpToken(ctx context.Context, id Identity, hc *http.Client, scope string) (string, error) {
	if id.Cloud != CloudGCP || id.Name == "" {
		return "", errors.New("a GCP read needs the iam.gke.io/gcp-service-account annotation on the operator ServiceAccount")
	}
	email, err := gcpMetadataEmail(ctx, hc)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(email, id.Name) {
		return "", fmt.Errorf("workload identity not active: metadata returns %s", email)
	}
	ts, err := gcpTokenSource(ctx, scope)
	if err != nil {
		return "", fmt.Errorf("gcp credentials: %w", err)
	}
	tok, err := ts.Token()
	if err != nil {
		return "", fmt.Errorf("gcp credentials: %w", err)
	}
	return tok.AccessToken, nil
}

// do sends req and returns the body of a 200 answer; any other status is an
// error that names it and never the body.
func do(hc *http.Client, req *http.Request) (io.ReadCloser, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	return nil, fmt.Errorf("status %d", resp.StatusCode)
}

// errNotFound is a 404 answer.
var errNotFound = errors.New("not found")
