//go:build kms || kms_hashivault

package signing

// The hashivault KMS provider is opt-in: build with -tags kms (every provider) or
// -tags kms_hashivault. The default pacto CLI leaves cloud KMS SDKs out to stay small.
import _ "github.com/sigstore/sigstore/pkg/signature/kms/hashivault"
