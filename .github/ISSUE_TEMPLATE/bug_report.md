---
name: Bug report
about: Report a defect in tmon (status, dashboard, hooks, install, or connectors)
title: "[bug] "
labels: bug
assignees: ""
---

## What happened

A clear description of the unexpected behaviour.

## What you expected

What should have happened instead.

## Steps to reproduce

1.
2.
3.

## Environment

- tmon version (`tmon version`):
- OS and arch:
- tmux version (`tmux -V`):
- Install method (TPM / manual / other):
- Go version (if built from source):

## Diagnostics

Paste relevant output (redact secrets and tokens):

```bash
tmon doctor
# and if useful:
tmon status --json
```

## Extra context

Logs, screenshots, or config snippets (`@tmon-*` options). Do not paste private keys or API tokens.
