# Member clusters

This page describes how a cluster joins a hub through the registry and how a
member links out to the hub so that each member decides, with its own RBAC,
what a signed-in person reads there. Reading clusters through kubeconfig
contexts is covered in [Multi-cluster](multi-cluster.md).

A cluster can join a hub as a [member](glossary.md#member) through the
registry alone, with no kubeconfig of it and no network path between the
clusters. The [hub](glossary.md#hub) is any cluster whose dashboard,
`pacto mcp` or `pacto cluster` reads that registry and whose operator lists
approver signers.

## Set up the member and the hub

1. Install the operator in the member with `--set registry=ghcr.io/acme` and
   nothing else, or every member at once
   [through Argo CD or Flux](install-members.md). It publishes its signed snapshot to
   `ghcr.io/acme/pacto-targets:cluster-<uid>` with its display name, the
   environments it seems to host and its installation key.
2. On the hub's operator, set `delivery.enabled=true` and
   `delivery.identities.signers`. A hub without signers counts no approval and
   says so on every pending row.
3. For a private registry, add a pull-only Secret to the hub's
   `dashboard.ociSecrets`. Never reuse `registryPullSecret`: it can push.
4. Let the people who approve members `approve` clusterjoins, as users:

```sh
kubectl create clusterrole pacto-cluster-approver --verb=approve --resource=clusterjoins.pacto.trianalab.io
kubectl create clusterrolebinding pacto-cluster-approver --clusterrole=pacto-cluster-approver --user=oidc:alice@acme.com
```

`clusterjoins` is a virtual resource with no CRD. A certificate names no
groups, so the hub never counts a grant to a group. A profile `hardened` cluster can be a
member but never a hub: it renders no delivery and so no approver signers.

## Approve a member

The new cluster shows as pending on Data sources, in
`pacto cluster list --registry ghcr.io/acme` and in the MCP tool
`pacto_cluster_list`. Ask the member's admin for its key id, read in the
member, or copy the approve line `helm upgrade` prints there, which fills in
the cluster's UID and key id:

```sh
kubectl get configmap pacto-operator-detected -n pacto-operator-system -o jsonpath='{.data.detected\.json}' | jq -r .installation.snapshotKeyID
```

If it matches the pending row, approve the key you checked. `--name` sets the
display name:

```sh
pacto cluster approve 9f1c4e2a --key sha256:<snapshotKeyID> --name eu-prod --registry ghcr.io/acme
```

The command checks your grant, signs you in once in the browser and records an
approval signed as you beside the snapshot. It refuses when the snapshot names
another key by then. The dashboard's Approve button shows this command.

From the next fleet build on, the member's services, targets and workloads
appear, verified under the approved key and scoped to its cluster. People
signed in to the dashboard or an HTTP MCP server see a member hidden and
counted until it has a [live channel](#review-people-through-a-live-channel)
that reviews them there.

The hub checks every approval each time it reads it, so a person who loses the
grant turns their approvals pending. A dashboard installed with a namespace
list cannot review `clusterjoins`, so its approvals stay pending. The CLI
reviews approvers as you: grant `create subjectaccessreviews`.

To ship a version to approved members from the hub, see
[Deliver through a member](integrations/kubernetes/delivery-members.md#deliver-through-a-member).

## Remove, rebuild and rotate

- `pacto cluster remove 9f1c4e2a --registry ghcr.io/acme` records a signed
  revocation. The member leaves the next fleet build. Rotate its
  registry credential too.
- A rebuilt cluster has a new UID. It shows pending with "may replace eu-prod"
  when it keeps the name of an approved member.
- A new installation key on a known UID shows pending again, with the key
  that changed, until someone approves the new key.

## The registry boundary

Every member pushes to `pacto-targets`. Give members credentials without
delete, and turn on tag immutability where the registry offers it, such as zot
or Harbor. A record names the key it approves, so a swapped tag never makes a
member, but a member that can delete can remove what others pushed. The hub
reads every `cluster-*` tag within 10 listing pages. It reads approved members
first and up to 32 pending clusters, plus a cluster a command names by UID. It
counts the rest unread and marks the list partial.

## Review people through a live channel

Set one value in an approved member's operator values and upgrade it:

```yaml
hub:
  url: https://pacto.acme.com
```

The member's operator dials `wss://pacto.acme.com/api/members/connect`,
checks the hub's TLS certificate and signs a hub nonce with its approved
installation key. It opens no port and holds no impersonate right. Over that
[live channel](glossary.md#live-channel) the hub asks one question: may this
person list this resource in this namespace? The member answers with its own
SubjectAccessReview, so each member decides what a person reads there. A
member publishes no inferred contracts; its hub does.

A review that takes longer than 5 seconds, a dropped channel or a refusal
hides that member with `CLUSTER_HIDDEN` and marks the answer partial; the
other clusters stay whole. A dashboard without sign-in keeps reading every
member. The hub's dashboard shows each member as `live` or `no live channel`,
with its identity model, on Data sources and in `meta.clusters[].link` of its
fleet answers. `pacto cluster list` reads the registry, so it shows the
approved group mappings and no channel.

The hub's dashboard must know the host members dial: the host of
`--oidc-redirect-url`, else `--members-host`. An operator-managed dashboard
takes it from `dashboard.auth.oidc.redirectURL`, so it needs OIDC sign-in to
be a hub. A profile `hardened` cluster runs no review channel.

## Identity models

Pacto picks an identity model per member and per issuer.

**Shared issuer.** The hub exchanges the person's session token at the
identity provider (RFC 8693) for a token whose audience is that one member:
`pacto-member-<uid>` by default, or `--members-token-audience` with `{uid}`
replaced. Configure each member's apiserver to accept that audience and the
hub's apiserver not to. The raw session token never leaves the hub. The member
asks its own apiserver who the token is, with the token as the only
credential, and then checks RBAC for that answer. The hub needs an issuer and
a client allowed to exchange tokens (`dashboard.auth.oidc`). ID and access
tokens must live 1 hour or less, or the hub cannot forward them.

**Different issuers or no exchange.** When a member's apiserver refuses an
issuer's tokens, or the hub exchanges none, a person with `approve` on
clusterjoins approves a [group mapping](glossary.md#group-mapping) for that
issuer:

```sh
pacto cluster map-groups 9f1c4e2a --issuer https://login.other.example --map ops=ops-readers --registry ghcr.io/acme
```

- The mapping answers reads only. Approvals for that member stay with its own
  sign-in.
- The member sees the person as `pacto-mapped:<issuer hash>:<user>` in the
  mapped groups. Bind mapped groups in the member rather than mapped users. A
  member group may not start with `system:`.
- The member does not verify the mapping: it knows neither the hub's approvers
  nor their signers. It trusts the hub for who a mapped person is and answers
  only list reviews of the resources Pacto reads.
- An issuer counts as refused only after two people are refused within a
  minute, so one bad token never switches a member to the mapping. The mark
  lasts 10 minutes.
- A `CLUSTER_ISSUER_DIFFERS` item on Needs attention names the members of each
  issuer and who approved the mapping, or the `map-groups` command when no one
  did. `--clear` removes a mapping. `--signer-issuer` picks your signer when
  the hub lists several approver signers.

## Members behind NAT

A member needs outbound HTTPS to `hub.url` and to the registry, and nothing
inbound: a default-deny ingress NetworkPolicy in its namespace keeps working.

- The dashboard serves plain HTTP. Reach the hub through a TLS-terminating
  Ingress or Gateway with WebSocket upgrade enabled. Its host must equal the
  member's `hub.url` host and the hub's `--members-host` or redirect URL host.
- For a private CA, set `hub.caConfigMap` to a ConfigMap in the member's
  release namespace holding `ca.crt`.
- The member sends a ping every 30 seconds, so idle timeouts of 60 seconds or
  more keep the link open.
- The member answers only list reviews of Pactos, workloads and GitOps
  objects.
- A dropped channel hides the member until it reconnects.
