# GA Runbooks

Status: GA implementation-baseline runbook set.

Final GA is governed by `docs/GA_RELEASE_GATES.md`,
`docs/READINESS_EVIDENCE.md`, and `scripts/verify-ga-release.sh`. These
runbooks are repo-local operational artifacts; they do not create a separate
role-approval or meeting gate.

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
- Do not manually edit JVS private metadata. Direct restore recovery must use
  the durable operation phase, save point ID, writer-session fence, redacted
  direct JVS evidence, and explicit diagnostic commands.
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

## Failed Direct Restore

Symptoms:

- `restore` operation failed, timed out, or returned unexpected direct JVS
  shape.
- same-repo save, restore, template, or clone is blocked by a durable operation
  lease, writer-session fence, or JVS lock.
- direct JVS status or doctor reports metadata/recovery ambiguity.

Actions:

- Confirm repo is active and no lifecycle fence is held.
- Inspect operation phase, requested save point ID, writer-session fence owner,
  active/uncertain read-write sessions, and redacted direct JVS output.
- If the operation failed before the writer-session fence, confirm no direct JVS
  mutation was invoked before retrying from the durable phase.
- If the operation holds the writer-session fence, keep it held until direct JVS
  status, doctor, operation state, and audit evidence prove a terminal result.
- If direct JVS output is malformed, contains forbidden internal fields, or
  cannot prove the requested save point was restored, move the operation to
  `operator_intervention_required`.
- Do not infer success from filesystem contents alone; use durable operation
  state plus redacted direct JVS evidence.

Terminal evidence:

- operation terminal succeeded with restored save point, previous/new head IDs
  when available, audit emitted, and writer-session fence released; or
  intervention record with owner and fence retained when safety is uncertain.

## Direct Restore Blocked By Writers

Symptoms:

- `ACTIVE_WRITER_SESSIONS`, `STALE_WRITER_SESSION_UNCERTAIN`, or
  `WRITER_SESSION_FENCE_HELD`.

Actions:

- List active read-write WebDAV exports and workload mount bindings.
- Revoke, release, or wait for expiry/reconciliation according to caller
  policy.
- Treat stale or uncertain writer evidence as active until reconciliation marks
  it terminal.
- Retry direct restore with the same idempotency key only when the request body
  is unchanged and writer evidence is terminal.

Terminal evidence:

- direct restore rejected with stable error, or retried after sessions are
  terminal.

## Writer-Session Fence Stuck

Symptoms:

- new read-write sessions rejected with `WRITER_SESSION_FENCE_HELD`.
- no running direct restore operation appears healthy.

Actions:

- Inspect operation store for owning restore operation and lease.
- Reconcile process restart state.
- Release fence only after restore operation terminal state, direct JVS status,
  session state, and audit evidence are proven.

Terminal evidence:

- fence released with audit reason, or intervention remains non-terminal.

## JVS Direct Diagnostics Failure

Symptoms:

- `JVS_DOCTOR_FAILED`, doctor unhealthy, or recovery blocking state.

Actions:

- Capture redacted direct JVS JSON.
- Block direct restore, template clone, lifecycle reactivation, and purge until
  operator decision.
- Escalate to JVS owner when doctor output conflicts with direct status.

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

## WebDAV Runtime Request Recovery

Symptoms:

- export runtime accounting shows non-terminal runtime request ledger rows after a
  gateway crash or restart.
- lifecycle drain or revoke remains blocked because one or more non-terminal runtime
  request rows have not closed.
- gateway logs, process state, or heartbeat evidence cannot prove a non-terminal
  runtime request is still active.

Actions:

- Keep the export session non-terminal and keep lifecycle drain blocked until
  no-access evidence is recorded.
- Inspect the export session, non-terminal `export_runtime_requests` rows, heartbeat
  expiry, revoke state, gateway process identity, and redacted gateway logs.
- Prefer the normal stale non-terminal runtime request recovery path. The worker
  recovery closes expired non-terminal ledger rows and subtracts their counts only when
  aggregate counts can cover the recovered rows.
- Terminal reconcile must not proceed while any non-terminal runtime request row remains
  for the export.
- Do not manually set aggregate counts to zero and do not write negative runtime
  deltas. If ledger/aggregate drift exists or the stale row cannot be proven
  inactive, leave the export blocking and mark the owning operation or incident
  as `operator_intervention_required`.
- Record operator, reason, correlation ID, export ID, repo ID, namespace ID,
  stale runtime request IDs, process evidence, and audit event IDs for any
  intervention decision.

Terminal evidence:

- stale runtime request ledger rows recovered followed by terminal export
  reconcile, or intervention record with owner and lifecycle drain still
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
  delivery summary, and sink-side receipt evidence, then create an incident or
  operator intervention item.
- Alert if denied/security events remain delayed after replay attempts.

Terminal evidence:

- lag cleared with sink receipts keyed by `audit_event_id`, or an incident is
  opened with retained outbox and worker summary evidence.
