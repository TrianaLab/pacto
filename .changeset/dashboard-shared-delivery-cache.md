---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A dashboard people sign in to now reads delivery Stages and Operations from one
shared cache, which its ServiceAccount lists and watches, instead of listing
them again with every person's token. Each person sees only what their own
RBAC allows: the dashboard asks the API server who their token is and, with a
SubjectAccessReview, whether they may make the same list or get, keeping each
answer 10 seconds; a list across every namespace needs a cluster-wide allow.
Every action (propose, approve, cancel, roll back, unfreeze) and every read it
is built on still uses the person's own token, and the dashboard reads with
that token until the cache has loaded and for a moment after the person
changes something. With 2,000 Stages and 10,000 Operations, fifty people
opening the inbox at once wait about a second instead of 12. The operator
grants the dashboard's ServiceAccount get, list and watch on Stages and
Operations, nothing more in the delivery group, and holds that right itself
while the dashboard is enabled.
