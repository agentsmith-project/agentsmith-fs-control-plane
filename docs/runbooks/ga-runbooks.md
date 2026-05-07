# GA Runbooks

Status: pre-dev runbook draft for implementation handoff.

Every incident action must record operator, reason, correlation ID, affected
namespace/repo/export/mount/operation IDs, and audit event IDs.

## Shared Operator Rules

- Prefer API/operator tooling over raw filesystem access.
- Use `GET /internal/v1/operations/{operationId}` for stable internal API
  operation inspection by ID.
- Treat correlated-resource operation lookup, intervention queues, held-fence
  views, and audit outbox lag views as runbook, read-only database,
  observability dashboard, or deployment-side operator-tooling workflows. They
  are not GA internal API list/search or aggregation contracts.
- Do not expose or paste JuiceFS root credentials, metadata URLs, Secret values,
  WebDAV passwords, bearer tokens, or raw host paths into tickets or chat.
- If a state transition cannot be proven, move the operation to
  `operator_intervention_required` and keep the relevant fence held.
- Do not release a writer or lifecycle fence until the corresponding session,
  JVS, storage, and audit state is known.
- Do not manually delete JVS private restore-plan files. Pending preview cleanup
  uses the restore preview discard API, which invokes `jvs restore discard`.
- Purged storage must not be restored from backups into ordinary service without
  a new reviewed incident decision.

## Failed Repo Create

Symptoms:

- `repo_create` operation failed or stuck.
- JVS init output missing or doctor failed.

Actions:

- Inspect operation phase, allocated path state, and JVS JSON output.
- Verify no raw path or credential leaked into operation/error records.
- If path exists without durable repo record, mark for operator cleanup after
  confirming no caller-visible repo exists.
- Retry only from recorded phase or create a new operation after cleanup.

Terminal evidence:

- repo record active with doctor ok, or failed with cleanup decision.
- audit event emitted.

## Failed Save Point Create

Symptoms:

- `save_point_create` failed, timed out, or JVS returned dirty/current-state
  ambiguity.

Actions:

- Inspect JVS JSON and operation phase.
- Verify save point ID existence before retry.
- If existence is ambiguous, keep operation in intervention and do not issue
  restore/template operations from the ambiguous save point.

Terminal evidence:

- save point visible in history or operation failed with stable error.

## Failed Restore Preview

Symptoms:

- restore preview failed or returned unexpected JVS shape.
- same-repo save, restore, template, or clone is blocked by an active pending
  restore plan.
- recovery status reports `pending_restore_preview` or `stale_restore_preview`.

Actions:

- Confirm repo is active and no lifecycle fence is held.
- Inspect save point ID, preview operation phase, durable `RestorePlan`
  `restore_plan_id`, `source_save_point_id`, restore plan status, and redacted
  JVS output.
- Verify whether the worker persisted the preflight idle marker before JVS
  preview.
- Adopt a single pending JVS plan only if AFSCP-exclusive-control assumptions
  hold and the preview operation is the earliest same-repo non-terminal restore
  preview or JVS mutation; otherwise move the plan or operation to
  `operator_intervention_required`.
- Treat `stale_restore_preview` as intervention unless a caller explicitly
  invokes restore preview discard.
- Do not create restore-run operation without a valid preview operation and
  durable plan.

Terminal evidence:

- preview operation succeeded with durable `status=pending` plan, preview
  operation failed with no active JVS plan, plan discarded through the discard
  flow, or intervention record with owner.

## Discard Restore Preview

Symptoms:

- caller cancelled preview or no longer wants to run the pending plan.
- repo JVS mutations are blocked by a pending restore plan that should not be
  consumed.

Actions:

- Call `POST /internal/v1/repos/{repoId}/restore-preview:discard` with the
  matching `preview_operation_id`.
- Confirm namespace, repo, preview operation type, succeeded preview state,
  `restore_plan_id`, `source_save_point_id`, and plan `status=pending`.
- Let the worker mark the plan `discarding` and run `jvs restore discard
  <plan_id>` through the runner.
- If JVS discard confirmation or recovery status is ambiguous, move the plan or
  operation to `operator_intervention_required`.
- Do not delete `.jvs` private files.

Terminal evidence:

- plan `discarded` with audit event, or intervention record with owner.

## Failed Restore-Run

Symptoms:

- restore-run failed, doctor failed, or JVS recovery state is ambiguous.

Actions:

- Keep writer-session fence held.
- Inspect active/uncertain read-write sessions.
- Validate the referenced preview operation and durable plan: same
  namespace/repo/resource, type `restore_preview`, preview succeeded,
  `restore_plan_id` and `source_save_point_id` present, plan `pending`, and not
  referenced by a succeeded or non-terminal restore-run.
- Run `jvs recovery status` through the runner contract only.
- Before JVS restore-run, require exactly one pending JVS plan matching the
  stored plan ID.
- If writer/session gating denies the run before JVS is invoked, release the
  writer-session fence and leave the plan `pending`.
- After writer/session gating passes, confirm the plan was marked `consuming`
  before JVS restore-run.
