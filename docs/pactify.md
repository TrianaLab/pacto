# Pactify a service

To pactify a service is to give it a Pacto contract and let Pacto check every
new version before it runs. A contract is one file, `pacto.yaml`, kept next to
your code. It says what the service offers, which other services it calls and
how it is built and run. This guide takes a plain service to its first gated
release: one Pacto checks before it reaches production. It is the only
delivery page you need. The
[idea behind delivery](integrations/kubernetes/delivery.md) is one short page,
and everything else is reference.

You need:

- a service in a git repository, with a Dockerfile;
- a container registry you can push to, the place your images are stored;
- for steps 4 and 5, a Kubernetes cluster with
  [Flux](https://fluxcd.io) (a tool that keeps the cluster running what your
  files say) and the [Pacto operator](integrations/kubernetes/installation.md)
  (Pacto's program that runs in the cluster and watches your services).

You do not write the contract by hand: Pacto writes it from the service you
already have.

## 1. See what Pacto would add

From the root of your repository:

```bash
pacto init --plan
```

`pacto init` reads what is already there, such as the Dockerfile and any
Kubernetes files that describe how the service runs, and fills in a contract
from it, including where its releases go (step 3). With `--plan` it shows
exactly what it would add and changes nothing.

The plan lists the services yours depends on, what Pacto could not work out,
marked Unknown, and a **Next** list of what to do after. Unknown is not an
error. It is a question Pacto will not guess the answer to. To also see the
services that depend on yours, add `--fleet ../contracts`, a folder of their
contracts.

## 2. Write the contract

```bash
pacto init
pacto validate
```

This writes `pacto.yaml` and a `.pactoignore` file that keeps the signing
key out of what is published, and `pacto validate` checks the contract is
well formed. Running `pacto init` again is safe: it never overwrites your
edits.

Open the contract and fill in what the plan marked Unknown, above all the
dependencies: which services yours calls and which of their versions it
accepts, such as `^2.0.0` for any 2.x. This is called a version range. That
one line is what later lets Pacto ask the right team about a change. Commit
the contract. Then add [`pacto diff`](ci.md), which compares two versions of a
contract, to the checks that run on every commit, so a change that breaks the
services using yours fails there.

## 3. Publish a version

```bash
pacto publish --plan
pacto publish
```

`--plan` shows what publishing would do and changes nothing. `pacto publish`
then builds the service with Docker, pushes the image and the contract to your
registry, signs them so anyone can check they came from you and labels them
with the version in the contract. The contract's `publish` section says where
each piece goes and how it is signed. Signing uses a key pair: a private key
file, `cosign.key`, that only you hold, and the public key, `cosign.pub`, that
anyone uses to check. Make them with `cosign generate-key-pair`, and set
`COSIGN_PASSWORD` to its password when you publish.

Versions have three numbers, major.minor.patch, such as 2.4.1. Publishing
refuses a version too small for the change (a change that breaks the services
using yours needs 3.0.0) and never overwrites a published one. Run it in your
build pipeline after your tests; details in [Publish a version](publishing.md).

## 4. Connect the service to your cluster

Delivery is the part of Pacto that decides which version runs in each
environment. Install it once per cluster with Helm, the usual Kubernetes
installer. It is opt-in: without it, Pacto never changes what runs.

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

Then let `pacto init` write the cluster side for this service:

```bash
pacto init --delivery --plan
pacto init --delivery
```

`--delivery` adds:

- `delivery/pacto.yaml`, a Kubernetes object of kind Pacto that tells the
  operator to check that the running service matches its contract. It ships
  inside every version you publish, so you never apply it yourself (when
  `init` cannot wire that up, the Next list says how);
- `delivery/<environment>.yaml` for each environment: test and prod, or the
  ones you list with `--env`, in promotion order. Each holds a Stage, which is
  one service in one environment and holds which version runs there, and the
  Flux pieces: one that watches your registry for new versions and one that
  applies the version the Stage picks. Each environment is a Kubernetes
  namespace of the same name.

`init` only writes files. Its Next list says what to do with them: create the
namespaces, store `cosign.pub` in each as a Kubernetes Secret named
`cosign-pub` so Flux can check your signatures, then
`kubectl apply -f delivery/test.yaml -f delivery/prod.yaml`. The install also
adds two Kubernetes roles: `pacto-delivery-approver` for the people who may
approve a change and `pacto-delivery-agent` for AI agents, which may only
propose one. Give each to the right people or accounts; see
[Setup and gates](integrations/kubernetes/delivery-setup.md#install).

## 5. Ship your first gated release

Make a change, raise the version in the contract (the `version` line under
`service`) and publish again:

```bash
pacto publish
```

To promote is to move a Stage to a new version. The Stages `pacto init`
writes are set to promote on their own (`auto: true` in the Stage file), so
the test Stage moves once it sees the new version in the registry. For a
Stage without `auto: true`, ask it yourself:

```bash
pacto publish --propose test/my-service
```

`test/my-service` names one Stage: the environment (`test`), a slash, then the
Stage name, which is your service's name. Watch it move:

```bash
kubectl get stage -A
pacto delivery history -n test --stage my-service
```

The `Pointer` column of the first is the digest the Stage runs
(`sha256:...`), a fingerprint of one published version. The second lists the
changes on your service in test.

From here Pacto does the rest. A safe change goes ahead without anyone being
asked. The prod Stage only takes versions that passed in test, on its own with
`auto: true`. Without it, propose the digest the test Stage runs:

```bash
pacto delivery propose -n prod --stage my-service --to sha256:...
```

To see what would happen before you ask, run `pacto delivery plan` with the
same flags; see
[Plan, propose and approve](integrations/kubernetes/delivery-setup.md#plan-propose-and-approve).
A change that would break a service using
yours waits, asks that service's team and says what to update first. A version
that fails its checks is rolled back: the version that ran before is put back.
[Delivery](integrations/kubernetes/delivery.md) explains each of these in plain
words.

## Where next

| Goal | Page |
|------|------|
| Understand what happens to a change | [Delivery](integrations/kubernetes/delivery.md) |
| Check errors and response times after release | [Health checks](integrations/kubernetes/delivery-health.md) |
| Let people approve from the dashboard | [Sign-in, approvals and agents](integrations/kubernetes/delivery-identity.md) |
| Keep a signed record of every change | [Signed records](integrations/kubernetes/delivery-records.md) |
| Set up Stages by hand | [Setup and gates](integrations/kubernetes/delivery-setup.md) |
