//go:build kms || kms_azure

package signing

// The azure KMS provider is opt-in: build with -tags kms (every provider) or
// -tags kms_azure. The default pacto CLI leaves cloud KMS SDKs out to stay small.
import _ "github.com/sigstore/sigstore/pkg/signature/kms/azure"
