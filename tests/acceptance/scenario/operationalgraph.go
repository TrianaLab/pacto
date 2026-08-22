package scenario

// OperationalGraph is the fixture the full Pacto vertical is proved against: an
// operator reconciling real published contract revisions, a dashboard serving
// them, an offline observation source carrying the matching call, and a signed
// EvidenceEnvelope arriving from outside the cluster.
//
// Everything downstream reasons about REAL published content, not a synthesized
// shortcut. Checkout is published twice, differing by exactly one deterministic
// semantic change, with only the first revision deployed — which is what makes
// the A -> B change analysis a real question about the fleet rather than a
// fixture. Orders DECLARES the dependency on checkout, and the observation source
// carries the call that corroborates it, so declared and observed have to meet in
// the backend and be reconciled there rather than here.
var OperationalGraph = Scenario{
	Name: "operational-graph",
	Journey: Journey{
		Provider: "checkout",
		Consumer: "orders",
		External: "payments",
	},
	Services: []Service{{
		// Payments is the subject of the remote evidence. Its bundle is published
		// so the envelope's ContractRef resolves to real content, but the Product
		// only ever learns about payments from the Evidence Server.
		Name:         "payments",
		Repo:         "payments",
		EvidenceOnly: true,
		Revisions: []Revision{{
			Version: "1.0.0",
			Dir:     "payments",
			Files: map[string]string{
				"pacto.yaml": `pactoVersion: "2.0"
service: { name: payments, version: "1.0.0" }
interfaces: [ { name: api, type: openapi, ref: openapi.yaml, visibility: public } ]
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
`,
				"openapi.yaml": `openapi: "3.0.0"
info: { title: payments, version: "1.0.0" }
paths: {}
`,
			},
		}},
	}, {
		Name: "checkout",
		Repo: "checkout",
		// checkout's contract declares a PUBLIC interface, and interface
		// availability is always a required assertion. Without a Service and a
		// binding the operator has nothing to observe it against, so the only
		// honest verdict it could reach is Unknown. Declaring the port here is what
		// makes checkout's Compliant observed rather than assumed.
		Workload: &Workload{Name: "checkout", Interface: "api", Port: 8080},
		Revisions: []Revision{{
			Version:  "1.0.0",
			Dir:      "checkout-a",
			Deployed: true,
			Files: map[string]string{
				"pacto.yaml": `pactoVersion: "2.0"
service: { name: checkout, version: "1.0.0", owner: { team: commerce, dri: d, contacts: [ { type: email, value: a@e.com, purpose: escalation } ] } }
interfaces: [ { name: api, type: openapi, ref: openapi.yaml, visibility: public } ]
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
`,
				"openapi.yaml": `openapi: "3.0.0"
info: { title: checkout, version: "1.0.0" }
paths:
  /checkout: { post: { responses: { "200": { description: ok } } } }
  /cart: { get: { responses: { "200": { description: ok } } } }
`,
			},
		}, {
			// Revision B is revision A with ONE change: the /cart path is gone. Both
			// files are written out in full rather than derived from A, so the change
			// under analysis is readable here instead of hidden in a transformation.
			// The real diff engine classifies a removed OpenAPI path as Breaking;
			// nothing else about the service moves.
			Version: "1.1.0",
			Dir:     "checkout-b",
			Files: map[string]string{
				"pacto.yaml": `pactoVersion: "2.0"
service: { name: checkout, version: "1.1.0", owner: { team: commerce, dri: d, contacts: [ { type: email, value: a@e.com, purpose: escalation } ] } }
interfaces: [ { name: api, type: openapi, ref: openapi.yaml, visibility: public } ]
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
`,
				"openapi.yaml": `openapi: "3.0.0"
info: { title: checkout, version: "1.1.0" }
paths:
  /checkout: { post: { responses: { "200": { description: ok } } } }
`,
			},
		}},
	}, {
		Name: "orders",
		Repo: "orders",
		// orders declares no public interface, so it runs unexposed: a Deployment
		// and a CR target, no Service and no binding.
		Workload: &Workload{Name: "orders"},
		Revisions: []Revision{{
			Version:  "1.0.0",
			Dir:      "orders",
			Deployed: true,
			Files: map[string]string{
				"pacto.yaml": `pactoVersion: "2.0"
service: { name: orders, version: "1.0.0", owner: { team: commerce, dri: d, contacts: [ { type: email, value: a@e.com, purpose: escalation } ] } }
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
dependencies: [ { name: checkout, ref: 'oci://{{.Domain}}/checkout', required: false, compatibility: '^1.0.0' } ]
`,
			},
		}},
	}},
	Sources: []Source{
		{ID: "oci", Kind: SourceRegistry},
		{ID: "cache", Kind: SourceCache},
		// The declarative form: a named source the OPERATOR mounts read-only
		// into the dashboard it manages, not an ad-hoc positional traces path. The
		// Product has to show it as a Data Source with this stable identity.
		{ID: "orders-traces", Kind: SourceObservation},
		{ID: "evidence-http", Kind: SourceEvidence},
	},
	Relationships: []Relationship{{
		From:           "orders",
		To:             "checkout",
		Declared:       true,
		ObservedBy:     "orders-traces",
		Reconciliation: "matched",
	}},
	Evidence: []Evidence{{
		Service: "payments",
		// The environment the observations were collected in, and — separately —
		// the identity that signs the envelope carrying them. The trust store the
		// harness installs binds the key to this producer, so an envelope claiming
		// any other id is rejected at ingestion.
		Source:     "remote-eu",
		Signer:     Signer{Producer: "remote-eu-collector", KeyID: "demo"},
		ObservedAt: "2026-07-29T12:00:00Z",
		Via:        "evidence-http",
	}},
}
