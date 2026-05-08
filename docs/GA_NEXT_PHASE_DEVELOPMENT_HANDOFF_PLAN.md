# AFSCP GA Next-Phase Development Handoff

Status: PO-first authoritative development handoff for direct GA convergence.

This document is the next development contract for AFSCP GA. It folds the
product, architecture, and QA read-only reviews into executable work. It is not
a new release gate, not a phase-gated roadmap, and not a request to wait for a
sibling or business project.

Primary source: `docs/research/afscp-product-architecture-review.md`.

When this document conflicts with older planning wording, this document owns the
next development contract. PRs should update the touched code, contracts,
schemas, runbooks, tests, and evidence entries together.

## PO Contract

AFSCP is an independent shared file-system control plane. GA means this repo can
ship a product-neutral control plane with a default, automatically proven user
loop. It does not mean a business product, sibling repo, connector UI,
orchestrator implementation, or production deployment has passed acceptance.

The default GA product promise is:

- Operator/admin can complete Day-0 bootstrap for namespace-bound managed
  storage.
- Trusted caller can complete repo create/get/projection/list.
- Trusted caller can complete pinned JVS save/history/restore-preview/
  restore-run/discard.
- Trusted caller can complete WebDAV export/gateway/revoke.
- Caller/operator can trace operation, audit, and recovery state.
- Retained lifecycle archive/restore_archived/delete-tombstone/
  restore_tombstoned is default GA positive storage-state behavior.

The default GA negative promise is equally required:

- Workload mount, template/clone, purge/break-glass, and real deployment runtime
  positives are not default GA.
- In the default profile they must be disabled, denied, recovered, or failed
  closed.
- Unsupported or disabled mutations must not create permanent `queued`
  operations.
- Historical operations must be visible to recovery even after a capability is
  disabled or configured false.

Release acceptance is repo-local, automated, and traceable. No manual approval,
meeting, owner sign-off, sibling repo, business project, or production
deployment state can become a GA blocker or substitute.

## Authoritative Handoff Contract

| Lane | Default GA contract | Required evidence owner | Forbidden drift |
| --- | --- | --- | --- |
| Default positive | Day-0 bootstrap; trusted caller repo create/get/projection/list; pinned JVS save/history/restore-preview/restore-run/discard; WebDAV export/gateway/revoke; retained lifecycle archive/restore_archived/delete-tombstone/restore_tombstoned. | P1 owns bootstrap/caller/WebDAV/JVS; P4 owns retained lifecycle and restore reconciliation; P5 owns final release evidence wiring. | Do not shrink default GA to doc-only examples. Do not move WebDAV, JVS, or retained lifecycle out of default positive. |
| Default safety/ops | Capability matrix, API admission, worker execution/recovery, readyz/discovery, operator inspection, stable errors, operation/audit/recovery terminalization. | P2 owns matrix and terminalization; P3 owns shared operator repair contract/test suite/audit schema. | Do not let readyz replace actor-specific contracts. Do not use ad hoc SQL or manual review as the safety mechanism. |
| Default negatives | Workload mount, template/clone, purge/break-glass, and real deployment runtime are disabled/denied/recovery/fail-closed by default. | P2 owns default negative admission/recovery evidence; P4 owns purge approval fixture-positive and default denial evidence. | Do not skip default negative evidence because optional positives are unselected. |
| Optional fixture positives | Optional positive capability evidence can block final only when selected by the final selector. | P4 owns repo-local fixture positive evidence; P0/P5 own selector semantics and final blocking. | Do not infer optional positive final-required from manifest shape alone. Do not use deployment runtime support as optional fixture conformance. |
| Deployment-runtime-support | Runtime support is an envelope: detection, configuration, redaction, runbook, risk acceptance, fixture docs. It is never a required local GA positive gate. | P5 owns final wording and risk envelope evidence. | Do not let real CSI/POSIX/subPath/orchestrator/deployment state become repo-local final proof. |
| Non-goals | No business catalog, connector UI, external orchestrator implementation, template marketplace, manual release approval, sibling gate, or production deployment gate. | All packages preserve this boundary; P5 doc-sync removes stale wording. | Do not introduce business project names or make caller product lifecycle an AFSCP gate. |

