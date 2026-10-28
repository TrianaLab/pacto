# Quickstart with a bundled registry and sign-in

For an evaluation cluster, the chart ships the profile `values-quickstart.yaml`.
It installs [zot](https://zotregistry.dev) as an in-cluster registry with
persistence and native OCI referrers, installs [Dex](https://dexidp.io) as the
dashboard's identity provider and turns on [delivery](delivery-setup.md) with
its recorder writing to zot. The chart generates the dashboard's client secret,
the first sign-in password and the recorder's signing key at install and keeps
them across upgrades.

```bash
helm pull oci://ghcr.io/trianalab/pacto/charts/pacto-operator --untar
helm install pacto-operator ./pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  -f pacto-operator/values-quickstart.yaml
```

The profile is for evaluation. zot and Dex speak plain HTTP, zot accepts
anonymous pushes, Dex keeps sessions in memory and the operator can read the
recorder's key. The registry host in the profile names the namespace
`pacto-operator-system`; to install elsewhere, edit it in the file first.

The browser and the dashboard reach Dex at the same issuer URL,
`http://pacto-dex:5556/dex`. To sign in, point that name at your machine,
forward both Services and read the password of `admin@example.com`:

```bash
echo "127.0.0.1 pacto-dex" | sudo tee -a /etc/hosts
kubectl -n pacto-operator-system port-forward svc/pacto-dex 5556:5556 &
kubectl -n pacto-operator-system port-forward svc/pacto-dashboard 3000:3000 &
kubectl -n pacto-operator-system get secret pacto-dashboard-oidc \
  -o jsonpath='{.data.admin-password}' | base64 -d
```

The dashboard asks the API server who each signed-in person's token belongs
to and sends their actions with it, so per-person Kubernetes RBAC needs the API server to trust the Dex issuer through
[structured authentication](dashboard-authentication.md#match-the-api-servers-claim-mapping)
with the profile's claim mapping (`email` and `groups`, no prefixes). That is
an API server setting, which no chart can set; kind takes it at cluster
creation. The API server also accepts only an `https` issuer, so the bundled
Dex needs TLS first. Until the API server trusts it, a Dex sign-in completes
and every request is refused, so use
[token sign-in](dashboard-authentication.md#token-sign-in), which the profile
turns on.
