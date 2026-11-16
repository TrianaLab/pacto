# External catalog reference

The external catalog names the systems outside your fleet that services call:
vendor APIs, managed cloud services and legacy hosts. Each entry maps hosts,
configuration key names and OpenTelemetry system values to one canonical
identity and a class. Pacto embeds a default catalog. A reader that cannot
reach the published copy classifies with the embedded one and reports its
digest followed by `(embedded)`.

## Identities and classes

An identity is a lower-case path with no cluster component, so a vendor is the
same system whichever cluster calls it:

| Form | Example | Class |
|------|---------|-------|
| A vendor | `stripe` | `vendor-api` |
| A managed cloud service, or one resource of it | `aws/rds`, `aws/rds/123456789012/eu-west-1/orders-db` | `managed-cloud` |
| A host the catalog does not name | `host/corp-legacy.co.uk` | `unclassified` |

The operator reports a host that an `ExternalName` Service, a selectorless
Service or a `ServiceEntry` names, and no catalog entry classifies, as
`legacy`.

## Matching

`pacto init`, the operator and the fleet all classify through the same
matcher.

| Input | Rule | Confidence |
|-------|------|------------|
| A host | The longest catalog host rule wins, an exact host over a `*.` wildcard of the same length. An exact rule matches only that name, and `*.example.com` matches its subdomains and never `example.com` itself | `likely` |
| A host no rule names | Three or more labels and a public registrable domain give `host/<registrable-domain>`. A two-label name matches only a catalog rule, because a namespace short form such as `orders.shop` reads like a vendor's domain | `likely` |
| A configuration key name | Compared without case against the entries' key patterns, with `-` and `.` read as `_`, so the Secret name `stripe-credentials` reads like `STRIPE_CREDENTIALS` | `possible` |
| An OpenTelemetry `db.system`, `messaging.system` or `rpc.system` value | Compared exactly, without case, against the entries' values. A protocol no entry claims, such as `grpc`, gives no match. The default catalog claims only values that name one managed service, such as `aws_sqs` or `dynamodb`, so a generic `postgresql` or `kafka` callee stays unresolved | `likely` |

A configuration value is a host candidate only when it has a scheme
(`https://`), a port or a key named like a host: one that contains `url`,
`uri`, `host`, `endpoint`, `addr`, `address`, `server` or `broker`. A
value that names no host can still match by its key name. `likely` means a
host or a system value matched. `possible` means only a key name did, and
nothing else confirms it.

### Redaction

The matcher reads a host and a port and nothing else:

- A URL keeps only its host and port. The user name, password, path, query and
  fragment are never read, so
  `postgres://user:pass@db.xyz.rds.amazonaws.com:5432/app?password=x` shows as
  `db.xyz.rds.amazonaws.com:5432`.
- A value under a key that names a password, secret, token, key, credential,
  private key or DSN is never parsed. Only its key name is matched. The rule
  reads parts of a name, so a `DATABASE_DSN` or `KEYCLOAK_URL` value gives no
  host either.
- A Secret contributes its name and key names, never a value.
- A comma-separated seed list, such as Kafka brokers, gives one host per entry,
  and a `jdbc:` prefix is dropped.

### Names that never match

An IP address, a single label, `localhost`, a name with a `svc` label (such as
`orders.shop.svc.corp.local`), a `*.cluster.local` name and a name with a label
outside RFC 1123 give no match. In-cluster names stay dependencies on services.
A value that does not parse gives no match, never part of the string.

## What's next

- [Pactify a service](pactify.md#what-pactify-recognizes): the externals
  `pacto init` finds in a folder
- [Operational graph](operational-graph.md#relationships-declared-observed-inferred):
  how observed and inferred edges to external systems join the graph