## Canonical Optional Rule

| Rule | Default final behavior | Selected optional behavior | Manifest/evidence minimum |
| --- | --- | --- | --- |
| Optional positive is non-default | Does not block default GA final. | Blocks final only when the authoritative final selector claims the capability. | Capability must be in `claimed_optional_capabilities`. |
| Optional disabled negative is default required | Always blocks final until disabled/denied/recovery/fail-closed evidence passes. | Still required even when optional positive is selected. | `evidence_profile=default`, `default_mode=true`, negative or both polarity, non-placeholder evidence. |
| Selected optional positive requires exact shape | Ignored unless selected. | Required replacement evidence must match exact capability/subclaim/acceptance and be non-placeholder. | `evidence_profile=repo-local-fixture-enabled`, `fixture_enabled_mode=true`, `default_mode=false`, `optional_gated=true`, `required=true`, non-placeholder, selected by `claimed_optional_capabilities`. |
| Deployment-runtime-support is separate | Never a default positive gate. | Never becomes selected optional fixture conformance. | Runtime envelope only; no local positive final requirement. |
| Seed gap is not a hidden requirement expansion | Seed/convergence may show open optional positive gaps. | Final rejects open required gaps; optional positive gaps reject only when selected. | `seed_gap_policy=reject_open_seed_gap` in final selector. |

## Claim, Acceptance, And Evidence Taxonomy

The manifest, selector, tests, and generated report must keep these claims
compact and stable. Acceptance IDs below are canonical names for next work; exact
test names may differ.

