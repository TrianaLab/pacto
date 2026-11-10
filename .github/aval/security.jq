# jq -e -f security.jq REPORT is true when an aval report records a security
# event (run.sh, summary.sh and publish.sh share it): a verification failure,
# as the outcome or among the rejected candidates beside a verified one, or a
# fingerprint flooded with more attestations than aval lists (aval prints
# "SECURITY", records outcome error, runs the test and exits 0 on a pass).
.outcome == "verification-failed"
or any(.rejected[]?; .outcome == "verification-failed")
or (.outcome == "error" and ((.reason // "") | test("more than [0-9]+ attestations")))
