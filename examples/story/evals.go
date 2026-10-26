package story

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// RunnerName is the name the story's trust config gives its eval runner.
const RunnerName = "story-evals"

// RunnerKey is the story eval runner's signing key. It is derived from a fixed
// phrase so the story is the same every time: anyone can derive it, so it
// proves nothing outside the story.
func RunnerKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("pacto story: the stand-in eval runner"))
	return ed25519.NewKeyFromSeed(seed[:])
}

// RunnerTrust is the runner key as a gate provider's signers list it.
func RunnerTrust() delivery.TrustedKey {
	pub, _ := delivery.PublicKeyPEM(RunnerKey().Public()) // an ed25519 key always marshals
	return delivery.TrustedKey{Name: RunnerName, PublicKey: pub}
}

// SignEval is the answer the story's eval runner gives: a Sigstore bundle
// whose DSSE envelope carries the statement that, asked by operation, it ran
// suite on subject and counted passed of total, signed with RunnerKey. It needs only the standard
// library, so the browser demo signs too.
func SignEval(operation, subject, suite, run string, passed, total int) []byte {
	algo, digest, _ := strings.Cut(subject, ":")
	payload, _ := json.Marshal(delivery.EvalStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateEval,
		Subject:   []delivery.Subject{{Name: "pay-agent", Digest: map[string]string{algo: digest}}},
		Predicate: delivery.EvalResult{Operation: operation, Suite: suite, Run: run, Passed: passed, Total: total, Message: "counted by the story's stand-in runner"}})
	// DSSE signs the pre-authentication encoding of the payload and its type.
	pae := fmt.Sprintf("DSSEv1 %d %s %d %s", len(delivery.PayloadType), delivery.PayloadType, len(payload), payload)
	key := RunnerKey()
	der, _ := x509.MarshalPKIXPublicKey(key.Public())
	hint := sha256.Sum256(der)
	b, _ := json.Marshal(map[string]any{
		"mediaType":            delivery.BundleMediaType,
		"verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": hex.EncodeToString(hint[:])}},
		"dsseEnvelope": map[string]any{"payload": payload, "payloadType": delivery.PayloadType,
			"signatures": []map[string]any{{"sig": ed25519.Sign(key, []byte(pae))}}},
	})
	return b
}
