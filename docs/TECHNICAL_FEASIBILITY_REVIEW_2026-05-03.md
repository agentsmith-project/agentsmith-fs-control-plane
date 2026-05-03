# Technical Feasibility Review 2026-05-03

Status: handoff-blocking feasibility review.

This review intentionally tried to disprove the current product design before implementation. It checked the AFSCP docs, local JVS source and rebuilt binary, local `mbos-sandbox-v1`, local AgentSmith and Desktop code, JuiceFS/CSI/WebDAV documentation, Kubernetes storage/RBAC documentation, and small POSIX permission experiments.

Important: `agentsmith-oss` was not used.

## Executive Conclusion

The core product direction is still sound:

- one or more shared JuiceFS filesystems can back many namespaces and repos
- AgentSmith workspace/tenant configuration can map to AFSCP namespaces without making AFSCP understand AgentSmith business objects
- ordinary concurrent file reads and writes can remain outside AFSCP locks
- JVS can provide save points, restore, and repo clone semantics when AFSCP pins a rebuilt binary that contains those commands
- cross-namespace template clone should remain rejected

The main design risk is narrower and sharper: stock JuiceFS CSI can mount a repo subdirectory, but it does not provide a child-path filter that hides `.jvs` while exposing the rest of the repo root as writable. A writable workload mount of the current JVS repo root is therefore not safe with stock CSI alone.

## Hard Blockers

### 1. Stock JuiceFS CSI Cannot Hide `.jvs`

Current AFSCP layout puts JVS control data at:

```text
<repo_root>/.jvs/
```

and also wants `<repo_root>` to be mounted as the workload home. That exposes the JVS control directory to arbitrary code running inside the sandbox.

JuiceFS CSI supports mounting a subdirectory as the volume root, and Kubernetes supports `volumeMounts.subPath`. These are repo-level root selection mechanisms. They do not hide a child directory inside the mounted root.

Permission-only protection is insufficient. A local POSIX experiment confirmed that a user can rename a root-level `.jvs` directory even when the `.jvs` directory itself is `000`, as long as the parent repo root is writable:

```text
RESULT rename_succeeded_with_child_mode_000
```

Decision:

- AFSCP must not mark a volume as workload-mount-capable when `filtered_mount=false` and JVS control data remains under the mounted root.
- Stock JuiceFS CSI `subdir`, Kubernetes `subPath`, non-root pod defaults, and `chmod 000 .jvs` do not satisfy the P0 `.jvs` protection gate.

Acceptable resolution options:

- Preferred: change the JVS/AFSCP integration so control metadata lives outside the workload-visible payload root.
- Alternative: implement a real filtered filesystem view in the orchestrator/runtime that blocks lookup, read, write, create, rename, unlink, chmod, chown, hardlink, and symlink operations targeting root-level `.jvs`.
- Interim: reject workload mounts for that volume/namespace until one of the above is implemented and tested.

Do not ship a writable sandbox home backed by the current repo-root layout through stock CSI alone.

### 2. Stock JuiceFS WebDAV Is Not Enough For AFSCP Export Policy

AFSCP needs method-aware filtering for:

```text
GET PUT DELETE MKCOL MOVE COPY PROPFIND PROPPATCH LOCK UNLOCK
```

including `Destination` handling for `MOVE`/`COPY`, encoded path bypass attempts, and symlink escape checks.

The built-in JuiceFS WebDAV server is useful as a low-level capability, but it should not be treated as the AFSCP policy gateway. AFSCP needs its own WebDAV gateway or a wrapper that applies the canonical resolver and `.jvs` policy before filesystem access.

Decision:

- P0 WebDAV export must be an AFSCP-controlled gateway.
- Do not rely on JuiceFS WebDAV `disallowList` or a simple reverse proxy method allowlist for `.jvs` protection.

### 3. Revoke Is Not A CSI Primitive

AFSCP can issue mount bindings, leases, and revoke requests, but active Kubernetes pods can keep using mounted volumes until the orchestrator stops the pod or unmounts the volume. Deleting a Secret/PV/PVC is not a reliable runtime writer revoke mechanism for already-running workloads.

Decision:

- The orchestrator must own pod stop/evict/unmount behavior and report terminal binding status.
- Restore-run must treat stale or uncertain read-write bindings as active until reconciliation proves the runtime is stopped.

### 4. Current Sandbox Binding Contract Bypasses The New Credential Boundary