| Claim | Default/optional | Acceptance coverage | Evidence owner |
| --- | --- | --- | --- |
| `CLAIM_ADMIN_BOOTSTRAP_READY` | Default positive prerequisite | Volume register/health/preflight; namespace binding; caller/operator role readiness; path redaction. | P1 |
| `CLAIM_DEFAULT_USER_LOOP` | Default positive | Repo create/get/projection/list; pinned JVS save/history/restore-preview/restore-run/discard; WebDAV export/gateway/revoke; operation/audit/recovery trace. | P1 |
| `CLAIM_RETAINED_LIFECYCLE_DEFAULT` | Default positive | Archive, restore_archived, delete-to-tombstone, restore_tombstoned; admission; session/fence predicate; worker recovery; stable errors; audit. | P4 |
| `CLAIM_DEFAULT_DENIAL_SAFE` | Default negative | Unauthorized namespace, policy deny, revoked/expired WebDAV, path escape, secret/path redaction, no permanent queued operation. | P1/P2 |
| `CLAIM_OPTIONAL_DENIED_SAFE` | Default negative | Workload mount, template/clone, purge/break-glass, and runtime positives deny/fail closed when not enabled. | P2/P4 |
| `CLAIM_CAPABILITY_MATRIX_CONSISTENT` | Default safety | API, worker, recovery, readyz, discovery, operator inspection, evidence classification use one matrix. | P2 |
| `CLAIM_OPERATION_TERMINALIZATION` | Default safety | Operation inventory; side-effect boundary; failed vs intervention decisions; idempotent replay; historical recovery visibility. | P2 |
| `CLAIM_DISCOVERY_SURFACES` | Default safety | Caller, orchestrator, operator, and readyz discovery are layered and do not overexpose optional/runtime state. | P2/P3 |
| `CLAIM_OPERATOR_REPAIR_SAFE` | Default safety | One shared repair contract/test suite/audit schema; API or CLI entry; reason/evidence/before-after/safety predicate. | P3 |
| `CLAIM_OPTIONAL_FIXTURE_CONFORMANT` | Selected optional positive | Repo-local fixture positive for selected optional capabilities only. | P4 |
| `CLAIM_PURGE_APPROVAL_SAFE` | Selected optional positive plus default negative | Default denial even with approval-like input; fixture approval object; expiry/scope/policy/hash/replay negatives; audit hash binding. | P4 |
| `CLAIM_RESTORE_RECONCILIATION` | Default safety | Backup/restore reconciliation; dangerous writes denied; no credential reissue; no purged resurrection; mismatch to intervention. | P4 |
| `CLAIM_RELEASE_GATE_TRACEABLE` | Release safety | Single release script; selector/digest/artifact identity; generated reports; seed vs final semantics. | P0/P5 |
| `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | Runtime support only | Runtime configuration, detection, redaction, rollback/roll-forward, runbook, residual-risk acceptance. Never required local positive. | P5 |

## Gate Mode Contract

The only release entrypoint is:

```bash
bash scripts/verify-ga-release.sh
```

Developers and release operators must not manually run `-mode final` and claim
GA. The script owns seed vs final selection.

Authoritative selector path:

```text
docs/release-evidence/ga-release-selector.json
```

| Condition | Gate mode | Required behavior | Hard fail |
| --- | --- | --- | --- |
| Ordinary seed/convergence context, no authoritative selector | Seed/convergence | Run baseline/seed checks; report gaps; allow placeholder seed gaps. | Output or docs claim final GA. |
| Release/final-candidate context but selector missing | No final | Hard fail; do not downgrade to seed pass. | Final candidate passes without selector. |
| Selector exists and `release_intent=final_candidate` | Final | Same script invokes final verifier and consumes selector/manifest/digest inputs. | Open required gaps, placeholder required evidence, digest mismatch, invalid selector. |
| Selector exists but is not final candidate | Seed/convergence only | May inform convergence status; cannot trigger final. | Non-final selector accepted as final. |
| Selector path or digest abnormal | No final | Reject absolute paths, `..`, non-authoritative paths, generated selector pretending to be same-run input, digest mismatch. | Any abnormal selector still passes final. |

## Final Selector, Digest, And Artifact Identity

The final selector is an input artifact. Generated reports are outputs and must
not become same-run authoritative inputs.

Final selector minimum fields:

| Field | Requirement |
| --- | --- |
| `schema_version` | Stable selector schema version. |
| `release_intent` | Must be `final_candidate` for final mode. |
| `manifest_path` | Must point to the current manifest. |
| `final_acceptance_selector` | Claim/subclaim/acceptance rows selected for final acceptance. |
| `claimed_optional_capabilities` | Optional capabilities selected for fixture-positive final blocking. |
| `seed_gap_policy` | Must be `reject_open_seed_gap` for final candidates. |
| manifest digest | Digest of authoritative manifest input. |
| selector input digest | Digest of authoritative selector input after removing `selector_input_digest`, canonicalizing JSON, and hashing that non-self-referential form. |
| schema/migration set digest | Digest of schema, OpenAPI, migrations, and generated-client relevant inputs. |
| policy/artifact identity digest | Digest or identity set for policy, release artifact, JVS/runtime support records as applicable. |
| rollback/roll-forward policy ref | Stable reference to the release rollback/roll-forward policy. |

Generated output minimum:

| Output | Rule |
| --- | --- |
| Generated report digest | Output only. It cannot be a same-run input. |
| Generated selector copy | Copy for audit only. It cannot override the authoritative selector. |
| Generated coverage report | JSON plus Markdown. Must map claims to evidence IDs and statuses. |
| Evidence artifact digest | Stable digest per evidence ID. |

## Generated Evidence Artifact Layout

Generated artifacts should live under:

```text
docs/release-evidence/generated/
```

Minimum layout:

```text
docs/release-evidence/generated/
  coverage-report.json
  coverage-report.md
  final-selector.generated.json
  final-report.generated.json
  evidence/
    <evidence_id>/
      command.json
      stdout.txt
      stderr.txt
      metadata.json
      redaction.json
      digest.json