- After JVS restore-run, require `jvs doctor --strict` success and recovery
  status idle before marking the plan `consumed`.
- If recovery cannot prove safe terminal state, mark
  `operator_intervention_required` and keep the writer-session fence held.
- Do not manually delete JVS private restore-plan files.

Terminal evidence:

- repo doctor ok, recovery status idle, plan `consumed`, audit emitted, and
  fence released; or intervention record with runbook owner and fence retained.

## Restore-Run Blocked By Writers

Symptoms:

- `ACTIVE_WRITER_SESSIONS` or `STALE_WRITER_SESSION_UNCERTAIN`.

Actions:

- List active read-write exports and workload mount bindings.
- Revoke or wait for expiry/reconciliation according to caller policy.
- Retry restore-run with same idempotency key only when request body is
  unchanged.

Terminal evidence:

- restore-run rejected with stable error, or retried after sessions terminal.

## Writer-Session Fence Stuck

Symptoms:

- new read-write sessions rejected with `WRITER_SESSION_FENCE_HELD`.
- no running restore-run appears healthy.

Actions:

- Inspect operation store for owning operation and lease.
- Reconcile process restart state.
- Release fence only after restore operation terminal state is proven.

Terminal evidence:

- fence released with audit reason, or intervention remains open.

## JVS Doctor Failure

Symptoms:

- `JVS_DOCTOR_FAILED`, doctor unhealthy, or recovery blocking state.

Actions:

- Capture redacted JVS JSON.
- Block restore-run, template clone, lifecycle reactivation, and purge until
  operator decision.
- Escalate to JVS owner when doctor output conflicts with recovery status.

Terminal evidence:

- doctor ok after recovery, or intervention record with disabled repo access.

## Failed Template Create Or Clone

Symptoms:

- template operation failed, source dirty after save, volume mismatch, clone JVS
  failure.

Actions:

- Inspect source save point, target path, and clone history mode.
- Clean up unpublished target template/repo only after operation phase proves no
  caller-visible resource was returned.
- Reject cross-namespace or cross-volume clone according to contract.

Terminal evidence:

- published template/active repo with doctor ok, or failed operation with cleanup
  decision.

## Repo Archive Blocked Or Failed

Symptoms:

- archive waits on sessions, lifecycle fence held, or health verification fails.

Actions:

- Confirm lifecycle fence owner.
- Drain/revoke all non-terminal exports and workload mounts.
- Keep repo active until retained storage state is safely archived.

Terminal evidence:

- repo `archived`, sessions terminal, audit emitted; or intervention required.

## Repo Restore From Archive Failed

Symptoms:

- archived repo cannot return to active.

Actions:

- Keep lifecycle fence held.
- Verify retained control and payload storage.
- Run doctor before returning active.
- If JVS health is ambiguous, keep repo unavailable and mark intervention.

Terminal evidence:

- repo active with health verification, or intervention record.

## Repo Delete Blocked By Sessions

Symptoms:

- delete operation running while exports or mounts are active/uncertain.

Actions:

- Revoke exports and workload mounts.
- Confirm WebDAV gateway reports no future access.
- Confirm orchestrator reports terminal non-accessing state.
- Keep lifecycle fence held.

Terminal evidence:

- repo tombstoned with retained storage marker and audit event, or intervention.

## Repo Tombstone Restore Failed

Symptoms:

- restore-tombstoned denied or health check fails.

Actions:

- Verify retention has not expired and repo is not purged.
- Verify product catalog still maps to repo ID.
- Restore to recorded pre-delete accessibility state only.
- Run doctor before reactivation.

Terminal evidence:

- repo restored to recorded state, or stable denial/intervention.

## Repo Purge Denied Or Failed

Symptoms:

- purge denied by retention, missing confirmation, active sessions, or storage
  failure.

Actions:

- Verify caller approval reference and reason.
- Verify namespace lifecycle policy.
- Verify lifecycle role and break-glass approval when retention override is
  requested.
- Verify no active or uncertain export/mount sessions.
- If physical removal is ambiguous, mark intervention and block reactivation.

Terminal evidence:

- repo purged with minimal audit/idempotency record retained, or denied with
  stable error.

## WebDAV Export Incident

Symptoms:

- denied path spike, credential misuse, traversal attempt, or unexpected method.

Actions:

- Revoke affected export sessions.
- Inspect access logs with credential redaction.
- Confirm no `.jvs`, control root, raw path, or credential exposure.

Terminal evidence:

- exports terminal, affected paths audited, incident outcome recorded.

## WebDAV Positive Runtime Count Repair

Symptoms:

- export runtime accounting shows a positive aggregate access count after a
  gateway crash or restart.
- lifecycle drain or revoke remains blocked because no matching negative delta
  or terminal observation has been committed.
- gateway logs, process state, or heartbeat evidence cannot prove the active
  request count is still nonzero.

Actions:

- Keep the export session non-terminal and keep lifecycle drain blocked until
  no-access evidence is recorded.
