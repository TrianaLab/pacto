# pacto-dev-gateway

A **local-development** API gateway for the Pacto dashboard and Evidence Server.

It installs the [Envoy Gateway](https://gateway.envoyproxy.io/) controller (a
Gateway API implementation) plus a `GatewayClass`, a `Gateway`, and an
`EnvoyProxy` that exposes the data plane on a fixed NodePort — so the
`pacto-operator` chart's dashboard/Evidence `HTTPRoute`s have a controller to
attach to and you get a stable `http://localhost:...` URL instead of
`kubectl port-forward`.

> This chart is a developer convenience. It is **not** part of the production
> Pacto packaging, is not published to Artifact Hub, and must not be installed in
> a production cluster.

## Install (kind)

Create the cluster with the NodePort bridged to localhost:

```sh
kind create cluster --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: 8080
EOF

helm dependency build integrations/kubernetes/charts/pacto-dev-gateway
helm install pacto-dev-gateway integrations/kubernetes/charts/pacto-dev-gateway -n pacto-system --create-namespace
```

Then install `pacto-operator` with its dashboard `HTTPRoute` pointed at the
`pacto-dev` Gateway (see the chart NOTES for the exact `--set` flags) and open
<http://localhost:8080/>.

## Values

| Key | Default | Description |
| --- | --- | --- |
| `envoyGateway.enabled` | `true` | Install the Envoy Gateway controller. Set `false` to reuse an already-installed controller. |
| `gateway.name` | `pacto-dev` | Gateway name the operator's HTTPRoutes reference. |
| `gateway.className` | `pacto-envoy` | GatewayClass name. |
| `gateway.port` | `80` | HTTP listener port. |
| `gateway.allowRoutesFromAllNamespaces` | `true` | Accept HTTPRoutes from any namespace (dev convenience). |
| `envoyService.type` | `NodePort` | Envoy data-plane Service type. Use `LoadBalancer` if the cluster has an LB. |
| `envoyService.httpNodePort` | `30080` | Fixed nodePort for the HTTP listener (pairs with the kind port mapping above). |