Current `mbos-sandbox-v1` creates JuiceFS Secret/PV/PVC from caller-provided `metadata_url`, storage endpoint, and subdir, and returns Secret/PV/PVC names. That is incompatible with the AFSCP split where ordinary product callers receive only an opaque binding while the orchestrator receives the privileged plan.

Decision:

- Add a sandbox/orchestrator v2 path that consumes AFSCP `OrchestratorMountPlan`.
- Stop using caller-provided `metadata_url` for new AFSCP-backed repos.
- Map `volume_subdir` to the JuiceFS CSI-supported subdirectory mechanism, not the current unverified `volumeAttributes["subdir"]` shape.

### 5. Current Desktop And AgentSmith File Library Flows Are Direct JuiceFS Flows

Current AgentSmith and Desktop flows still return or consume `metadata_url`, bucket URL, and direct `juicefs mount` details. Those paths bypass AFSCP export sessions, heartbeat/revoke, writer-session fencing, and audit.

Decision:

- Desktop ordinary access must move to AFSCP-backed WebDAV export credentials.
- Raw JuiceFS desktop mount should be disabled for ordinary users on AFSCP-backed repos.
- AgentSmith needs a mapping layer from product file-library records to AFSCP namespace/repo IDs.

## JVS-Specific Gates

These are not product blockers if handled before implementation:

- Pin and package a JVS binary built from a commit that includes `repo clone` and the lifecycle commands AFSCP depends on.
- CI should smoke-test `jvs repo clone --help`, `jvs init`, save, history, restore preview/run, and `doctor --strict`.
- Execute AFSCP JVS commands from a clean working directory outside another JVS repo. A local smoke test passed from `/tmp`; running from inside the JVS source repo with `--repo <other-repo>` produced a target mismatch.
- Template create must fence source writers while saving and cloning, or must fail/cleanup if the source becomes dirty between the fresh save point and clone completion.
- Restore API must model JVS dirty-state decisions. P0 should choose explicit behavior for dirty repos, such as fail closed unless caller requests a supported `discard_unsaved` or `save_first` mode.
- Restore failure must integrate JVS recovery status/resume/rollback or enter an explicit `operator_intervention_required` state with a runbook.
- JVS repo clone target paths must not be pre-created by AFSCP before invoking `jvs repo clone`.
- Save point creation must include a non-empty message, supplied by the caller or generated by AFSCP.

## Product Impact

The review does not require AFSCP to become AgentSmith-specific. The opposite is true: keeping AFSCP generic is still the right product boundary.

What must change is the confidence level in P0 workload mounts. The design should not imply that shared JuiceFS plus CSI subdir automatically solves sandbox home mounting for JVS repos. It solves repo-level isolation, but not `.jvs` protection.

Recommended P0 gate:

1. Choose and prove one `.jvs` resolution strategy before implementing workload mount handlers.
2. Keep WebDAV export in P0, but implement it as an AFSCP policy gateway.
3. Keep workload mount API contracts in P0, but return a capability error for runtimes that cannot prove `.jvs` protection.
4. Do not migrate ordinary Desktop or AgentSmith runner flows to AFSCP-backed repos until direct JuiceFS credentials are removed from those paths.

## Sources Checked

Local source and docs:

- `/home/percy/works/mbos-v1/jvs`
- `/home/percy/works/mbos-v1/mbos-sandbox-v1`
- `/home/percy/works/mbos-v1/agentsmith`
- `/home/percy/works/mbos-v1/agentsmith-desktop`

Primary external references:

- JuiceFS CSI configuration and subdirectory mount options: https://juicefs.com/docs/csi/guide/configurations/
- JuiceFS CSI PV and Secret reference model: https://juicefs.com/docs/csi/guide/pv/
- JuiceFS command reference, including mount and WebDAV options: https://juicefs.com/docs/community/command_reference/
- JuiceFS WebDAV deployment: https://juicefs.com/docs/community/deployment/webdav/
- JuiceFS POSIX compatibility: https://juicefs.com/docs/community/posix_compatibility/
- Kubernetes volumes and `subPath`: https://kubernetes.io/docs/concepts/storage/volumes/
- Kubernetes persistent volume protection: https://kubernetes.io/docs/concepts/storage/persistent-volumes/
- Kubernetes Secret and RBAC good practices: https://kubernetes.io/docs/concepts/configuration/secret/ and https://kubernetes.io/docs/concepts/security/rbac-good-practices/
- WebDAV method semantics: https://www.rfc-editor.org/rfc/rfc4918.html
