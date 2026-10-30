# catalog-service

1.2.0 adds preorder to ProductStatus, the enum every product response refers to with $ref. A consumer that switches over every status meets a value it does not know, so the change is breaking although it shipped as a minor release.

This service is part of the synthetic demo fleet: its contract is made up.
