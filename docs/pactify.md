# Pactify a service

To pactify a service is to give it a Pacto contract and let Pacto check every
new version before it runs. A contract is one file, `pacto.yaml`, kept next to
your code. It says what the service offers, which other services it calls and
how it is built and run. This guide takes a plain service to its first release
that Pacto checks before it reaches production. It is the only delivery page
you need.
The [idea behind delivery](integrations/kubernetes/delivery.md) is one short
page, and everything else is reference.

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
from it, including where its releases go (step 3). With `--plan` it shows exactly what it would add and
changes nothing.

The plan also shows what you get before you apply anything: the services
yours depends on, the services that depend on yours and what Pacto could not
work out, marked Unknown. Unknown is not an error. It is a question Pacto will
not guess the answer to.

## 2. Write the contract

```bash
pacto init
pacto validate
```

This writes `pacto.yaml`, and `pacto validate` checks the file is well
formed. Running `pacto init` again is safe: it adds what is missing and never
overwrites your edits.

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
with the version in the contract. Check the contract's `publish` section
first: it says where each piece goes and how it is signed.

Versions have three numbers, major.minor.patch, such as 2.4.1. Publishing
refuses a version too small for the change, so a change that breaks the
services using yours needs a new major version (3.0.0). It never overwrites a
version already published. Run it in your build pipeline as one step after
your tests. The details are in
[Publish a version](publishing.md).

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

- a Pacto entry, which tells the operator to check that the running service
  matches its contract;
- one Stage for each environment, such as test and prod. A Stage is one
  service in one environment and holds which version runs there. Each
  environment is a Kubernetes namespace of the same name;
- the Flux pieces: one that watches your registry for new versions and one
  that applies the version a Stage picks.

`init` only writes files. Read them, then apply them to the cluster with
`kubectl apply -f` and the file names. The install also adds two Kubernetes
roles: `pacto-delivery-approver` for the people who may approve a change and
`pacto-delivery-agent` for AI agents, which may only propose one. Give each
role to the right people or accounts; see
[Setup and gates](integrations/kubernetes/delivery-setup.md#install).

## 5. Ship your first gated release

Make a change, raise `service.version` in the contract and publish again:

```bash
pacto publish --propose test/my-service
```

`test/my-service` is the environment, then the Stage, which has your
service's name. To promote is to move a Stage to a new version. `--propose`
asks the test Stage to promote the new version once it sees it in the
registry. A Stage set to promote on its own (`auto: true` in the Stage file)
needs no `--propose`. Watch it move:

```bash
kubectl get stage -A
pacto delivery history -n test --stage my-service
```

The first command lists every Stage in every namespace and the version it
runs. The second shows the changes on your service in test.

From here Pacto does the rest. A safe change goes ahead without anyone being
asked, and reaches prod only after it passed in test. A change that would
break a service using yours waits, asks that service's team and says what to
update first. A version that fails its checks is rolled back: the version that
ran before is put back. [Delivery](integrations/kubernetes/delivery.md)
explains each of these in plain words.

## Where next

| Goal | Page |
|------|------|
| Understand what happens to a change | [Delivery](integrations/kubernetes/delivery.md) |
| Check errors and response times after release | [Health checks and claims](integrations/kubernetes/delivery-health.md) |
| Let people approve from the dashboard | [Sign-in, approvals and agents](integrations/kubernetes/delivery-identity.md) |
| Keep a signed record of every change | [Signed records](integrations/kubernetes/delivery-records.md) |
| Set up Stages by hand | [Setup and gates](integrations/kubernetes/delivery-setup.md) |
