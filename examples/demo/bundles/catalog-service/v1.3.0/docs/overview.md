# catalog-service

1.3.0 requests its own PostgreSQL database (pactoVersion 2.4). The `database` configuration references the `provisioning` configuration of postgresql 16.1.0 and gives the values catalog-service controls: a medium instance with 50 GiB of storage, and in production a standby replica and 14 days of backups. Pacto validates those values against the provider's schema. Publishing this contract is the request: the platform's adapter reads it and reports the database's state back as provisioning evidence. The committed `pacto.lock` pins which postgresql revision the reference resolves to.

1.2.1 declares the commerce business domain (pactoVersion 2.4) and changes nothing else.

1.2.0 adds preorder to ProductStatus, the enum every product response refers to with $ref. A consumer that switches over every status meets a value it does not know, so the change is breaking although it shipped as a minor release.

This service is part of the synthetic demo fleet: its contract is made up.
