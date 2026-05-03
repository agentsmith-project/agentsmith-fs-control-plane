# Workload Mounts

AFSCP should generate generic workload mount specs. It should not know the caller's workload product or runtime workflow.

## Current Problem

Product-specific systems often pass raw filesystem names, metadata URLs, or storage credentials into runtime managers. That makes storage isolation depend on application code and leaks backend details.

## Target Contract

New AFSCP-backed repos should use a mount spec with structured IDs and an AFSCP-resolved subdirectory.

Draft:

```json
{
  "namespace_id": "ns_123",
  "repo_id": "repo_123",
  "volume_id": "vol_default",
  "payload_subdir": "/afscp/namespaces/ns_123/repos/repo_123",
  "mount_path": "/workspace",
  "read_only": false,
  "secret_ref": {
    "namespace": "storage-system",
    "name": "juicefs-vol-default"
  }
}
```

The final field names should be agreed with the orchestrator that consumes this spec.

## Responsibilities

AFSCP:

- resolves `payload_subdir`
- chooses volume
- chooses Secret reference
- validates namespace/repo consistency
- ensures `.jvs` is protected before enabling writable workload mounts

External orchestrator:

- creates or updates Secret/PV/PVC/Pod mount or equivalent runtime mount
- reports binding status
- does not make product authorization decisions

Workload:

- sees only mounted repo payload path
- receives no JuiceFS root credentials
- should run non-root by default
- must not be able to read or write `.jvs`

## Compatibility

Caller-specific compatibility adapters can translate from legacy business contracts to this generic mount spec outside AFSCP core.
