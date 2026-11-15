# PostgreSQL HA

Highly available PostgreSQL for critical workloads. A second provider of the `postgresql` offering, run by the platform-data team beside the infra-data team's primary database.

## Requesting a database

A consuming service depends on `postgresql-ha` and requests a database through its `provisioning` configuration, with the same values as the primary provider: `size`, `storageGi`, `highAvailability` and `backups.retentionDays`.

## Availability

Each database runs replicated across zones. Consumers that cannot reach it report `DEPENDENCY_UNREACHABLE`, which is how Pacto reads its availability when no target reports it.
