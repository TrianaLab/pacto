package impact

import "time"

// The states of a judged acceptance record. Only a counted one accepts:
// routed, proposal, expired and ignored records are listed with why, and
// never change a verdict.
const (
	// AcceptCounted is a verified record a person who declares the accepting
	// service's ownership signed, for a break that has not expired.
	AcceptCounted = "counted"
	// AcceptRouted is a person's record who is not the declared owner: it
	// waits for the owner.
	AcceptRouted = "routed"
	// AcceptProposal is a record a non-person (CI, an agent or a workload
	// identity) signed: a proposal until a person accepts it.
	AcceptProposal = "proposal"
	// AcceptExpired is a break acceptance whose announcement expired or
	// records no expiry.
	AcceptExpired = "expired"
	// AcceptIgnored is a record that does not verify or does not belong to
	// the subject it is attached to.
	AcceptIgnored = "ignored"
)

// Acceptance is one acceptance record attached to a consumer's revision,
// as the app read and judged it.
type Acceptance struct {
	State    string    `json:"state" enum:"counted,routed,proposal,expired,ignored" doc:"How the record was judged; only counted accepts"`
	By       string    `json:"by,omitempty" doc:"Who signed the record: the certificate's subject"`
	Issuer   string    `json:"issuer,omitempty" doc:"The OIDC issuer of the signer's certificate"`
	Record   string    `json:"record,omitempty" doc:"Where the record is: <repository>@sha256:<hex>"`
	Reason   string    `json:"reason,omitempty" doc:"Why the record does not count, or the route to the owner who may accept"`
	SignedAt time.Time `json:"signedAt,omitzero" doc:"When a timestamp authority or the transparency log saw the record signed"`
	Break    *BreakRef `json:"break,omitempty" doc:"The break the record accepts, for a break acceptance"`
}

// BreakRef names one announced break: the provider (fleet key), the version
// that breaks and the digest of the provider's breaks[] entry naming the
// consumer.
type BreakRef struct {
	Provider     string `json:"provider" doc:"The provider's fleet key"`
	Version      string `json:"version" doc:"The provider version the break ships in"`
	Announcement string `json:"announcement" doc:"The sha256 of the provider's breaks[] entry naming the consumer"`
}
