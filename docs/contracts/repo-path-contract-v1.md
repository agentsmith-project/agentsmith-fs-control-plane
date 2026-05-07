# Contract: Repo Path V1

Status: GA implementation-baseline contract. FINAL GA is governed by
`docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and
`scripts/verify-ga-release.sh`.

AFSCP resolves repo paths from structured IDs. Callers never provide raw filesystem paths.

## Inputs

- `namespace_id`
- `repo_id`
- `repo_kind`
- `volume_id`

## Outputs

- internal `control_root_path`
- internal `payload_root_path`
- internal `control_volume_subdir`
- orchestrator `payload_volume_subdir`

`control_root_path` is the absolute JVS external control root used only by AFSCP. `payload_root_path` is the absolute JVS `main` workspace folder.

`payload_volume_subdir` is relative to the JuiceFS filesystem root and is the only path-like value that may appear in an ordinary orchestrator mount plan. It points to the repo payload root, never to the control root.

GA subdirs include the AFSCP managed subroot prefix:

```text
control_volume_subdir = afscp/namespaces/<namespace_id>/repos/<repo_id>/control
payload_volume_subdir = afscp/namespaces/<namespace_id>/repos/<repo_id>/payload

template control_volume_subdir = afscp/namespaces/<namespace_id>/templates/<template_id>/control
template payload_volume_subdir = afscp/namespaces/<namespace_id>/templates/<template_id>/payload
```

## ID Grammar

GA IDs must use strict ASCII grammar:

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
- Control roots are never exposed through user/export/workload payload paths.
- `.jvs` is never present in AFSCP-managed payload roots.
- WebDAV/export still rejects root-level `.jvs` access and creation attempts as defense-in-depth.
- Canonical namespace root mismatch is an authorization boundary violation.

## Test Corpus

The same resolver test corpus must be used for:

- WebDAV methods
- workload mount plan generation
- migration/import tools
- future file APIs
- operator/debug APIs that accept relative paths
