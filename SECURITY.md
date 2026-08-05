# Security Policy

## Supported versions

Security fixes target the latest released version on the `main` branch and the
most recent GitHub Release. Older tags are not maintained.

## Reporting a vulnerability

**Do not open a public issue for security problems.**

Please report vulnerabilities privately using one of:

1. **[GitHub Security Advisories](https://github.com/guillaumemeyer/tmon/security/advisories/new)**
   (preferred) — "Report a vulnerability" on the repository Security tab
2. A private message to the repository maintainers via GitHub

Include:

- A description of the issue and its impact
- Steps to reproduce or a proof of concept when safe to share
- Affected version or commit if known

## What to expect

- Acknowledgement when a maintainer has seen the report
- An initial assessment of severity and scope
- A coordinated fix and disclosure timeline when the report is valid

We will not take legal action against good-faith research that follows this
policy and avoids privacy harm, service disruption, or data destruction.

## Scope notes for tmon

tmon is a local tmux status monitor. Reports that matter most include:

- Path traversal or unsafe writes outside intended state / hook directories
- Command injection via tmux, hooks, or agent config merge
- Checksum or install-script bypass that installs untrusted binaries
- Information leaks of secrets from agent configs or hook state

Out of scope (unless they cause a concrete security impact in tmon):

- DoS by running many agents or a very short poll interval
- Issues only in third-party AI agents that tmon observes
- Social engineering of individual users

## Prefer private disclosure

After a fix is released, we may credit reporters who want public credit.
Do not publish exploit details until a fixed release is available, unless we
agree otherwise.
