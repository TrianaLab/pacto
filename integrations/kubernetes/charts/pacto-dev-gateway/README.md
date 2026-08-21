# pacto-dev-gateway

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v1.2.1](https://img.shields.io/badge/AppVersion-v1.2.1-informational?style=flat-square)

A **local-development** API gateway for the Pacto dashboard and Evidence Server.

It installs the [Envoy Gateway](https://gateway.envoyproxy.io/) controller (a
Gateway API implementation) plus a `GatewayClass`, a `Gateway` and an
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

## Requirements

| Repository | Name | Version |
|------------|------|---------|
| oci://docker.io/envoyproxy | envoyGateway(gateway-helm) | v1.2.1 |

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| envoyGateway | object | `{"enabled":true}` | Install the Envoy Gateway controller as a dependency. Set false to reuse a controller that is already installed in the cluster. |
| envoyService.httpNodePort | int | `30080` | Fixed nodePort for the HTTP listener (must be within the cluster's nodePort range; 30080 pairs with the demo's kind port mapping). |
| envoyService.type | string | `"NodePort"` | Envoy data-plane Service type. Use LoadBalancer if the cluster has one. |
| gateway.allowRoutesFromAllNamespaces | bool | `true` | HTTPRoutes are accepted from every namespace so the operator's routes (in pacto-system) can attach to this Gateway. |
| gateway.className | string | `"pacto-envoy"` | GatewayClass name created for this Gateway. |
| gateway.name | string | `"pacto-dev"` | Name of the Gateway (the pacto-operator dashboard/Evidence HTTPRoutes reference this via dashboard.httproute.parentRefs / evidence.httproute.parentRefs). |
| gateway.port | int | `80` | HTTP listener port on the Gateway. |
