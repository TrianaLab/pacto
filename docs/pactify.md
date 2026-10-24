# Pactify a service

To pactify a service is to give it a Pacto contract and let Pacto check every
new version before it runs. A contract is one file, `pacto.yaml`, kept next to
your code. It says what the service offers, which other services it calls and
how it is built and run. This guide takes a plain service to its first gated
release: one Pacto checks before it reaches production. Every command of that
path is on this page; the links are reference.

In the cluster, [Flux](https://fluxcd.io) does the deploying: it is a tool that
keeps the cluster running what your files say. Pacto tells it which version.

You need:

- the `pacto` command:
  `go install github.com/trianalab/pacto/v4/cmd/pacto@latest`, or another
  method from [Installation](installation.md);
- a service in a git repository, with a Dockerfile and the Kubernetes files
  that run it (a Deployment and a Service). Those files are what lands in the
  cluster. Leave the namespace out of them: each environment gets its own;
- Docker with a builder that can write an image to a folder, as Pacto builds
  the image there and pushes it itself:
  `docker buildx create --use --driver docker-container`;
- a container registry, and a login to it:
  `pacto login ghcr.io -u <user>` (or `docker login`);
- `cosign`, once, to make a signing key: `brew install cosign`, or
  `go install github.com/sigstore/cosign/v2/cmd/cosign@latest`;
- for step 4, a Kubernetes cluster (1.30 or later), `kubectl` and `helm`.

You do not write the contract by hand: Pacto writes it from your service.

## 1. See what Pacto would add

From the root of your repository:

```bash
pacto init --delivery --plan
```

`pacto init` reads what is already there, such as the Dockerfile and the
Kubernetes files, and fills in a contract from it. It reads your registry from
the image name in your Deployment; pass `--registry ghcr.io/acme` if it cannot.
`--delivery` adds the cluster files of step 4, so the first version you publish
already carries them. With `--plan` it shows exactly what it would add and
changes nothing. For a local registry that speaks plain HTTP, add
`--insecure-registry`; to keep a throwaway key's signatures out of the public
transparency log, add `--no-transparency-log`.

The plan lists the services yours depends on, what Pacto could not work out,
marked Unknown, and a **Next** list of what to do after. Unknown is not an
error. It is a question Pacto will not guess the answer to.

## 2. Write the contract

```bash
pacto init --delivery --owner payments
pacto validate
echo cosign.key >> .gitignore
```

`--owner` names the team that owns the service. Use the name of that team's
group in your company login: its members are the people Pacto asks about this
service. `init` writes `pacto.yaml`, the `delivery/` folder and a
`.pactoignore` file that keeps the signing key out of what is published.
`pacto validate` checks the contract is well formed. Running `pacto init` again
never overwrites your edits. For an image `ghcr.io/acme/my-service:1.0.0` and
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
      build:                # your Kubernetes files plus delivery/pacto.yaml
        command: [sh, -c, 'cp k8s/deployment.yaml k8s/service.yaml "$PACTO_OUT"/ && sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml']
  sign: {key: cosign.key}
```

Fill in what the plan marked Unknown, above all the dependencies: uncomment one
entry per service yours calls and set `compatibility` to the versions it
accepts, such as `^2.0.0` for any 2.x. This is called a version range, and it is
what lets Pacto ask the right team about a change. Every field is in
[Publish](contract-reference/publish.md) and
[Dependencies](contract-reference/dependencies-and-state.md). If your images
are private, also add `imagePullSecrets: [{name: registry-creds}]` under
`spec.template.spec` in your Deployment; step 4 creates that Secret. Commit
`pacto.yaml`, `.pactoignore`, `.gitignore` and `delivery/`.

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

Pacto reads the contract and deploy repositories (`publish.bundle` and the
`manifests` ref) without a login, so make those two public. On GitHub a new
package starts private: open it under your organization's **Packages**, then
**Package settings** and **Change visibility**. Your images can stay private.

Versions have three numbers, major.minor.patch, such as 2.4.1. Publishing
refuses a version too small for the change (a change that breaks the services
using yours needs 3.0.0) and never overwrites a published one; see
[Publish a version](publishing.md).

## 4. Connect the service to your cluster

Install, once per cluster, Flux, the Pacto operator (it checks that each
running service matches its contract) and delivery (it decides which version
runs in each environment). Helm is the usual Kubernetes installer; the charts
have their own version numbers.

```bash
kubectl apply -f https://github.com/fluxcd/flux2/releases/latest/download/install.yaml
```

--8<-- "integrations/kubernetes/docs/generated/_install-command.md"

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

Delivery is opt-in: without it, Pacto never changes what runs. Each environment
is a namespace of the same name. Create them, give Flux your public key, so it
takes only versions you signed, and apply the files `init` wrote:

```bash
for env in test prod; do
  kubectl create namespace $env
  kubectl -n $env create secret generic cosign-pub --from-file=cosign.pub
done
kubectl apply -f delivery/test.yaml -f delivery/prod.yaml
```

If your images are private, create in each namespace the pull Secret your
Deployment names since step 2:

```bash
for env in test prod; do
  kubectl -n $env create secret docker-registry registry-creds \
    --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>
done
```

Flux and the operator can read a private repository with the same Secret, but
delivery cannot yet, hence the two public repositories
([Limitations](integrations/kubernetes/limitations.md#delivery-and-private-registries)).

Each `delivery/<env>.yaml` holds a Stage, which is one service in one
environment and holds which version runs there, and the Flux pieces that watch
your registry and apply what the Stage picks. `delivery/pacto.yaml` tells the
operator to check the service; it ships inside every version, so you never
apply it. Within a minute or two the test Stage runs the version you published,
with nobody asked, then prod takes it once it passed in test:

```bash
kubectl get stage -A                                  # Pointer: the version it runs
pacto delivery history -n test --stage my-service     # what happened in test
kubectl -n test describe stage my-service             # if nothing moves: why, under Ready
```

## 5. Ship the next version

Make a change, raise `service.version` in `pacto.yaml` and the image tag in
your Deployment to the same number, then run `pacto publish`. A safe change
moves through test to prod on its own (`auto: true` in each Stage). A version
that fails its checks after release is rolled back once, or, with nothing to
go back to, its Stage freezes: nothing moves there until a person unfreezes it.
A change that would break a service using yours waits and asks that service's
team.

### When a change waits or an environment freezes

A waiting change is an Operation in the phase `Held`; a frozen Stage shows
`true` under `Frozen`. Only a person decides, never an AI agent, and never the
person who asked for the change:

```bash
kubectl get operations -n test                        # find the held one
pacto delivery explain -n test <operation>            # what is asked and why
pacto delivery approve -n test <operation> --decision approve
pacto delivery approve -n test <operation> --decision accept-unknown
pacto delivery approve -n test <operation> --decision reject
pacto delivery cancel -n test <operation>             # withdraw it instead
pacto delivery propose -n test --stage my-service --type unfreeze --reason "fixed"
```

`approve` lets a change that breaks a consumer go ahead; `accept-unknown` lets
one go ahead that Pacto could not check, recorded as accepted, never as a pass.
To unfreeze (the dashboard calls it unpause) is to let automation move the
Stage again; it takes effect without anyone else's approval. Each needs the
`pacto-delivery-approver` role, and only members of the team that owns an
affected service may answer for it:

```bash
kubectl create clusterrolebinding payments-approvers \
  --clusterrole=pacto-delivery-approver --group=payments
```

Deciding (approve, accept-unknown or reject) also needs a company login the
cluster trusts, done in the last 15 minutes; a cluster admin sets that up once
with [Sign-in, approvals and agents](integrations/kubernetes/delivery-identity.md).
Until then, cancel the change, fix what it asks about and publish again.
In the dashboard (`pacto dashboard`), the Delivery tab's **Inbox** lists the
changes **Waiting for your decision**: open one and press **Approve** (or
**Accept the unknown**) or **Reject**. Its **Environments** view has an
**Unpause** button on each frozen Stage.

## Where next

| Goal | Page |
|------|------|
| Understand what happens to a change | [Delivery](integrations/kubernetes/delivery.md) |
| Fail a commit that would break a consumer | [CI integration](ci.md) |
| Check errors and response times after release | [Health checks](integrations/kubernetes/delivery-health.md) |
| Keep a signed record of every change | [Signed records](integrations/kubernetes/delivery-records.md) |
| Set up Stages by hand | [Setup and gates](integrations/kubernetes/delivery-setup.md) |
