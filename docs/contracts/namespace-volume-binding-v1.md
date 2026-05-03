# Contract: Namespace Volume Binding V1

Status: draft

Namespace volume binding is owned and enforced by AFSCP. Trusted callers may request or configure bindings according to deployment policy, but they do not provide authoritative raw filesystem paths.

## Required Fields

- `namespace_id`
- `default_volume_id`
- `quota_bytes_default`
- `export_policy`
- `template_policy`
- `status`

## Rules

- A binding is selected when creating new repos.
- Changing a binding affects new repos only.
- Existing repos require explicit migration.
- AFSCP computes and owns the canonical namespace root for each volume.
- `cross_namespace_clone_enabled` defaults to false in P0.
- Template policy may enable namespace templates but not cross-namespace templates in P0.
