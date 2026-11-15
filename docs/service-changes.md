# A service's changes and history

The service page, `pacto fleet entity` and `pacto_fleet_entity` answer what
changed in a service and which revision brought it. Delivery history answers
what each Stage ran at a past time. Both read
only what Pacto holds: contract revisions and signed delivery records. Neither
keeps a store of past snapshots.

## What changed in this service?

The **What changed in this service?** section of the
[service page](dashboard-service-page.md) lists each declared change between
the revisions Pacto holds, newest first in version order: the owner, the
business domain, the offering, a dependency, the lifecycle, an environment or
a validation finding that appeared or resolved. Each row names the revision
that shows the change and the one it was compared with. Chips above the table
filter it by kind. A sequential owner handover is an owner change, never an
ownership conflict.

![The Changes section of a service page: domain, environment and owner changes by version, each with the revision it was compared with and when it reached a Stage or time not recorded](assets/screenshots/dashboard-service-changes-light.png#only-light)
![The Changes section of a service page: domain, environment and owner changes by version, each with the revision it was compared with and when it reached a Stage or time not recorded](assets/screenshots/dashboard-service-changes-dark.png#only-dark)

Version order is the order Pacto reads from the versions themselves. No source
lists every version, so a change happened after the revision it was compared
with and at or before the revision that shows it. A revision Pacto could not
read is counted, and no change is reported across it.

## When a change reached a Stage

The **Reached** column says when the revision first reached each Stage you may
read, from that Stage's delivery records, read with your identity:

- A move a verified record signed shows its time, and its Stage links to
  that Stage's Delivery history, where the record opens.
- A move read from an Operation's status reads **from status, not signed**.
- A record whose signature was not checked reads **recorded, signature not
  checked**.
- A row with no record reads **time not recorded**, and **time not recorded in
  the Stages read** when Pacto could not read every namespace.
- A row reads **time hidden** when you may not list Stages.

The fleet's read time and the version order never become a time.

## Recorded impact and today's graph

A delivery history line carries the `pacto.impact` verdict its record signed
when the change shipped, labelled **Impact recorded** with its time. On a
history whose signatures were not checked it reads **Impact recorded,
signature not checked**. **Analyse against today's graph**, on a change row
and on a history line, opens Change analysis for the two revisions: who the
change reaches today, which may differ from what the record said.

## What ran at a past time

The **As of (UTC)** field of Delivery history answers which revision each
Stage ran at one time, from its signed records:

![Delivery history as of a past time: each Stage with the version it ran, its owner, since when and whether the records make it known](assets/screenshots/dashboard-delivery-history-as-of-light.png#only-light)
![Delivery history as of a past time: each Stage with the version it ran, its owner, since when and whether the records make it known](assets/screenshots/dashboard-delivery-history-as-of-dark.png#only-dark)

Each Stage reads **known** when a verified record names what ran and nothing
after it is missing or unsigned, **partial** when a record names it but a
later move may be missing or is not signed and **unknown** when no record
precedes the time or the Stage could not be read. The caption counts the
Stages read and names the namespaces that could not be read.

## From the CLI and MCP

```bash
pacto fleet entity service payments-service --reached
pacto delivery history --all --service payments --at 24h --trust recorder.pub
```

`--reached` reads the times with your cluster credentials; `reached: true` on
a stdio `pacto_fleet_entity` does the same, and any other MCP server answers
`times: not-read`. `--at` takes a time like `--since`, with `--stage` or
`--all`, and takes no `--since`, `--until`, `--cause` or `--correlation-id`;
`at` in `pacto_delivery_history` follows the same rules. The answer is
`partial` whenever a namespace could not be read.

`pacto delivery history` prints an `IMPACT` column, the verdict the record
signed, marked `(unverified)` when no trust config checked it. Its
`REVISION (DERIVED)` column, the MCP `revisions` map and the blame answer's
`revision` are derived from the artifact's contract digest and name, not
checked against a fleet; a contract Pacto could not read leaves them empty.
The dashboard links only revisions its fleet holds. See
[signed records and history](integrations/kubernetes/delivery-records.md#history-blame-and-undo).
