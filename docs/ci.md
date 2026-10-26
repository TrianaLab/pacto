# Put it in your repo

Add Pacto to your build pipeline (CI) so a pull request that would break the
services using yours fails its checks, and every merge publishes a version.
This page assumes you have a contract, `pacto.yaml`, from
[Pactify a service](pactify.md). The examples use the names from that page:
the contract is published to `oci://ghcr.io/acme/contracts/my-service`, the
`publish.bundle` field of `pacto.yaml`.

## What to commit

Commit what `pacto init` wrote (`pacto.yaml`, `.pactoignore`, `.gitignore`,
`delivery/` and any files the contract points to, such as an OpenAPI
document).

If your contract lists dependencies, also run `pacto lock .` and commit the
`pacto.lock` it writes. It records the exact published build of every
contract yours depends on, so every build checks against the same ones. It is
optional: without it, Pacto looks dependencies up fresh each time. See
[Lockfile](lockfile.md).

## On every pull request

Two checks. The second reads your registry, so give the job a login. These
variables work for any registry; on GitHub's, the built-in token will do:

```yaml
env:
  PACTO_REGISTRY_USERNAME: ${{ github.actor }}
  PACTO_REGISTRY_PASSWORD: ${{ secrets.GITHUB_TOKEN }}
```

```yaml
- name: Validate the contract
  run: pacto validate .

- name: Check for breaking changes
  run: pacto diff oci://ghcr.io/acme/contracts/my-service .
```

`pacto validate` catches missing or malformed fields, fields that contradict
each other and failed policies, the rules your platform team may set for
every contract.

`pacto diff` compares the contract in the pull request (the second argument)
with the last one you published (the first). With no version after the name,
it picks the highest published version. It exits 1 when the change is
breaking, meaning it removes or changes something other services may use, so
the check fails. Your branch protection rules decide whether a failed check
blocks the merge.

Until the first version is published there is nothing to compare with and
`pacto diff` fails, so add this step after your first `pacto publish`. To
compare two folders with no registry, run `pacto diff ./main ./feature-branch`.

## After a merge: publish

With the same login variables:

```yaml
- name: Set up a Docker builder
  run: docker buildx create --use --driver docker-container

- name: Publish a version
  run: |
    echo "$COSIGN_KEY" > cosign.key      # publish.sign reads it from the repository root
    pacto publish
  env:
    COSIGN_KEY: ${{ secrets.COSIGN_KEY }}
    COSIGN_PASSWORD: ${{ secrets.COSIGN_PASSWORD }}
```

`pacto publish` builds the image, pushes it to your registry with your
Kubernetes files and the contract, and signs them; delivery only rolls out
versions published this way. Store the contents of `cosign.key` and its
password as CI secrets. On GitHub, the job also needs
`permissions: packages: write` to push.

It never overwrites a published version, and a merge that does not raise
`service.version` in `pacto.yaml`, such as a docs change, fails here. Raise
the version with each change you want to ship, or run this step only when
`pacto.yaml` changed. See [Publish a version](publishing.md).

If you use contracts without delivery, `pacto push oci://ghcr.io/acme/contracts/my-service -p .`
uploads only the contract.

## A breaking change you need to ship

Sometimes a breaking change is the right call. Give it a new major version
(`1.4.2` becomes `2.0.0`): `pacto publish` refuses a breaking change with a
smaller version. With delivery, the change then waits for the teams whose
services it would break. Do not remove fields or change `pactoVersion` to get
past the check.

## GitHub Actions and other CI systems

The [GitHub Actions integration](github-actions.md) wraps these steps in a
ready-made action that also installs Pacto and comments on the pull request.
For GitLab CI, Jenkins or CircleCI, install Pacto with the
[installer script](installation.md#via-installer-script) and run the same
commands.
