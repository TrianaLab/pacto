# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |

Only the latest release is actively supported with security updates. We recommend always running the most recent version.

## Reporting a Vulnerability

If you discover a security vulnerability in the Pacto Operator, please report it responsibly. **Do not open a public GitHub issue.**

### How to Report

1. **Email:** Send a detailed report to the maintainers via [GitHub Security Advisories](https://github.com/TrianaLab/pacto-operator/security/advisories/new).
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

- The operator is **read-only and non-intrusive** — it never modifies your workloads. It only reads cluster state and compares it against contracts.
- The operator runs with **least-privilege RBAC** — it only requests the permissions it needs.
- Container images are built with a **distroless base** and run as non-root.
- All dependencies are kept up to date and monitored for known vulnerabilities.

## Scope

The following are in scope for security reports:

- The Pacto Operator controller
- CRD definitions and validation logic
- RBAC permissions and cluster access
- OCI artifact resolution
- Helm chart security defaults

The following are **out of scope**:

- The [Pacto CLI](https://github.com/TrianaLab/pacto) (report to that repository)
- Third-party integrations consuming Pacto CRs
- Vulnerabilities in upstream dependencies (report these to the upstream project, but let us know so we can update)
