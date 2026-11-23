package cloud

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/trianalab/pacto/v4/pkg/managedres"
)

// OAuth scopes and the Blob API version of the state reads.
const (
	gcsScope          = "https://www.googleapis.com/auth/devstorage.read_only"
	azureStorageScope = "https://storage.azure.com/.default"
	azureBlobVersion  = "2023-11-03"
)

// FetchState opens the state at loc through the workload identity id, with
// hc: an S3 GET signed with SigV4, a Cloud Storage media GET or an Azure Blob
// GET, each with a token of id. The caller closes the body and bounds it
// (managedres.ParseState reads at most MaxStateBytes).
func FetchState(ctx context.Context, loc managedres.Location, id Identity, hc *http.Client) (io.ReadCloser, error) {
	req, err := stateRequest(ctx, loc, id, hc)
	if err != nil {
		return nil, err
	}
	body, err := do(hc, req)
	if errors.Is(err, errNotFound) {
		return nil, errors.New("not found, or the identity may not read it")
	}
	return body, err
}

// stateRequest is the authorized GET of loc.
func stateRequest(ctx context.Context, loc managedres.Location, id Identity, hc *http.Client) (*http.Request, error) {
	switch loc.Scheme {
	case managedres.SchemeS3:
		return s3Request(ctx, loc, id)
	case managedres.SchemeGCS:
		tok, err := gcpToken(ctx, id, hc, gcsScope)
		if err != nil {
			return nil, err
		}
		return bearer(ctx, http.MethodGet, endpoint(endpoints.gcs, "{bucket}", loc.Host, "{key}", url.PathEscape(loc.Key)), tok, nil)
	default: // ParseLocation admits no other scheme
		tok, err := azureToken(ctx, id, azureStorageScope)
		if err != nil {
			return nil, err
		}
		req, err := bearer(ctx, http.MethodGet, endpoint(endpoints.azureBlob, "{account}", loc.Host, "{container}", loc.Container, "{key}", escapePath(loc.Key)), tok, nil)
		if err == nil {
			req.Header.Set("x-ms-version", azureBlobVersion)
		}
		return req, err
	}
}

// s3Request is the SigV4-signed GET of an S3 state, in the location's
// region, else the identity's, else us-east-1.
func s3Request(ctx context.Context, loc managedres.Location, id Identity) (*http.Request, error) {
	if id.Cloud != CloudAWS {
		return nil, fmt.Errorf("an S3 state needs an AWS workload identity, have %q", id.Cloud)
	}
	creds, region, err := awsCredentials(ctx)
	if err != nil {
		return nil, err
	}
	region = cmp.Or(loc.Region, region, "us-east-1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(endpoints.s3, "{bucket}", loc.Host, "{region}", region, "{key}", escapePath(loc.Key)), nil)
	if err != nil {
		return nil, err
	}
	return req, sign(ctx, req, creds, nil, "s3", region)
}

// sign adds the SigV4 signature of body to req.
func sign(ctx context.Context, req *http.Request, creds aws.Credentials, body []byte, service, region string) error {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", hash)
	return v4.NewSigner().SignHTTP(ctx, creds, req, hash, service, region, time.Now())
}

// bearer is a request carrying tok as a bearer token.
func bearer(ctx context.Context, method, u, tok string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return req, nil
}

// escapePath escapes each segment of a /-separated key.
func escapePath(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
