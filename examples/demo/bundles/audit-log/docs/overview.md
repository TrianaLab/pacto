# audit-log

An append-only audit trail. It consumes domain events from across the fleet and
persists them durably for compliance and forensic review.

In the demo it illustrates an **observed-only (shadow) dependency**: the audit
log is observed calling `payments-service` at runtime even though it declares no
contract dependency on it — the kind of drift the Impact view surfaces when
observed evidence is included.