- Inspect the export session, runtime aggregate count, last observation time,
  revoke state, gateway process identity, and redacted gateway logs.
- Prefer the normal revoke and terminal reconcile path when the gateway is
  healthy enough to report a fresh zero-count observation.
- If the gateway process that held the positive count is gone, record operator,
  reason, correlation ID, export ID, repo ID, namespace ID, observed count,
  process evidence, and audit event IDs before applying a repair.
- Repair the aggregate count to zero only after independent evidence proves no
  future access can be served for the credential and no request from the crashed
  process remains active.
- If that proof is unavailable, leave the session blocking and move the owning
  operation to `operator_intervention_required`.

Terminal evidence:

- aggregate count repaired to zero with audit evidence and terminal reconcile
  completed, or intervention record with owner and lifecycle drain still
  blocked.

## WebDAV Credential Leak

Symptoms:

- password, URL credential, or bearer secret exposed.

Actions:

- Revoke export immediately.
- Rotate stored credential material if needed.
- Audit caller and actor context.
- Confirm logs and operation records are redacted.

Terminal evidence:

- credential invalidated and leak scope documented.

## Stale Workload Mount Lease

Symptoms:

- binding lease expired but not terminal.

Actions:

- Ask orchestrator for latest runtime state.
- Treat read-write stale lease as uncertain writer.
- Treat any non-terminal binding as lifecycle drain blocker when no access is
  allowed.

Terminal evidence:

- binding terminal or intervention with orchestrator owner.

## Workload Mount Revoke Stuck

Symptoms:

- binding remains `releasing`.

Actions:

- Confirm orchestrator received revoke.
- Confirm pod/runtime unmounted or unable to write.
- Do not mark `revoked` terminal from AFSCP intent alone.

Terminal evidence:

- confirmed terminal state or intervention.

## Operation Reconciliation After Crash

Symptoms:

- operations left `running` after process restart.

Actions:

- Reacquire operation leases.
- Run phase-specific recovery.
- Recover fences from durable state.
- Emit recovery audit events.

Terminal evidence:

- operations terminal or intervention with phase and external IDs.

## Caller Authorization Denial

Symptoms:

- `CALLER_NOT_ALLOWED`, `ROLE_NOT_ALLOWED`, namespace mismatch.

Actions:

- Verify authenticated principal to caller mapping.
- Verify namespace binding roles.
- Do not bypass with admin unless operator action is explicitly approved.

Terminal evidence:

- denied event audited and caller policy corrected or request rejected.

## Namespace Disable And Session Drain

Symptoms:

- namespace disabled while sessions exist.

Actions:

- Reject new mutations and new session issuance.
- Revoke or wait for session expiry according to policy.
- Allow only authorized operator inspection.

Terminal evidence:

- namespace disabled with sessions terminal or documented exceptions.

## JuiceFS Secret Rotation

Symptoms:

- planned rotation or suspected credential exposure.

Actions:

- Rotate platform Secret outside ordinary product access.
- Restart or reconcile mount/export components according to deployment policy.
- Confirm no ordinary caller received Secret references.

Terminal evidence:

- rotation audited and health checks pass.

## Volume Health Degradation

Symptoms:

- volume health check fails or reports degraded capability.

Actions:

- Stop new repo create on affected volume if unsafe.
- Reject exports/mounts when capability is unavailable.
- Keep operations retry-safe and visible.

Terminal evidence:

- volume active again, or namespace policy moved for new repos.

## Audit Outbox Lag Or Replay

Symptoms:

- audit delivery lag over threshold.

Actions:

- Inspect the durable outbox status distribution for `pending`, `retry_wait`,
  `delivering`, and `failed` records, preserving records for evidence.
- Confirm the audit delivery worker configuration is present and redacted in
  operator notes: delivery enabled, PostgreSQL DSN source, delivery owner,
  limit, max attempts, retry backoff, stale threshold, `http_json` sink kind,
  endpoint, optional bearer token, and timeout.
- Confirm the configured external sink treats `audit_event_id` as an idempotency
  key before replaying.
- Run a bounded delivery pass with `afscp-worker --run-once` using the audit
  delivery gate and configured HTTP JSON sink.
- Confirm sink behavior: only HTTP 2xx is success; 3xx redirects are not
  followed automatically; every POST carries `X-AFSCP-Audit-Event-Id`,
  `X-AFSCP-Audit-Event-Type`, and `Idempotency-Key` using the audit event ID.
- Recheck lag and status distribution. `pending`/`retry_wait` should drain when
  the sink is healthy; stale `delivering` should be recovered for replay before
  due delivery in the same run-once pass. A recovered record may remain
  `retry_wait` until its backoff is due.
- For terminal `failed` records or sustained lag, retain the outbox evidence,
  delivery summary, and sink-side receipt evidence, then open an incident or
  operator intervention item.
- Alert if denied/security events remain delayed after replay attempts.

Terminal evidence:

- lag cleared with sink receipts keyed by `audit_event_id`, or an incident is
  opened with retained outbox and worker summary evidence.
