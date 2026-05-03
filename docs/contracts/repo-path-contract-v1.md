# Contract: Repo Path V1

Status: P0 review draft

AFSCP resolves repo paths from structured IDs. Callers never provide raw filesystem paths.

## Inputs

- `namespace_id`
- `repo_id`
- `repo_kind`
- `volume_id`

## Outputs

- internal `repo_path`
- orchestrator `volume_subdir`
- internal `jvs_control_path`

`repo_path` is the absolute JVS repo root used only by AFSCP. `volume_subdir` is relative to the JuiceFS filesystem root and is the only path-like value that may appear in an orchestrator mount plan.

P0 `volume_subdir` includes the AFSCP managed subroot prefix:

```text
afscp/namespaces/<namespace_id>/repos/<repo_id>
afscp/namespaces/<namespace_id>/templates/<template_id>
```

## ID Grammar

P0 IDs should use strict ASCII grammar:

```text
namespace_id := ns_[A-Za-z0-9][A-Za-z0-9_-]{1,62}
repo_id      := repo_[A-Za-z0-9][A-Za-z0-9_-]{1,62}
template_id  := tmpl_[A-Za-z0-9][A-Za-z0-9_-]{1,62}
volume_id    := vol_[A-Za-z0-9][A-Za-z0-9_-]{1,62}
```

Reject empty IDs, path separators, percent-encoded separators, Unicode slash-like characters, control characters, leading dots, and display-name-derived path segments.

## Resolver Rules

- Decode protocol paths exactly once before validation; reject double-encoding bypasses.
- Reject caller-provided absolute paths.
- Reject `..` traversal before and after decode.
- Reject symlink escape.
- Reject hardlink escape where filesystem APIs can detect it.
- Reject namespace/repo/template mismatches before computing paths.
- Use dirfd/openat-style segment traversal with `O_NOFOLLOW` or equivalent for file APIs and export implementations.
- Do not concatenate user input into raw paths.
- Do not normalize case in a way that merges two distinct IDs.
- `.jvs` is never exposed through user/export/workload payload paths.
- Workload and export payload paths must block lookup, read, write, create, rename, unlink, chmod, chown, hardlink, and symlink operations targeting root-level `.jvs`.
- Canonical namespace root mismatch is an authorization boundary violation.

## Test Corpus

The same resolver test corpus must be used for:

- WebDAV methods
- workload mount plan generation
- migration/import tools
- future file APIs
- operator/debug APIs that accept relative paths
