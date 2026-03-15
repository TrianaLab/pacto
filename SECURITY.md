# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |

Only the latest release is actively supported with security updates. We recommend always running the most recent version.

## Reporting a Vulnerability

If you discover a security vulnerability in Pacto, please report it responsibly. **Do not open a public GitHub issue.**

### How to Report

1. **Email:** Send a detailed report to the maintainers via [GitHub Security Advisories](https://github.com/TrianaLab/pacto/security/advisories/new).
2. Include the following in your report:
   - A description of the vulnerability
   - Steps to reproduce the issue
   - The potential impact
   - Any suggested fixes (if applicable)

### What to Expect

- **Acknowledgment:** We will acknowledge receipt of your report within **48 hours**.
- **Updates:** We will provide status updates as we investigate and work on a fix.
- **Disclosure:** Once a fix is released, we will coordinate with you on public disclosure. We aim to resolve critical issues within **30 days**.

## Security Practices

- Pacto runs at **build time and CI time only** — it has no runtime agents, sidecars, or persistent infrastructure.
- Contracts are distributed as **OCI artifacts** through standard container registries.
- All dependencies are kept up to date and monitored for known vulnerabilities.

## Scope

The following are in scope for security reports:

- The `pacto` CLI and its core libraries
- Official plugins (e.g., `pacto-plugin-schema-infer`)
- OCI artifact push/pull operations
- Contract validation logic

The following are **out of scope**:

- Third-party integrations or tools consuming Pacto contracts
- Vulnerabilities in upstream dependencies (report these to the upstream project, but let us know so we can update)
