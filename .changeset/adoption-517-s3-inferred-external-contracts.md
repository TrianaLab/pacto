---
"@pacto/core": minor
"@pacto/k8s-module": minor
"@pacto/demo-compose": minor
---

Pacto publishes an inferred contract for every external system your services call. Once `registry` is set the operator writes `<registry>/pacto-inferred/<identity>` every 5 minutes, signed with the installation key and annotated `run.pacto.contract.provenance: inferred`, and only when the hosts, ports or client majors it sees change; `pacto fleet publish-inferred` does the same where no operator runs. The 2.5 contract field `service.external` names the system and the `pacto.inferred.yaml` section `surface` records what Pacto saw with its sources, catalog digest and first and last seen; it never names a consumer. A range against an inferred contract reads `rangeStatus: unknown` with the new `rangeReason: INFERRED_PROVIDER` and gates as unverified. A revision only the operator signed reads `unverified` with the new signature source `operator` and an `INFERRED_PROVIDER` attention item suggests `pacto declare`. The Compose demo publishes `demo/pacto-inferred/stripe`.
