# PostgreSQL

Primary relational database providing transactional storage for orders, payments and other domain services.

## Requesting a database

A consuming service requests a database by referencing this contract's `provisioning` configuration and giving the values it controls: `size`, `storageGi`, `highAvailability` and `backups.retentionDays`. Pacto validates those values against `configuration/provisioning.schema.json` wherever it checks the consumer, and the request is published with the consumer's contract.

## Fulfilment

The platform team runs an adapter that reads published consumer contracts, maps the `postgresql` offering to the database implementation the platform runs, and reports each resource's state back to Pacto as signed `ResourceProvisioned` evidence. The contract names no implementation, so the platform can change how it fulfils the offering without changing any consumer.

## Lifecycle

PostgreSQL 16 is deprecated from 2026-11-01 and may be retired from 2027-06-30. Move to `oci://ghcr.io/trianalab/pacto/postgresql:17.0.0` before then. Consumers see the deprecation as a `DEPENDENCY_SERVICE_DEPRECATED` warning.

## Persistence

Data is stored on persistent volumes with automated backups.
