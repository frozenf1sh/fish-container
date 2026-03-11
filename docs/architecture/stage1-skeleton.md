# Stage 1 Skeleton Architecture

## Directory Layout

```text
cmd/
  fish-container/
internal/
  cli/
  runtime/
  image/
  network/
  cgroups/
  store/
docs/
  architecture/
  adr/
```

## Module Responsibilities

- `cmd/fish-container`: process entrypoint and CLI process exit policy.
- `internal/cli`: command routing and user-facing command set.
- `internal/runtime`: lifecycle orchestration for containers.
- `internal/image`: OCI image pull and unpack contracts.
- `internal/network`: network resource attach/detach contracts.
- `internal/cgroups`: cgroup v2 apply/delete contracts.
- `internal/store`: state persistence and content-addressed metadata access.

## Dependency Direction

- `cmd` -> `internal/cli`
- `internal/cli` -> `internal/*` interfaces only
- module implementations must not import `cmd`

## Runtime Paths (Plan)

```text
/var/lib/fish-container/
  images/
  snapshots/
  containers/
  runtime/
  network/
  logs/
```

## Stage 1 Constraints

- Keep business logic out of `cmd` package.
- Keep module APIs stable before starting namespace and mount work.
- Keep all high-risk operations behind runtime interfaces.
