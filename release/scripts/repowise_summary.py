#!/usr/bin/env python3
"""Format a deterministic Repowise change-risk + code-health report as Markdown.

Reads the JSON emitted by `repowise risk --format json` and `repowise health
--format json` and prints a Markdown summary. Invoked by the `ci-arch` make
target, which redirects stdout to $GITHUB_STEP_SUMMARY in CI (stdout locally).

Advisory only: it never exits non-zero. To hard-gate a signal later (e.g. block
genuinely high-risk changes), the caller can inspect risk["level"] itself.

Usage: repowise_summary.py RISK_JSON HEALTH_JSON
"""

import json
import sys


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return {}


def main():
    risk = load(sys.argv[1]) if len(sys.argv) > 1 else {}
    health = load(sys.argv[2]) if len(sys.argv) > 2 else {}

    out = ["## Repowise — architecture health (deterministic, zero-LLM)\n"]
    if risk:
        out.append(
            f"**Change risk:** `{risk.get('level', '?')}` "
            f"(percentile {risk.get('risk_percentile', '?')}, "
            f"review priority {risk.get('review_priority', '?')})\n"
        )

    kpis = (health or {}).get("kpis") or {}
    if kpis:
        out.append("**Health KPIs:**\n")
        out.extend(f"- {k}: {v}" for k, v in kpis.items())
        out.append("")

    findings = (health or {}).get("findings") or []
    if findings:
        out.append(f"**Top health findings ({len(findings)} total):**\n")
        for f in findings[:10]:
            loc = f.get("file", "?")
            msg = f.get("marker") or f.get("message") or f.get("title") or ""
            sev = f.get("severity") or f.get("score") or ""
            out.append(f"- `{loc}` — {sev} {msg}".rstrip())
        out.append("")

    out.append("_Advisory only — see the Repowise PR bot for inline comments._")
    print("\n".join(out))


if __name__ == "__main__":
    main()