```

Artifact requirements:

| Artifact | Requirement |
| --- | --- |
| `command.json` | Command, cwd, env allowlist, timeout, exit code, start/end time. |
| `stdout.txt` / `stderr.txt` | Captured output with stable ordering and redaction. |
| `metadata.json` | Evidence ID, claim/subclaim/acceptance, evidence type, profile, mode, status, repo revision. |
| `redaction.json` | Redaction status and proof no raw secret/path material is emitted. |
| `digest.json` | Stable digests for command, metadata, stdout, stderr, and combined artifact. |
| Reports | Deterministic ordering by claim, subclaim, acceptance, evidence ID. |

## Actor Boundary

| Actor | Owns | Must not own for AFSCP GA |
| --- | --- | --- |
| AFSCP | Namespace/volume binding, repo storage state, pinned JVS save/restore, WebDAV export gateway, retained lifecycle, operation/audit/recovery, capability/admission/worker/readyz/evidence consistency. | Business catalog, product lifecycle UX, connector UI, real orchestrator implementation, deployment permission state. |
| Trusted caller | Caller identity, namespace authorization, AFSCP API calls, relaying first-create WebDAV credentials to a connector. | Issuing WebDAV passwords, seeing raw root paths or SecretRefs, bypassing namespace policy. |
| Client connector | Receiving short-lived WebDAV credentials from the trusted caller and accessing the AFSCP gateway. | Calling AFSCP admin/caller APIs directly, reading `.jvs`, replaying raw passwords, managing storage credentials. |
| Orchestrator | Consuming workload plans only when an orchestrator capability is explicitly enabled. | Being a default GA dependency or exposing mount plans to ordinary callers. |
| Operator/admin | Bootstrap, preflight, inspection, intervention queue review, one allowlisted repair entry, audit review. | Manual GA approval, arbitrary SQL repair, arbitrary state rewrite. |
| Deployment/runtime | Providing Postgres, managed volume, JVS binary, WebDAV runtime, audit sink, CI, and optional orchestrator runtime. | Serving as this repo's release gate or replacing repo-local evidence. |

## Product Decisions

| Decision area | Package/owner | Decision |
| --- | --- | --- |
| Operator repair entry | P3 | GA requires one shared repair contract, one test suite, and one audit schema. The entry can be API or CLI. GA does not require both. Ad hoc SQL and arbitrary state rewrite are forbidden. |
| Purge release-note posture | P4/P5 | Purge and break-glass purge are optional, irreversible, capability-gated, and not default GA. Default release notes say purge is denied/fail-closed unless explicitly enabled with structured approval evidence. |
| Purge approval reference | P4 | Approval is a controlled evidence object or verifiable reference with subject, policy version, scope, expiry, reason, hash/correlation, audit binding, and anti-replay semantics. |
| Template naming/mental model | P4/P5 | For GA, position template as same-namespace/same-volume clone primitive unless controlled admin import/publish is explicitly designed. Do not imply marketplace or cross-namespace reusable templates by default. |
| Quota fields | P1/P5 | Do not imply hard quota enforcement from `quota_bytes_default`. Add or align machine-readable status such as `quota_enforcement_status`, `effective_quota_bytes`, or `enforced=false`, or rename toward policy wording. |
| Restore session drain | P4 | Fixed decision: `restore_archived` and `restore_tombstoned` restore access. Default GA must prove no active or uncertain session/fence; otherwise fail closed or enter `operator_intervention_required`. Contract, implementation, errors, tests, runbook, and evidence must agree. |
| Product-neutral conformance | P1/P2/P4 | Minimum scope is caller credential relay semantics, connector WebDAV access/revoke semantics, orchestrator denied/default-disabled semantics, operation inspection, and negative authorization cases. It is repo-local and product-neutral, not a sibling gate. |
| Capability discovery layering | P2/P3 | Caller, orchestrator, operator, and readyz surfaces expose different decisions from the same matrix. Readyz is not the only contract. |
| Shared-volume residual risk | P4/P5 | Define namespace isolation assumptions, volume admin misconfiguration risk, backup/restore mismatch, POSIX/CSI drift, detection metrics, compensating controls, and when dedicated volume is required. |

## User Journeys

| Journey | Default/optional | Acceptance |
| --- | --- | --- |
| Day-0 bootstrap | Default prerequisite | Volume health/preflight, namespace binding, caller/operator role readiness, path redaction, machine-checkable bootstrap evidence. |
| Trusted caller default loop | Default positive | Repo create/get/projection/list; JVS save/history/restore-preview/restore-run/discard; WebDAV export/gateway/revoke; operation/audit/recovery trace. |
| Retained lifecycle | Default positive | Archive, restore_archived, delete-tombstone, restore_tombstoned with admission, session/fence predicate, worker recovery, stable errors, audit, schema/OpenAPI, runbook, manifest evidence. |
| Default failure loop | Default negative | Unauthorized, policy denied, capability disabled, stale, revoked, expired, unsupported, and redaction paths fail closed and audit. |
| Workload teardown-only | Default safety for optional capability | Only scoped orchestrator/operator reader can see teardown-only plan; no mount material; audit emitted; stale closure depends on P3 repair. |
| Optional fixture positive | Selected optional | Fixture evidence only after selector claims capability. |

## Capability Matrix V1 Contract

One matrix must drive API admission, worker execution, worker recovery, readyz,
actor discovery, operator inspection, and evidence classification.

Minimum fields:

| Field | Meaning |
| --- | --- |
| `surface_type` | API, worker, recovery, readyz, caller-discovery, orchestrator-discovery, operator-inspection, evidence. |
| `operation_type` | Stable operation inventory key. |
| `capability_id` | Capability controlled by the row. |
| `resource_scope` | Namespace, repo, volume, operation, export, lifecycle, restore, or runtime scope. |
| `supported` | Code supports the operation type. |
| `configured` | Runtime configuration is present. |
| `ready` | Runtime is healthy enough to execute. |
| `required_for_default_ga` | Default GA positive or default negative required evidence. |
| `required_for_service_ready` | Service readiness dependency. |
| `optional_gated` | Positive behavior is optional and selector-controlled. |
| `namespace_policy` | Policy predicate or policy class required. |
| `volume_runtime_capability` | Volume/runtime prerequisite. |
| `denial_code` | Stable denial code when unavailable. |
| `runbook_ref` | Stable runbook reference for denial/intervention. |
| `evidence_ref` | Manifest evidence ID or claim reference. |

One row must answer one surface decision. Do not hide multiple actor decisions in
one row. If caller API, worker recovery, and operator inspection make different
decisions for the same capability, they need separate `surface_type` rows with
the same `capability_id` and explicit evidence.

Surface rules:

| Surface | Rule |
| --- | --- |
| API admission | Deny unsupported or disabled new mutations before queuing, unless the operation can be safely terminalized. |
| Worker execution | Register executors only for supported/configured/ready operation types. |
| Worker recovery | Historical operations remain queryable even if `configured=false` or capability disabled. |
| Readyz | Summarizes service readiness; does not replace actor-specific denial contracts. |
| Caller discovery | Shows default usable capability and stable denial state, not optional mount material. |
| Orchestrator discovery | Shows mount/teardown state only to authorized scoped readers. |
| Operator inspection | Shows matrix state, intervention, held fence/session, stale lease, recovery state, and audit lag. |
| Evidence | Maps claim/subclaim/acceptance to exact matrix rows. |

## Operation Terminalization Contract

P2 owns the operation terminalization contract. It is a default GA safety claim.

Required rules:

- Maintain operation_type inventory for repo create, save, restore-preview,
  restore-run, discard, WebDAV export/revoke, retained lifecycle, workload
  mount, template/clone, purge, repair, and recovery-only terminalization.
- Define side-effect boundary for each operation type: before side effect, after
  durable side effect, uncertain side effect, and replay-safe side effect.
- Prefer idempotent replay before capability denial when an operation already
  has durable side-effect evidence.
- New disabled/unsupported operations fail before queueing when no safe
  terminalization path exists.
- Historical operations do not disappear from recovery queries because
  capability is disabled or `configured=false`.
- Use `failed` when no side effect happened or replay can prove safe failure.
- Use `operator_intervention_required` when side effect is uncertain, fence or
  session state is uncertain, storage/control-plane state mismatches, or repair
  proof is needed.
- Emit stable errors, runbook references, and audit for denial,
  terminalization, and intervention.

## Workload Teardown-Only Plan Contract

Default GA does not require workload mount positive behavior. It does require a
safe teardown-only shape for stale/cleanup paths.

Minimum shape:

- Visible only to orchestrator/operator scoped readers.
- Contains only fields required for release, revoke, and terminal evidence.
- Does not contain SecretRef, raw path, payload subdir, credential, or material
  from which mount access can be derived.
- Emits audit on read and terminal evidence write.
- Denies ordinary caller visibility.
- Uses stable denial codes and runbook refs when unavailable.
- Stale closure depends on the P3 shared operator repair contract; do not invent
  a second workload-specific repair path.

## Purge Approval Acceptance

Purge is optional, irreversible, and selected optional fixture positive only.

Default profile:

- Deny purge even if input looks like an approval.
- Deny break-glass purge unless capability selected and fixture approval object
  verifies.
- Audit denial without treating the approval-like input as valid.

Fixture-positive profile:

- Blocks final only when `repo_purge` is selected in
  `claimed_optional_capabilities`.
- Approval evidence includes expiry, scope, policy version, subject, reason,
  hash/correlation, and anti-replay marker.
- Negative tests cover expired approval, wrong scope, wrong policy, hash
  mismatch, replay, missing reason, unauthorized subject, retention conflict,
  and audit hash binding.
- Purged repo must not be resurrected by restore/reconciliation.

## Backup And Restore Consistency

P4 owns default restore reconciliation evidence and P5 owns release/runtime
wording.

Acceptance:

- Reconciliation mode is explicit after backup/restore.
- Dangerous writes are denied until metadata/storage consistency is known.
- No WebDAV credential is automatically reissued after restore.
- Purged repos are not resurrected.
- Metadata/storage mismatch enters `operator_intervention_required`.
- Storage generation, snapshot timestamp, tombstone/purge marker, and audit
  state are part of the reconciliation evidence.
- Runbook explains safe recovery and escalation.

## Architecture Convergence

Implementation should converge around these shared contracts:

| Contract | Owner | Closure condition |
| --- | --- | --- |
| Capability matrix | P2 | API, worker, recovery, readyz, discovery, operator inspection, and evidence agree. |
| Operation terminalization | P2 | New disabled work fails before queue; historical unsupported work terminalizes or intervenes. |
| Access/session/fence predicates | P1/P4 | WebDAV, restore, lifecycle, workload cleanup, and retained lifecycle share stable predicate semantics. |
| Operator repair | P3 | One shared repair contract/test suite/audit schema; API or CLI entry; no arbitrary SQL. |
| Retained lifecycle | P4 | Default positive archive/restore_archived/delete-tombstone/restore_tombstoned evidence covers admission, predicate, worker, errors, audit, schema/OpenAPI, runbook, manifest. |
| Release hardening | P5 | Single script, selector, digests, generated artifacts, workflow hardening, rollback/roll-forward, doc-sync. |

## Work Packages

These packages are ownership slices, not stage GA gates. However, their semantic
dependencies matter:

```text
P0 -> capability/terminalization -> access/session -> operator repair
   -> irreversible lifecycle/restore -> release hardening
