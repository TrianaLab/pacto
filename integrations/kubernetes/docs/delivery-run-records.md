# Delivery reference: run records

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

The promoter can publish a run record of every operation it concludes: its
type, outcome, gate results, how long it waited on a person, the class of who
asked, the Stage and the digests, never a name. Records are unsigned statistics
for `pacto stats`. Turn delivery on first ([Install](delivery-setup.md#install)).
Recording needs neither the recorder nor signing, and it works on GHCR.

Publishing is best effort and never changes an outcome. A record that could not
be published is counted in `pacto_delivery_stats_dropped_total`
([metrics](delivery-setup.md#metrics-and-events)), and a promoter that stops
between concluding an operation and queuing its record loses that record.

The repository must be private: pull access to it equals read access to the
delivery history of every Stage that records to it, so grant pull only to the
identities that run `pacto stats` or the dashboard. On a registry without the
native Referrers API, such as GHCR, concurrent writers to one service can lose
a record without it being counted. The `service` on a record is what the Stage
declares in `spec.service` and nothing verifies it, so statistics key delivery
rates by Stage.

```bash
helm upgrade pacto-operator oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --reset-then-reuse-values \
  --set delivery.stats.repository=ghcr.io/acme/pacto-stats
```

The promoter's own registry credentials are usually read-only. Give it push
access to the stats repository with a separate Secret:

```bash
kubectl -n pacto-delivery create secret docker-registry pacto-stats-push \
  --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>
helm upgrade pacto-operator oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --reset-then-reuse-values \
  --set delivery.stats.registryCredentials.secretName=pacto-stats-push
```

After the next concluded operation, list a service's records:

```bash
oras discover ghcr.io/acme/pacto-stats:pacto-stats-$(printf payments | shasum -a 256 | cut -d' ' -f1)
```

The [run record reference](../../run-record-reference.md#operation-records)
lists every field of an operation record.
