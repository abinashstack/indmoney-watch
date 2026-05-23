# Security Policy

## Reporting a vulnerability

If you've found a security issue in `indmoney-watch`, please **don't open a public issue**. Report it privately so we can fix it before disclosure:

- **GitHub private vulnerability report**: https://github.com/abinashstack/indmoney-watch/security/advisories/new (preferred — auditable, integrates with the fix workflow)
- **Email**: open a GitHub security advisory above, or DM the maintainer on the platform where you found the project

Please include:

- A clear description of the issue and its impact
- Steps to reproduce, or a proof-of-concept
- Affected version(s) (commit SHA is most precise)
- Your suggested fix, if you have one — appreciated but not required

## Response expectations

This is a hobby project maintained by one person, but security takes priority. You should expect:

- **Acknowledgement within 7 days**
- An initial assessment (severity, scope, fix path) within 14 days
- A fix or mitigation timeline communicated by then
- Public disclosure coordinated with the reporter — typically after a fix is released

## Scope

In scope:

- The `indw` CLI binary
- The OAuth + token storage flow
- The MCP client, alert engine, SwiftBar plugin

Out of scope:

- The INDmoney MCP server itself (report to INDmoney)
- Vulnerabilities in dependencies — we run `govulncheck` in CI and Dependabot for security updates; if you find something they miss, we still want to know
- Issues that require local root or physical access (the threat model assumes a single-user macOS account)

## Hardening notes for self-deployers

- OAuth tokens live in macOS Keychain, not in files. The fallback case is also covered in the audit notes.
- The SwiftBar plugin script is installed with `0700` (owner-only) to defend against local plugin-swap attacks. If you're upgrading from an older `indw`, re-run `indw menubar install` to apply the tighter perms.
- macOS notifications are rendered via `osascript` with strings passed through environment variables, not concatenated into the AppleScript body — INDmoney-supplied names cannot inject AppleScript.

## Disclosure history

No advisories yet.
