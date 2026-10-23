# HANDOFF from the demok8s track (wave 2)

Requests outside this track's files:

1. examples/story (story owner): the payments deploy files run podinfo with
   `args: [--port=8080]`, but the podinfo image has no entrypoint, so the
   container cannot start ("exec: --port=8080: executable file not found").
   Change it to `command: [./podinfo, --port=8080]` in
   `contracts/payments/{2.3.0,2.4.0,2.4.2,2.4.3,3.0.0}/deploy/payments.yaml`
   (and the same in checkout and orders). Then drop the `ponytail:` perl
   rewrite in `tests/acceptance/kind/delivery-demo.sh` checkout_version.
2. examples/story (optional): the demo adds two things to each version's
   working copy that the story could carry itself, so the Kubernetes copy
   would differ from the story only in the registry host:
   - `deploy/pacto-resource.yaml`, the Pacto resource (what `pacto init
     --delivery` writes), with `checkIntervalSeconds: 30` and, for payments,
     `serviceName: payments` plus `interfaceBindings: [{interface: http,
     servicePort: 8080}]` (without the binding the http interface cannot be
     observed and the contract check stays Unknown);
   - for payments 2.4.2, a `logger` sidecar that calls `http://fraud:8080/score`
     every 2 seconds: the call the B3 claim check catches.
3. docs (docs owner): link `docs/examples/kubernetes-demo.md` from
   `integrations/kubernetes/docs/delivery.md`, and add the page to the
   docs-check pin list if the integrator wants its commands pinned.
4. ledger section G "Heavy Kubernetes demo": `.github/workflows/delivery-demo.yml`
   now runs `make e2e-delivery-demo` with no "target missing" gate;
   `tests/architecture/kind_image_loading_test.go` TestEveryKindScenarioRuns
   accepts a scenario the nightly workflow runs as `make e2e-<target>`.
5. OWNERSHIP DEVIATION, please accept or move (commit d032a1ab, operator
   observer, `integrations/kubernetes/internal/observer/dependencies.go`): a
   dependency now resolves to the provider's Pacto in the consumer's own
   namespace first. Before, payments running in both test and prod (exactly
   what `pacto init --delivery` sets up in one cluster) made every consumer's
   required dependency ambiguous, so checkout's contract check stayed Unknown
   (EVIDENCE_INSUFFICIENT) and nothing that depends on anything could ship
   without a person. Regression test TestDependency_OwnNamespaceWins; the
   observer stays at 100.0%. The demo cannot run without it.
6. OWNERSHIP DEVIATION, please accept or move (core, `internal/pactify`):
   the Pacto resource `pacto init --delivery` writes now names the workload's
   Service (`target.serviceName`) when the folder has a Service of the same
   name. Without it the operator never counts the service as a peer, so after
   pactifying fraud the undeclared-call check could not catch payments 2.4.2
   calling it (ledger F: "the agent pactifies fraud, then the undeclared-call
   check can catch it"). Tests in pactify_test.go; internal/pactify stays at
   100.0%. A Service named differently from its workload is not matched yet
   (a `ponytail:` note says how to add it).
7. OWNERSHIP DEVIATION, please accept or move (operator observer,
   `integrations/kubernetes/internal/observer/kernel.go` targetPods): pods that
   are shutting down (deletionTimestamp set) are no longer part of the kernel
   view. Before, the rollback that B3 raises was judged on the 2.4.2 pod still
   terminating, so the claim check failed the rollback itself ("concluded
   Failed, VerifyFailed: claims ... DEPENDENCY_UNDECLARED dependency/fraud").
   TestTargetPods covers it; the observer stays at 100.0%.
8. core (pacto init) and logic (promoter): the Kustomization `pacto init
   --delivery` writes has no health check, so Flux reports a version applied
   before its rollout is done and the verify gates can judge the old pods.
   The demo adds `healthChecks: [{apiVersion: apps/v1, kind: Deployment, ...}]`
   to its own Kustomizations (as delivery.sh does). Suggest init writes the
   same for the workload it found, or the promoter treats "applied" as "rolled
   out" (Deployment updatedReplicas == availableReplicas == replicas).
9. core (pacto init): the pactify act patches two things `pacto init
   --delivery` writes for fraud, so the demo can sign and pull:
   - `transparencyLog: false` under `publish.sign` in the generated
     pacto.yaml (the story signs offline);
   - `spec.insecure: true` on the three OCIRepositories it writes (`fraud` in
     test and prod, `fraud-candidates` in test), because the demo registry
     speaks plain HTTP.
   Suggest init takes hints for both (for example `--insecure-registry` and
   `--offline-signing`, or reads them from an existing publish config), and
   the demo then drops its patches. The demo now checks that init's Pacto
   resource for fraud exists in test.
10. ledger F and G (integrator): the Kubernetes demo now checks B1 to B9
    against examples/story (B6 to B9 in `make e2e-delivery-demo-browser`,
    B7 to B9 partly through the dashboard in Chromium). B10 (a tampered record
    freezes payments) is not played on Kubernetes; record that note on G
    ("checks every story step itself") until a later wave adds it.
