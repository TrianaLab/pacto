# Sample Scenarios

## Quick start: the demo platform

The full "Acme E-Commerce Platform" demo is the Kustomize sample in
`config/samples/demo`: the same services in the `pacto-demo` namespace, with
contracts pulled from OCI bundles that carry real version history (the
`payments-service` drift and its breaking change included). Apply it with:

```bash
make deploy-samples
```

The scenarios below are smaller, self-contained inline examples. Apply
`00-infrastructure.yaml` first, then any scenario file.

## Scenarios

| File | Description |
|------|-------------|
| `00-infrastructure.yaml` | Kubernetes Service/Deployment stubs for all scenarios |
| `01-demo-oci.yaml` | Real OCI contracts from `ghcr.io/trianalab/pacto` |
| `02-microservices.yaml` | E-commerce services with inline contracts and interface bindings |
| `03-stateful-infrastructure.yaml` | Stateful services (postgres, redis, rabbitmq) |
| `04-platform-standards.yaml` | Definition-only contracts and cross-references |
| `05-advanced-patterns.yaml` | Hybrid state, scheduled jobs, event consumers |
| `06-error-scenarios.yaml` | An invalid contract (`Invalid`) plus a missing target and an unmappable port (`Unknown`) |
| `08-runtime-conformance.yaml` | A Ready workload that listens on nothing, caught only by the node agent |

## Runtime conformance (Scenario 08)

`deaf-api` is Ready and its Service has an endpoint, but nothing in the pod
listens on 8080. With the node agent watching every pod's sockets with eBPF, the
operator reports `INTERFACE_ABSENT` on `api`, seen by `k8s-observer+ebpf`
("Kubernetes + kernel" in the dashboard), and the Pacto turns `NonCompliant`.
Install the operator with:

```bash
helm upgrade --install pacto-operator <chart> -n pacto-system \
  --set nodeAgent.enabled=true --set controller.enableProbing=true
kubectl apply -f config/samples/scenarios/08-runtime-conformance.yaml
```

Without `nodeAgent.enabled` the same Pacto reconciles `Compliant`: no Kubernetes
API can tell an open socket from a missing one.

## Inline contracts

Every inline contract here is `pactoVersion: "2.0"`. Ports are not part of the
contract: each Pacto maps its interfaces to Service ports with
`spec.target.interfaceBindings`, and an unbound interface reads `Unknown`.
Inline contracts carry no bundle files, so interface specs, configuration
schemas and ref-based policies are not resolved; publish a bundle to OCI for the
full checks.

To check one locally, save the inline YAML as `pacto.yaml` in an empty
directory, add the files its `ref` and `schema` fields name, and run
`pacto validate <dir>`.
