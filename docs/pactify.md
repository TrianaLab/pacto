# Pactify a service

To pactify a service is to give it a Pacto contract and let Pacto look after
its releases. This guide takes a plain service to its first release that Pacto
checks before it reaches production. It is the only delivery page you need.
The [idea behind delivery](integrations/kubernetes/delivery.md) is one short
page, and everything else is reference.

You need:

- a service in a git repository, with a Dockerfile;
- a container registry you can push to, the place your images are stored;
- for steps 4 and 5, a Kubernetes cluster with
  [Flux](https://fluxcd.io) (the tool that applies what runs in the cluster)
  and the [Pacto operator](integrations/kubernetes/installation.md).

The [Quickstart](quickstart.md) explains what a contract is. Here Pacto writes
one for a service that already exists.

## 1. See what Pacto would add

From the root of your repository:

```bash
pacto init --plan
```

`pacto init` reads what is already there, such as the Dockerfile and any
Kubernetes manifests, and fills in a contract from it, including how the
service is published. With `--plan` it shows exactly what it would add and
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

This writes `pacto.yaml`. Running it again is safe: it adds what is missing
and never overwrites your edits.

Open the contract and fill in what the plan marked Unknown, above all the
dependencies: which services yours calls and which of their versions it
accepts, such as `^2.0.0` for any 2.x. That one line is what later lets Pacto
ask the right team about a change. Commit the contract, and add
[`pacto diff` to your CI](ci.md) so a change that breaks the services using
yours fails the build.

## 3. Publish a version

```bash
pacto publish --plan
pacto publish
```

`pacto publish` builds the service with Docker, pushes the image and the
contract to your registry, signs them and tags them with the version in the
contract. Check the contract's `publish` section first: it says where each
piece goes and how it is signed.

Publishing refuses a version too small for the change, so a breaking change
needs a new major version. It never overwrites a version already published.
Run it in CI as one step after your tests. The details are in
[Publish a version](publishing.md).

## 4. Connect the service to your cluster

Install delivery once per cluster. It is opt-in: without it, Pacto never
changes what runs.

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

Then let `pacto init` write the cluster side for this service:

```bash
pacto init --delivery --plan
pacto init --delivery
```

`--delivery` adds:

- a Pacto resource, which checks that the running service matches its
  contract;
- one Stage for each environment, such as test and prod. A Stage is one
  service in one environment and holds which version runs there;
- the Flux pieces: one that watches your registry for new versions and one
  that applies the version a Stage picks.

Review the files and apply them with `kubectl apply`. To let people approve
and agents propose, bind the two roles the chart creates; see
[Setup and gates](integrations/kubernetes/delivery-setup.md#install).

## 5. Ship your first gated release

Make a change, raise `service.version` in the contract and publish again:

```bash
pacto publish --propose test/my-service
```

`--propose` asks the test Stage to promote the new version once it sees it.
A Stage set to promote on its own (`auto: true`) needs no `--propose`. Watch
it move:

```bash
kubectl get stage -A
pacto delivery history -n test --stage my-service
```

From here Pacto does the rest. A safe change goes ahead without anyone being
asked, and reaches prod only after it passed in test. A change that would break a service using yours waits, asks
that service's team and says what to update first. A version that fails its
checks is rolled back. [Delivery](integrations/kubernetes/delivery.md)
explains each of these in plain words.

## Where next

| Goal | Page |
|------|------|
| Understand what happens to a change | [Delivery](integrations/kubernetes/delivery.md) |
| Check errors and response times after release | [Health checks and claims](integrations/kubernetes/delivery-health.md) |
| Let people approve from the dashboard | [Sign-in, approvals and agents](integrations/kubernetes/delivery-identity.md) |
| Keep a signed record of every change | [Signed records](integrations/kubernetes/delivery-records.md) |
| Set up Stages by hand | [Setup and gates](integrations/kubernetes/delivery-setup.md) |
