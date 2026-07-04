# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, report them privately:

- Use **[GitHub Security Advisories](https://github.com/thesatellite-ai/lorewire/security/advisories/new)** (preferred), or
- Email **khanakia@gmail.com** with the details.

Please include:

- A description of the vulnerability and its impact.
- Steps to reproduce (proof-of-concept if possible).
- Affected version(s) and environment.

We will acknowledge your report within a few days and keep you updated on remediation. We ask that you give us a reasonable window to release a fix before any public disclosure, and we're happy to credit you in the advisory.

## Scope note

lorewire stores messages in a local, unencrypted SQLite file and assumes cooperative sessions on a single user's machine. Secret payloads are delivered consume-once and masked in non-consuming peeks, but the store is not a hardened multi-user secrets vault. Threats that assume a hostile local user or a shared/multi-tenant host are out of scope for the current design — but please still report anything surprising.

## Supported versions

Security fixes are applied to the latest released version. Older versions may not receive patches; please upgrade to stay supported.

| Version | Supported |
|---|---|
| latest | ✅ |
| older | ❌ |
