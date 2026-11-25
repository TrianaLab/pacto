# Delivery reference: several clusters

A [member](../../glossary.md#member) takes versions from its
[hub](../../glossary.md#hub) through the registry, and a Stage in one cluster
can follow a Stage in another. The hub writes nothing into a member and needs
no route to one: each member pulls what is asked of it, checks it with its own
trust and RBAC and decides with its own gates and approvals. Joining a member
is covered in [Member clusters](../../member-clusters.md); the managers that
apply a version are covered in [Appliers](delivery-reach.md).

## Deliver through a member

A person on the hub plans a promote by service and environment, with the
digest `pacto publish` printed (member mode takes a digest, never a version):

```sh
pacto delivery plan --service billing --environment production \
  --to sha256:5e1f... --registry ghcr.io/acme
```

The plan names the placement, the members the promote goes to, and where it
came from:

- `policy`: a signed placement rule names the members by uid.
- `unconfirmed`: the members whose join block says they take requests for that
  environment. Pacto sends nothing on an unconfirmed placement.
- `advertised`, with `confirmedBy`: a person signed a request that named every
  member placed now.

To confirm, the person runs the command the plan and the dashboard print. It
logs them in again in their browser and signs one request per placed member,
or per `--member` (each one the plan places), keyless as them, pushed to that
member's request stream in `<registry>/pacto-targets`. When the placement
policy or the members read only in part, the plan offers no command and
`--confirm` answers `PlacementUnknown`, signing nothing:

```sh
pacto delivery propose --service billing --environment production \
  --to sha256:5e1f... --registry ghcr.io/acme --confirm
```

An AI agent reads the plan and hands that command to a person:
`pacto_delivery_operation_propose` answers `MemberRequestNeedsPerson`.

### What the member checks

The member's operator reads its request stream every 30 seconds and checks
each request in this order. A request no approver signer verifies writes
nothing on the member; it is counted in `pacto_member_requests_unverified_total`.

1. A person signed it, under the installation's approver signers
   (`NotAPerson` otherwise).
2. It names this cluster (`NotForThisCluster`), and it was signed within its
   lifetime, at most 24 hours by default and never longer than 7 days or the
   recorder's `operationTTL` (`RequestExpired`).
3. The member has a Stage of that service in that environment, where `prod`
   and `production` name the same environment (`NoStage`).
4. Exactly one such Stage passes the allowlist of service, namespace and
   environment (`OutsideAllowlist`, `AmbiguousStage`).
5. The Stage did not move since the person asked (`PointerMoved`).
6. A SubjectAccessReview in the member lets the person `create`
   `operations.delivery.pacto.trianalab.io` in the Stage's namespace
   (`NotAllowedHere`, with the `kubectl create rolebinding` to run).

The operator then raises one promote, as an agent acting for that person, so
that person never approves it. The chart grants these reads and writes in the
ClusterRole `<release>-member-intake`; the operator learns its own user with a
SelfSubjectReview, which the built-in `system:basic-user` role allows. The member's own gates, approval floors and
applier decide the rest: a digest no trusted publisher signed is rejected by
`pacto.published`, and a managed production Stage holds it for another person.

### Narrow what a member takes

By default a member takes requests for the namespaces its operator watches and
the environments its cluster name hosts, by the same rule its managed Stages
use. Three values narrow that set and never widen it:

```yaml
delivery:
  remote:
    enabled: auto   # false turns requests off
    allow:
      services: ["billing*"]
      namespaces: [billing]
      environments: [production]
```

### What the hub sees

Every refusal and conclusion is an outcome record the member signs with its
installation key, the key the hub approved. The plan, the Members block on the
Environments board of one service and `pacto delivery refusals --registry`
read them per member: `waiting`, `refused`, `raised`, `held`, `applied`,
`failed` or `concluded` (it expired, was cancelled or superseded), each with
its reason. A `held` member names the
command to approve it from the hub, which needs no trust file:

```sh
pacto delivery approve --intent <record> --registry ghcr.io/acme
```

The intent verifies under the recorder key the member advertises, on that
member's Stage streams only. A member that advertises none answers
`MemberRecorderUnknown`: set `delivery.recorder.signing.publicKey` there.

A placement rule names members by full uid, never by display name, because a
hub can rename a member at approval. `pacto cluster list` prints each uid.

## When a member is unreachable

A member pulls its requests, so a hub outage or a NAT never blocks an apply,
and a member the hub cannot read keeps what runs. On the hub, a member whose
snapshot is older than its freshness reads `stale`, a request with no outcome
past that time reads `unknown` and the answer is `partial`, naming the member.

## Follow a rung on another member

A managed Stage whose upstream rung runs on another member follows that
member's Stage records. The member derives its hub from the approval of its
own key, and follows a peer only when:

- that hub approved the peer, with an approval that verifies under this
  cluster's approver signers;
- a SubjectAccessReview here lets that approver `approve`
  `clusterjoins.pacto.trianalab.io` named for the peer;
- the peer's join block lists exactly one Stage stream for the service and
  rung, and advertises a recorder key.

The promoter then reads that one stream with that key, as described below.
Otherwise the rung stays unplaced and its reason names the member and the fix.
Pacto never falls back to the registry.

## Follow a Stage in another cluster

A Stage in one cluster can take its versions from a Stage in another cluster,
such as prod in one cluster following staging in another. The clusters do not
talk to each other. The downstream promoter reads the upstream Stage's signed
records from the registry.

You need:

1. The recorder running in the upstream cluster (see
   [Signed records](delivery-records.md)).
2. The upstream recorder's public key in the downstream promoter's trust
   config, bound to the upstream stream:

   ```yaml
   promoter:
     upstreamTrust: |
       keys:
         - name: staging-recorder
           publicKey: |
             -----BEGIN PUBLIC KEY-----
             ...
             -----END PUBLIC KEY-----
           streams: ["8d1e…/staging/*"]
   ```

3. The downstream Stage naming the upstream stream and the repository that
   holds its records. The stream is the upstream Stage's `status.stream.id`.

   ```yaml
   apiVersion: delivery.pacto.trianalab.io/v1
   kind: Stage
   metadata: {name: payments, namespace: prod}
   spec:
     service: {name: payments}
     environment: prod
     applyRef: {name: payments}
     candidates:
       upstreamRecords:
         repository: registry/delivery/streams
         stream: 8d1e…/staging/payments
   ```

### What the follower takes

The promoter rebuilds from the records what the upstream verified: a promote
that `Succeeded`, `Verified` and passed the published gate there is offered
here, and a failure the follower has read is never taken: a version that
failed there, or was rolled back after release. That includes a version that fails a check after release when
the promoter cannot roll it back there (a person or agent chose it, or its
rollback is used up): the promoter freezes that Stage with a freeze operation
whose record names the failed version. Its own gates, approvals and checks
still run. The digest must also exist in the Stage's own apply repository: use the same repository or a
mirror that keeps digests. `pacto delivery plan` reads these records only with
`--trust` and the upstream recorder's key.

### When the records cannot be trusted

The promoter offers nothing when it cannot check the history: without
`delivery.promoter.upstreamTrust` the Stage is not `Ready` (`UpstreamUntrusted`). A gap,
a fork or an unreadable registry make it not `Ready` (`UpstreamUnreadable`,
saying why) until a read succeeds, and refuse a promote as `NotPublished`. A
record no trusted key signed is skipped, since anyone with push access could
attach one. A trusted record that stops verifying leaves a gap.

What the follower cannot see, it cannot refuse. It detects records removed
from before `status.upstreamSeen`, but not a record hidden after it was
written and before the follower first read it, nor one signed with a key
`upstreamTrust` does not list, which is skipped like a forgery. A version that
failed upstream through such a record can still be taken. So rotate the
recorder's key in every follower first (list the new key beside the old one
in `upstreamTrust`), then upstream. The Stage's `Ready` message counts the
referrers on the upstream anchor it could not verify, so a skipped record
does not go unsaid.

### When records were removed or rewritten

The Stage remembers the newest upstream record it read in
`status.upstreamSeen`. A later read that ends before it, or holds another
record at its seq however far the stream has grown since, is refused the same
way: records were removed or rewritten, and one of them could be a failure.
Every `pacto.published` verdict on such a Stage cites the record it relied on
as `upstreamHead` and `upstreamSeq`, so an auditor can replay the stream up to
it.

Sometimes the cut is intended: the upstream stream was recreated, or a
`StreamConflict` repair removed its newest record. Nothing clears the mark on
its own, so the follower stays not `Ready` until a person does. Once an
auditor confirms the cut, clear it, with the right to patch `stages/status`.
Admission lets that right do nothing else: only the promoter writes the rest of
a Stage's or an Operation's status, so the write must remove `upstreamSeen` and
change nothing more. The next read, within 30 seconds, sets it again:

```bash
kubectl patch stage <stage> -n <namespace> --subresource=status --type=merge \
  -p '{"status":{"upstreamSeen":null}}'
```

The promoter reads the upstream registry at most every 30 seconds, and
afresh when it plans a promote. It verifies each record once.

### Freezes

A freeze is local, in one cluster or across two. Freezing the upstream Stage
does not stop a follower from taking a version the upstream already verified:
freeze the follower too. When the promoter freezes a Stage because a check
failed after release, its record names the failed version, so followers never
take it.

## Consumers in member clusters

- On a [hub](../../multi-cluster.md#consumers-and-delivery-across-clusters),
  a promote also reads each approved member's signed snapshot from
  `--targets-repository`, as the CLI and dashboard do. A consumer there that
  does not accept the change asks its owners.
- A stale member, a join list it could not read or a cluster that is not a
  member yet asks a person to accept the unknown for a change that is not
  plainly non-breaking.
- On a member, the chart passes `--member` whenever `hub.url` is set. That
  promoter reads no joins, since consumers in other clusters are read on the
  hub, and asks a person here for any change that is not plainly
  non-breaking.

## Limits

- A Stage sees the upstream's history only as far as its records reach the
  registry, so a failure not yet recorded is not seen.
- A cut is noticed only against a record the Stage already read. A Stage
  that starts following a stream whose newest records were already removed
  cannot tell.
