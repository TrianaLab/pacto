//go:build kms || kms_gcp

package signing

// The gcp KMS provider is opt-in: build with -tags kms (every provider) or
// -tags kms_gcp. The default pacto CLI leaves cloud KMS SDKs out to stay small.
import _ "github.com/sigstore/sigstore/pkg/signature/kms/gcp"
