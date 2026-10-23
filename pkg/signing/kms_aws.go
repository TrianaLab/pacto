//go:build kms || kms_aws

package signing

// The aws KMS provider is opt-in: build with -tags kms (every provider) or
// -tags kms_aws. The default pacto CLI leaves cloud KMS SDKs out to stay small.
import _ "github.com/sigstore/sigstore/pkg/signature/kms/aws"
