# Delivery reference: signed approvals

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

With the [recorder](delivery-records.md) on, every decision on a held change
is a signed record in your registry. CI and agents read it with no cluster
credentials. Who may decide is in
[Delivery reference: approvals](delivery-identity.md).

## Approval and intent records

Each Stage's stream also holds two smaller in-toto statements. Neither has a
`seq`, and the operation chain skips both:

| Predicate type | Written by | Says |
|---|---|---|
| `delivery.pacto.trianalab.io/operation-intent/v1` | the recorder, for every admitted Operation | what was asked and by whom (the admission-bound user), whether admission guarded it, and the approvals repository a person pushes a signed approval to; params, reason and cause that look like a secret are left out |
| `delivery.pacto.trianalab.io/approval/v1` | the recorder for an Approval made in the cluster (dashboard, kubectl), or the person's own client (`pacto delivery approve`) | the operation (name, uid, intent digest, Stage), the decision, the note and who signed: a `person`, or the `recorder` with `onBehalfOf` the person admission verified |

The recorder signs an Approval on behalf of its author only while the
approval admission policy guards the cluster, and only for Approvals created
after it first saw that policy, so turning it on never signs past decisions.
It keeps that moment in memory: an Approval made before a recorder restart, or
in the second the recorder first saw the policy, gets no record and never
counts. Approve again.
A person's signed approval goes to the Stage's approvals repository
(`delivery.recorder.approvalsRepository`, published on
`status.stream.approvals`), never to the streams repository. For a held
operation the recorder finds it there, verifies it against `delivery.identities.signers` and makes an
Approval for it.

With the recorder on, the promoter counts an Approval only once its record
exists and verifies, and the operation record lists every counted approval
with its record id. A verified approval record is a recorded decision: a gate
reads the operation record's `approvals[]` to know whether it counted.

## Sign a decision from the terminal

The installation can accept approvals a person signs keyless. List the issuers
that may sign in `delivery.identities.signers`:

```yaml
delivery:
  identities:
    signers:
      - issuer: https://idp.example.com
        clientID: pacto-approve
        usernamePrefix: "https://idp.example.com#"
        subjects: '^[^@]+@example\.com$'   # required
        fulcioURL: https://fulcio.example.com
        timestampURL: https://tsa.example.com/api/v1/timestamp
        trustedRoot: |
          { ... the Sigstore trusted_root.json ... }
```

`pacto delivery approve` then logs you in again in your browser, signs the
decision keyless with that login alone and pushes the signed record to the
Stage's approvals repository. It refuses while `SIGSTORE_ID_TOKEN` or a GitHub
Actions token variable is set: approving needs your own fresh login. The
issuer must put `auth_time`, `email` and `email_verified: true` in its tokens;
Fulcio refuses an unverified email. `trustedRoot` is required and the command
fetches no root: for public-good Fulcio, paste its `trusted_root.json`.

The approver is `usernamePrefix` followed by the certificate's email, with no
groups, so set the prefix your apiserver puts on that user. That user still
needs to be allowed to update the Stage's workload. A certificate that names
a URI, an email outside `subjects` or a workload domain such as
`*.iam.gserviceaccount.com` is an agent and never approves.

A certificate shows neither groups nor a login time, so the command records
the Approval in the cluster instead when no signer matches, the Stage asks for
a recent sign-in, `humanGroups` is set or owner teams narrow who may answer.
`--keyless=false` always records it in the cluster.

With no kubeconfig, approve the operation an intent record names:

```bash
pacto delivery approve --intent registry.example/pacto/streams@sha256:... \
  --trust recorder.pub --signers identities.yaml --decision approve
```

Public-good Fulcio logs every certificate, the approver's email included, to
Certificate Transparency. Run a private Fulcio to keep emails out of a public
log.

## Read one record with no cluster

`record get` and `record verify` take a record reference
(`<repository>:<tag>` or `<repository>@<digest>`) and read no kubeconfig:

```bash
pacto delivery record verify registry.example/pacto/streams@sha256:... \
  --trust recorder.pub --signers identities.yaml
```

For an approval it says `decision recorded (signature verified)`, then
`counted`, `not counted` or `unknown` when the stream cannot be read or holds
no record of the operation yet. `--signers` takes a file holding the
`pacto-delivery-identities` ConfigMap's `policy.json`, which verifies an
approval a person signed. For an intent it prints the
`pacto delivery approve --intent` command that approves it. A reference that finds no records is unknown, never
an empty stream.

## Upgrade: approval records

Readers decode records strictly, so an earlier CLI or promoter cannot read an
operation record that names its approvals' records: it reports the record
unreadable and a follower stops promoting. Upgrade the CLIs and every promoter
that follows these streams before you upgrade the recorder. Operation records
written with no approval records are unchanged.
