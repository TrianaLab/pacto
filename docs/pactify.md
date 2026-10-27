# Pactify a service

To pactify a service is to give it a Pacto contract and let Pacto check every
new version before it runs. A contract is one file, `pacto.yaml`, kept next to
your code. It says what the service offers, which other services it calls and
how it is built and run. This guide takes a plain service to a contract,
signed published versions and environments Pacto only watches, and shows how
an environment moves past watching once it has earned it. Every command is on
this page except the one-time cluster setup for approvals. To see where it leads first,
with nothing to install, press **Play the delivery story** in the
[live dashboard demo](examples/dashboard-demo.md); it runs in your browser.

In the cluster, [Flux](https://fluxcd.io) does the deploying: it is a tool that
keeps the cluster running what your files say. Pacto tells it which version.
Each environment starts on observe, the first level of
[the autonomy ladder](autonomy.md): Pacto watches and moves nothing until a
person raises it.

You need:

- the `pacto` command, from [Installation](installation.md);
- a service in a git repository, with a Dockerfile and the Kubernetes files
  that run it (a Deployment and a Service). Those files are what lands in the
  cluster. Leave the namespace out of them: each environment gets its own;
- Docker with a builder that can write an image to a folder, as Pacto builds
  the image there and pushes it itself:
  `docker buildx create --use --driver docker-container`;
- a container registry, and a login to it:
  `pacto login ghcr.io -u <user>` (or `docker login`). Any registry works
  for this page. The optional signed record of each change also needs one
  that serves OCI Referrers, such as [zot](https://zotregistry.dev); GitHub's
  registry does not, so on `ghcr.io` you get no records, and without records
  a Stage stops at advise and never ships on its own;
- `cosign`, once, to make a signing key: `brew install cosign`, or
  `go install github.com/sigstore/cosign/v2/cmd/cosign@latest`;
- for step 4, a Kubernetes cluster (1.30 or later), `kubectl` and `helm`.
  People approve with a fresh company login, which needs a one-time cluster
  setup, maybe by you:
  [the cluster trusts your identity provider](integrations/kubernetes/delivery-identity.md#the-cluster-trusts-your-identity-provider).
  It is tested on kind, not yet on EKS, GKE and AKS. Until it works,
  everything here works except approving, so prod waits; see
  [where approving works](integrations/kubernetes/delivery-identity.md#where-approving-works).

You do not write the contract by hand: Pacto writes it from your service.

Only want the contract and its CI checks? Leave out `--delivery`, stop after
step 3 and go on to [Put it in your repository](ci.md). No cluster needed.

## 1. See what Pacto would add

From the root of your repository:

```bash
pacto init --delivery --plan
```

`pacto init` reads what is already there, such as the Dockerfile and the
Kubernetes files, and fills in a contract from it. It reads your registry from
the image name in your Deployment; pass `--registry ghcr.io/acme` if it cannot,
or if the image is a bare name such as `checkout`: a Docker Hub official
image only Docker can push to.
`--delivery` adds the cluster files of step 4, so the first version you publish
already carries them. With `--plan` it shows exactly what it would add and
changes nothing. For a private registry, add `--registry-secret registry-creds`
(step 4 creates that Secret). For a local registry that speaks plain HTTP, add
`--insecure-registry`; to keep a throwaway key's signatures out of the public
transparency log, add `--no-transparency-log`.

The plan lists the services yours depends on, what Pacto could not work out,
marked Unknown, and a **Next** list of what to do after. Unknown is not an
error but a question Pacto will not guess.

## 2. Write the contract

Add any flags you needed in step 1, such as `--registry-secret`:

```bash
pacto init --delivery --owner payments
pacto validate
echo cosign.key >> .gitignore
```

`--owner` names the team that owns the service: the name of its group in your
company login, whose members Pacto asks about this service
([Decide, unfreeze and roll back](pactify-decide.md)). `init` writes
`pacto.yaml`, the `delivery/` folder and a `.pactoignore` that keeps the
signing key out of what is published, and never overwrites your edits. For an image `ghcr.io/acme/my-service:1.0.0` and
the files `k8s/deployment.yaml` and `k8s/service.yaml`, the contract is:

```yaml
pactoVersion: "2.1"
service:
  name: my-service
  version: 1.0.0            # from the image tag (0.1.0 and an Unknown if none)
  owner:
    team: payments
# dependencies:             # one per service yours calls; init leaves them commented
#   - name: billing
#     ref: oci://ghcr.io/acme/contracts/billing   # its publish.bundle
#     required: true                              # yours cannot run without it
#     compatibility: "^2.0.0"                     # the versions yours accepts
publish:
  bundle: oci://ghcr.io/acme/contracts/my-service   # where the contract goes
  deploy: manifests                                 # what Flux applies
  artifacts:
    - name: api
      type: image
      ref: oci://ghcr.io/acme/my-service
      build: {dockerfile: {context: .}}
    - name: manifests
      type: manifests
      ref: oci://ghcr.io/acme/deploy/my-service
      build:                # your Kubernetes files, the image just built, delivery/pacto.yaml
        command: [sh, -c, 'cp k8s/deployment.yaml k8s/service.yaml "$PACTO_OUT"/ && sed -E "s,(^|[{,[:space:]])(image:[[:space:]]*[\"'']?)ghcr\.io/acme/my-service:1\.0\.0([\"'']?[[:space:]]*([,}#]|$)),\1\2$PACTO_REF_API@$PACTO_DIGEST_API\3,g" k8s/deployment.yaml > "$PACTO_OUT"/deployment.yaml && { grep -qF "$PACTO_DIGEST_API" "$PACTO_OUT"/deployment.yaml || { echo "pacto: no image: ghcr.io/acme/my-service:1.0.0 line to pin" >&2; exit 1; }; } && sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml']
  sign: {key: cosign.key}
```

You never edit the long `command` line. It copies your Kubernetes files with
the image pinned to the digest this version built, in every manifest that runs
it, and names any file it cannot rewrite. `k8s/deployment.yaml` stays as it is.

Fill in what the plan marked Unknown, above all the dependencies: uncomment
one entry per service yours calls whose contract is published, and set
`compatibility` to the versions it accepts, such as `^2.0.0` for any 2.x. This
version range lets Pacto ask the right team about a change. Declare an OpenAPI
document under `interfaces` ([how](ci.md#declare-your-api-first)): Pacto sees
API changes only there. Every field is in [Publish](contract-reference/publish.md)
and [Dependencies](contract-reference/dependencies-and-state.md). For private
images, add `imagePullSecrets: [{name: registry-creds}]` under
`spec.template.spec` in your Deployment. With dependencies, run `pacto lock .`
([Lockfile](lockfile.md)). Commit `pacto.yaml`, `.pactoignore`, `.gitignore`,
`delivery/` and any `pacto.lock`.

## 3. Publish a version

```bash
cosign generate-key-pair      # once: cosign.key (private) and cosign.pub
export COSIGN_PASSWORD=...    # the password you chose
pacto publish --plan
pacto publish
```

`--plan` changes nothing. `pacto publish` builds the image, pushes it with your
Kubernetes files and the contract to your registry, signs them so anyone can
check they came from you and labels them with the version in the contract. It
reads `cosign.key` from the root of the repository, as `publish.sign` says. In
your build pipeline, give the key and its password as secrets and run
`pacto publish` after your tests.

Versions have three numbers, major.minor.patch, such as 2.4.1. Publishing
refuses a version too small for the change (a change that breaks the services
using yours needs 3.0.0) and never overwrites a published one; see
[Publish a version](publishing.md).

## 4. Connect the service to your cluster

Install, once per cluster, Flux, the Pacto operator (it checks that each
running service matches its contract) and delivery (it decides which version
runs in each environment). One Helm chart installs both. Delivery reads your contracts: with a private registry, give it a login
first in the operator's namespace and add
`--set delivery.promoter.registryCredentials.secretName=registry-creds` to the
command that turns delivery on below:

```bash
kubectl create namespace pacto-operator-system
kubectl -n pacto-operator-system create secret docker-registry registry-creds \
  --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>
```

```bash
kubectl apply -f https://github.com/fluxcd/flux2/releases/latest/download/install.yaml
```

--8<-- "integrations/kubernetes/docs/generated/_install-command.md"

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

Replace `staff` with the group your people are in: anyone outside it counts as
an agent and never approves.

Delivery is opt-in: without it, Pacto never changes what runs. Each environment
is a namespace of the same name. Create them, give Flux your public key, so it
takes only versions you signed, and apply the files `init` wrote:

```bash
for env in test prod; do
  kubectl create namespace $env
  kubectl -n $env create secret generic cosign-pub --from-file=cosign.pub
  # a private registry only:
  kubectl -n $env create secret docker-registry registry-creds \
    --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>
done
kubectl apply -f delivery/test.yaml -f delivery/prod.yaml
```

`registry-creds` is the pull Secret your Deployment names and, with
`--registry-secret`, the login Flux and the operator read your repositories
with. A read-only token is enough (on GitHub, `read:packages`); see
[Private registries](integrations/kubernetes/limitations.md#delivery-and-private-registries).

Each `delivery/<env>.yaml` holds a Stage (one service in one environment and
the version it runs there) and the Flux pieces that watch
your registry and apply what the Stage picks. `delivery/pacto.yaml` tells the
operator to check the service; it ships inside every version, so you never
apply it.

Each Stage starts at `observe`: it plans every new version and moves nothing,
and the OCIRepository stays pinned to the tag you deploy (`spec.ref.tag`), so
Flux applies what it did. With no version tag on the image, `init` writes no
environment file.

```bash
kubectl get stages.delivery.pacto.trianalab.io -A    # POINTER: the digest of the version each one runs
pacto delivery history -n test --stage my-service     # what happened in test
kubectl -n test describe stages.delivery.pacto.trianalab.io my-service  # if nothing moves: why, under Ready
```

## 5. Ship the next version

Make a change, raise `service.version` in `pacto.yaml`, then run
`pacto publish`. While the Stages observe, you deploy the new tag as before:
set `spec.ref.tag` in `delivery/<env>.yaml` and apply it. Each Stage records
what it would have done.

To leave observe, in this order:

1. Keep deploying this way until 10 versions passed their checks
   (`autonomy.proposeAfter` lowers that, see
   [the ladder](autonomy.md#earned-autonomy-pacto-proposes-a-person-decides)).
   The Stage's `status.proposal` lists what is still missing.
2. Remove `spec.ref` from the OCIRepository in `delivery/test.yaml` and apply
   it, so Flux stops setting the version, and label it so only Pacto may
   write its digest. Pacto then proposes `advise`:

    ```bash
    kubectl -n test label ocirepository my-service delivery.pacto.trianalab.io/apply-ref=my-service
    ```

3. Ask for the raise; another person of the owner team approves it (see
   [Why you were asked](pactify-decide.md#why-you-were-asked)). Be quick:
   until the raise, nothing moves the version.

    ```bash
    pacto delivery propose --type delivery.pacto.trianalab.io/autonomy --stage my-service -n test --to advise
    ```

Each level above that is one more approved operation. In prod, `init` also
writes `approval: always: true`, so every promote there waits for a person:
the contract check cannot see a consumer with no contract, or tell whether the
version is healthy. Delete it from `delivery/prod.yaml` once a health check
judges each version there (`gates.verify.checks`, see
[Delivery reference: health checks](integrations/kubernetes/delivery-health.md)).
A version that fails its checks after release is rolled back once, or its
Stage freezes until a person unfreezes it.

Each move is a change, a Kubernetes object of kind `Operation`; one that waits
for a person shows `Held`. To approve one, as a member of the owner team:

```bash
kubectl get operations.delivery.pacto.trianalab.io -n prod   # PHASE shows Held
pacto delivery approve -n prod <name> --decision approve
```

## Many services at once

`pacto init` takes several folders and ends with a summary table.
`--cluster` drafts a contract for every workload no Pacto describes yet, from
your current kubeconfig context, in `<out>/<namespace>/<name>`:

```bash
pacto init --plan services/checkout services/payments
pacto init --cluster --namespace shop --out drafts --plan
```

With the Pacto node agent running, the services a workload was seen calling
become dependencies, pinned to exactly the version seen running
(`compatibility: "=2.3.0"`) for you to widen. Until you do, every new version
of that service asks your team. A service with no contract stays a comment.
Drafts are never published, and an invalid one fails the command. The MCP tool
`pacto_create` with `pactify: true` takes `paths`, `cluster` and `namespaces`,
and only plans unless `dry_run` is false.

## Where next

| Goal | Page |
|------|------|
| Decide on a waiting change or roll one back | [Decide, unfreeze and roll back](pactify-decide.md) |
| Understand what happens to a change | [Delivery](integrations/kubernetes/delivery.md) |
| Fail a commit that would break a consumer | [Put it in your repository](ci.md) |
| Check errors and response times after release | [Health checks](integrations/kubernetes/delivery-health.md) |
| Keep a signed record of every change | [Signed records](integrations/kubernetes/delivery-records.md) |
| Set up Stages by hand | [Setup and gates](integrations/kubernetes/delivery-setup.md) |
