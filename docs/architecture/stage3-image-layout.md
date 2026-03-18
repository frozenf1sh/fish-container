# Stage 3 Image Layout

## Unified Data Root

Default data root is `/var/lib/fish-container`.

```text
<data-root>/
  images/
    blobs/sha256/<digest>
    manifests/<registry>/<repo>/<tag>.json
    refs/<registry>/<repo>/<tag>
    indexes/<registry>/<repo>/<tag>.json
  snapshots/
    overlay/<chain-id>/lower
    overlay/<container-id>/upper
    overlay/<container-id>/work
    overlay/<container-id>/merged
  containers/<id>/config.json
  runtime/<id>/state.json
  network/
  logs/
```

## Naming Rules

- `blobs/sha256/<digest>` stores CAS content without the `sha256:` prefix.
- `<registry>` is normalized to lowercase.
- `<repo>` keeps slash-separated path form.
- refs file contains target manifest digest as plain text.

## Goal

This layout allows dedupe by digest, enables reference lookup by tag, and keeps room for future garbage collection and image prune.