```

Do not land work that depends on an unclosed earlier semantic contract unless the
PR includes the missing contract seed and evidence.

### P0: Evidence, Selector, And Manifest Contract

Owner: release/evidence.

Work:

- Final selector parser and authoritative selector path.
- `evidence_status`: `placeholder`, `implemented`, `closed`.
- Required placeholder rejection.
- Seed gap placeholder semantics.
- Optional-positive final blocking selected only by selector.
- Unknown field rejection for generated report/digest inputs.
- Seed/final gate mode contract tests.

Expected red tests:

```bash
go test -count=1 ./internal/releaseevidence -run 'Test.*Selector|Test.*EvidenceStatus|Test.*Final'
go test -count=1 ./cmd/afscp-evidence-verify -run 'Test.*Selector|Test.*Final|Test.*Mode'
go test -count=1 ./internal/contractcheck -run 'Test.*GA|Test.*Release|Test.*Evidence'
```

### P1: Bootstrap, Default Caller Loop, And Access Predicates

Owner: API/store/WebDAV/JVS.

Work:

- Day-0 bootstrap.
- Repo create/get/projection/list.
- Pinned JVS save/history/restore-preview/restore-run/discard.
- WebDAV export/gateway/revoke.
- Shared access/session/fence predicate seed for restore/lifecycle.
- Quota machine-readable status or conservative naming.
- Product-neutral conformance for caller credential relay and connector
  WebDAV access/revoke.

Expected red tests:

```bash
go test -count=1 ./internal/api ./internal/store/postgres ./internal/exportgateway ./internal/exportaccess ./internal/exportreconcile -run 'Test.*DefaultUserLoop|Test.*Bootstrap|Test.*WebDAV.*Revoke|Test.*Quota'
go test -count=1 ./internal/contractcheck -run 'Test.*OpenAPI|Test.*Schema|Test.*Readiness'
```

### P2: Capability Matrix And Operation Terminalization

Owner: capability/API/worker/recovery.

Work:

- Capability matrix v1 fields and surface rows.
- API admission and worker capability parity.
- Worker recovery sees historical operations after capability/config changes.
- Stable denial codes and runbook refs.
- Operation_type inventory.
- Side-effect boundary rules.
- Default negatives for workload mount, template/clone, purge, deployment
  runtime.
- Discovery surfaces split by actor.

Expected red tests:

```bash
go test -count=1 ./internal/capability ./internal/api ./internal/workerapp -run 'Test.*Capability.*Matrix|Test.*Admission.*Disabled|Test.*Recovery.*Unsupported|Test.*Terminal'
go test -count=1 ./internal/contractcheck -run 'Test.*Capability|Test.*Discovery'
```

### P3: Operator Inspection And Shared Repair

Owner: operator/admin surface.

Work:

- Inspection for correlated operation lookup, intervention queue, held
  fence/session, stale lease, recovery state, and audit lag.
- One shared repair contract/test suite/audit schema.
- One entry implementation: API or CLI. GA does not require both.
- Reason, evidence reference, affected IDs, before/after state, safety
  predicate, audit event.
- Workload teardown-only stale closure uses this repair contract.

Expected red tests:

```bash
go test -count=1 ./internal/api ./internal/workerapp ./internal/store/postgres -run 'Test.*Operator.*Inspection|Test.*Repair|Test.*Intervention|Test.*Audit'
go test -count=1 ./internal/contractcheck -run 'Test.*Runbook|Test.*Repair'
```

### P4: Retained Lifecycle, Restore Reconciliation, And Optional Fixture Positives

Owner: lifecycle/restore/optional capabilities.

Work:

- Default retained lifecycle positive evidence:
  archive/restore_archived/delete-tombstone/restore_tombstoned.
- Admission, session/fence predicate, worker recovery, stable errors, audit,
  schema/OpenAPI, runbook, and manifest evidence for retained lifecycle.
- Fixed restore drain decision for restore_archived/restore_tombstoned.
- Backup/restore reconciliation evidence.
- Workload mount fixture positive only when selected.
- Template/clone fixture positive or clone-primitive naming alignment.
- Purge approval fixture positive and default denial evidence.

Expected red tests:

```bash
go test -count=1 ./internal/api ./internal/repoexec ./internal/store/postgres ./internal/workerapp -run 'Test.*RetainedLifecycle|Test.*Restore.*Drain|Test.*Restore.*Reconciliation|Test.*Purge.*Approval|Test.*Template'
go test -count=1 ./internal/releaseevidence -run 'Test.*Optional|Test.*Final'
```

### P5: Release Hardening, Runtime Envelope, And Doc Sync

Owner: release/docs/workflow.

Work:

- Final selector/digest/artifact identity.
- Generated report and evidence artifact layout.
- Workflow hardening: permissions, artifact retention, final-candidate
  trigger/context, no manual final bypass, branch/tag identity.
- Rollback/roll-forward policy reference and evidence.
- Runtime/flake/retry policy: deterministic retries only where safe, no
  evidence masking, explicit timeout and flake classification.
- High-risk release evidence: JVS provenance/smoke, Postgres integration,
  WebDAV gateway plus ledger e2e, generated-client compile, and precise
  race/concurrency gate.
- Deployment-runtime-support envelope for real runtime prerequisites.
- Doc-sync targets:
  - `cmd/README.md`
  - `docs/PRODUCT_REQUIREMENTS.md`
  - `docs/READINESS_EVIDENCE.md` `auto_verified`, seed-vs-final, and profile
    wording
  - `docs/ARCHITECTURE.md`
  - `docs/API_CONTRACT_DRAFT.md`
  - `docs/contracts/`
  - `docs/runbooks/`
  - `scripts/README.md`

Expected red tests:

```bash
go test -count=1 ./internal/contractcheck -run 'Test.*GA|Test.*Release|Test.*Workflow|Test.*Readiness|Test.*Docs'
go test -count=1 ./internal/releaseevidence ./cmd/afscp-evidence-verify
bash scripts/verify-ga-release.sh
```

## TDD Rules

Every PR starts with a failing test, schema assertion, contract guard, doc guard,
or manifest evidence expectation that names the claim being closed.

Required PR shape:

1. Add failing evidence/test/guard.
2. Implement the smallest code, schema, doc, or manifest change.
3. Update touched contract/schema/OpenAPI/runbook/evidence entries.
4. Run package-level targeted tests.
5. Run the relevant release gate subset.

Do not:

- Turn optional positive capabilities into default GA.
- Add sibling repo checks.
- Use manual approval as a release gate.
- Use deployment-runtime-support as local positive final proof.
- Let placeholder evidence satisfy required final acceptance.
- Leave high-risk claims as doc-only evidence.

## Evidence And Gate Policy

Evidence status:

| Status | Meaning |
| --- | --- |
| `placeholder` | Static manifest marker for an open gap. Never a passing final result. |
| `implemented` | Static manifest says repo-local evidence exists and should run or be checked. |
| `closed` | Static manifest says the gap is closed by evidence. It does not mean the current command run passed. |

Minimum evidence by area:

| Area | Minimum evidence |
| --- | --- |
| Default caller loop | Positive repo-local tests covering bootstrap, repo, JVS, WebDAV, operation/audit/recovery. |
| Retained lifecycle | Admission, session/fence predicate, worker recovery, stable errors, audit, schema/OpenAPI, runbook, manifest evidence. |
| Default negatives | Denied/disabled/recovery/fail-closed tests and no permanent queued operations. |
| Capability matrix | Matrix row tests across API, worker, recovery, readyz, discovery, operator inspection, evidence. |
| Operation terminalization | Operation inventory, side-effect boundary, failed vs intervention, idempotent replay, historical visibility. |
| Operator repair | One shared contract/test suite/audit schema, API or CLI entry, no arbitrary SQL. |
| Optional positives | Fixture-enabled evidence plus selector-selected final blocking. |
| Runtime support | Envelope only: detection, config, redaction, runbook, risk acceptance. |
| Release | Selector, digests, artifact identity, generated reports, workflow hardening, rollback/roll-forward. |

## Handoff Definition Of Done

The handoff is complete when:

- PO contract is reflected in product, architecture, contracts, schema/OpenAPI,
  runbooks, release notes, and evidence manifest.
- Default positive loop has repo-local evidence.
- Retained lifecycle default positive has explicit evidence owner and coverage
  for admission, session/fence predicate, worker recovery, stable errors, audit,
  schema/OpenAPI, runbook, and manifest evidence.
- Workload mount, template/clone, purge/break-glass, and real deployment runtime
  are capability-gated by default and have denied/recovery/fail-closed evidence.
- Optional positives only block final when selected by the final selector and
  exact manifest fields match.
- Deployment-runtime-support is never a required local GA positive gate.
- Restore_archived/restore_tombstoned drain behavior is fixed and proven.
- Operator repair has one shared contract/test suite/audit schema and one entry
  implementation, API or CLI.
- Capability matrix v1 drives all surfaces.
- Operation terminalization rules are implemented and tested.
- Generated report/evidence artifacts are deterministic and digestable.
- `bash scripts/verify-ga-release.sh` is the only release entrypoint and cannot
  pass final with open required gaps.
- No sibling project, business project, manual sign-off, or production
  deployment state is a GA gate.

## Reviewer Checklist

Reviewers should ask:

- Which claim from the taxonomy does this PR close?
- Does evidence strength match the claim wording?
- Did retained lifecycle coverage include admission, predicate, worker recovery,
  stable errors, audit, schema/OpenAPI, runbook, and manifest evidence?
- Did optional positive behavior remain selector-selected and non-default?
- Did deployment-runtime-support stay out of local positive final gates?
- Did restore drain behavior follow the fixed decision?
- Did operator repair use the shared contract/test suite/audit schema?
- Did the PR update touched docs, contracts, schemas, runbooks, and evidence?
- Did targeted package tests and the relevant gate subset run?
