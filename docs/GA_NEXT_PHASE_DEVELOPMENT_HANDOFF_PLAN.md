# AFSCP GA Next-Phase Development Handoff

Status: PO-first authoritative development handoff for direct GA convergence.

This document is the current and only development execution handoff for AFSCP
GA. It folds the product, architecture, and QA read-only reviews into executable
work. It is not a new release gate, not a phase-gated roadmap, and not a request
to wait for a sibling or business project.

Primary source: `docs/research/afscp-product-architecture-review.md`.

When this document conflicts with older planning wording, this document owns the
next development contract. PRs should update the touched code, contracts,
schemas, runbooks, tests, and evidence entries together.

`docs/GA_CONVERGENCE_WORK_PLAN.md` is background and a
research-to-workstream map. It is not a parallel execution entrypoint. Developers
should use this handoff plus the current manifest gaps to choose slices.

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
- Trusted caller can trace caller-scoped operation/audit/recovery state for its
  namespace and operations.
- Operator/admin can inspect global audit/intervention/fence/session/stale
  lease/audit lag state.
- Retained lifecycle archive/restore_archived/delete-tombstone/
  restore_tombstoned is default GA positive storage-state behavior.

The default user loop is complete only when exact, non-placeholder evidence
exists for repo projection, JVS save/restore, WebDAV default access,
caller-scoped operation/audit/recovery trace, and the P2b runtime parity those
paths depend on. P1b/P1c/P1d partial evidence is necessary, but it is not the
final caller-loop claim.

Default User Loop and other default GA claims are related but not identical:

- P1 Default User Loop is the caller main loop: trusted caller repo
  create/get/projection/list, pinned JVS save/history/restore-preview/
  restore-run/discard, WebDAV export/gateway/revoke, and operation/audit/
  recovery trace for that loop.
- Admin bootstrap is a prerequisite to the caller loop.
- Retained lifecycle, operator repair, capability/terminalization safety, and
  release hardening are default GA claims, but they are not the same P1 caller
  loop and must keep separate evidence IDs, owners, and closure criteria.

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
| Seed gap is not a hidden requirement expansion | Seed/convergence may show open optional positive gaps. | Final rejects open required/default gaps; optional positive gaps reject only when selected. | `seed_gap_policy=reject_open_seed_gap` in final selector. |

## Claim, Acceptance, And Evidence Taxonomy

The manifest, selector, tests, and generated report must keep these claims
compact and stable. Acceptance IDs below are canonical names for next work; exact
test names may differ.

| Claim | Default/optional | Acceptance coverage | Evidence owner |
| --- | --- | --- | --- |
| `CLAIM_PROFILE_BOUNDARY` | Default release/profile guard | Default, repo-local-fixture-enabled, and deployment-runtime-support profiles cannot be mixed or promoted across boundaries. | P0/P5 |
| `CLAIM_ADMIN_BOOTSTRAP_READY` | Default positive prerequisite | Volume register/health/preflight; namespace binding; caller/operator role readiness; path redaction. | P1 |
| `CLAIM_DEFAULT_USER_LOOP` | Default positive | Repo create/get/projection/list; pinned JVS save/history/restore-preview/restore-run/discard; WebDAV export/gateway/revoke; operation/audit/recovery trace. | P1 |
| `CLAIM_WEBDAV_DEFAULT_ACCESS` | Default positive subclaim | WebDAV first-create credential, gateway access, expiry/revoke, replay redaction, and ledger/audit behavior. | P1d/P5 |
| `CLAIM_RETAINED_LIFECYCLE_DEFAULT` | Default positive | Archive, restore_archived, delete-to-tombstone, restore_tombstoned; admission; session/fence predicate; worker recovery; stable errors; audit. | P4 |
| `CLAIM_DEFAULT_DENIAL_SAFE` | Default negative | Unauthorized namespace, policy deny, revoked/expired WebDAV, path escape, secret/path redaction, no permanent queued operation. | P1/P2 |
| `CLAIM_SECRET_PATH_REDACTION` | Default safety subclaim | Raw root paths, metadata URLs, SecretRefs, host paths, storage credentials, `.jvs`, and replayable raw secrets are redacted from caller/runtime/audit/evidence surfaces. | P1/P2/P5 |
| `CLAIM_OPTIONAL_DENIED_SAFE` | Default negative | Workload mount, template/clone, purge/break-glass, and runtime positives deny/fail closed when not enabled. | P2/P4 |
| `CLAIM_CAPABILITY_MATRIX_CONSISTENT` | Default safety | API, worker, recovery, readyz, discovery, operator inspection, evidence classification use one matrix; runtime behavior claims require production wiring evidence, not helper-only agreement. | P2 |
| `CLAIM_OPERATION_TERMINALIZATION` | Default safety | Operation inventory; side-effect boundary; failed vs intervention decisions; idempotent replay; historical recovery visibility. | P2 |
| `CLAIM_DISCOVERY_SURFACES` | Default safety | Caller, orchestrator, operator, and readyz surfaces are layered and do not overexpose optional/runtime state; discovery closure requires surface output evidence, not only matrix rows. | P2/P3 |
| `CLAIM_OPERATOR_REPAIR_SAFE` | Default safety | One shared repair contract/test suite/audit schema; API or CLI entry; reason/evidence/before-after/safety predicate. | P3 |
| `CLAIM_OPTIONAL_FIXTURE_CONFORMANT` | Selected optional positive | Repo-local fixture positive for selected optional capabilities only. | P4 |
| `CLAIM_WORKLOAD_FIXTURE_READY` | Selected optional subclaim | Workload fixture plan fetch, heartbeat, release, revoke, and terminal evidence only under `repo-local-fixture-enabled` and selected capability. | P4c |
| `CLAIM_TEMPLATE_QUOTA_BOUNDARY` | Default negative plus selected optional subclaim | Template/clone default denial, same-namespace/same-volume fixture boundary when selected, quota machine-readable status and no hard-enforcement implication. | P1/P4c/P5 |
| `CLAIM_PURGE_APPROVAL_SAFE` | Selected optional positive | Fixture approval object; expiry/scope/policy/hash/replay negatives; audit hash binding. Default purge denial belongs to `CLAIM_OPTIONAL_DENIED_SAFE` and remains always required. | P4 |
| `CLAIM_RESTORE_RECONCILIATION` | Default safety | Backup/restore reconciliation; dangerous writes denied; no credential reissue; no purged resurrection; mismatch to intervention. | P4 |
| `CLAIM_RESIDUAL_RISK_CATALOG` | Default safety/runtime-risk subclaim | Residual-risk acceptance uses predefined risk IDs, scope, expiry/review point, evidence reference, and audit shape; not human GA approval. | P3/P4/P5 |
| `CLAIM_RELEASE_GATE_TRACEABLE` | Release safety | Single release script; selector/digest/artifact identity; generated reports; seed vs final semantics. | P0/P5 |
| `CLAIM_WORKFLOW_HARDENING_GUARD` | Release safety subclaim | Repo-local workflow YAML/script declarations for single gate, permissions, artifact/report publication, and no final bypass. | P5 |
| `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | Runtime support only | Runtime configuration, detection, redaction, rollback/roll-forward, runbook, residual-risk acceptance. Never required local positive. | P5 |

Default-loop evidence must not absorb other default claims. A P1b/P1c/P1d PR can
add partial default-loop evidence, but it must not close
`CLAIM_DEFAULT_USER_LOOP` until all caller-loop subclaims are present and the
P2b runtime parity contract it relies on is satisfied. Retained lifecycle,
operator repair, and release hardening close their own claims.

## Product Architecture Review Follow-up Plan

Research findings are triaged into this handoff by GA acceptance semantics, not
by finding count. A finding becomes a default blocker only when it protects the
product-neutral shared file-system control plane promised above. Optional
positive and deployment/runtime findings stay out of default GA final unless the
selector explicitly claims them or the finding is release/profile/doc-sync
safety.

| Finding class | Handoff treatment | Closure rule |
| --- | --- | --- |
| Default blocker | Default caller loop, capability/admission/worker/recovery parity, operator inspection/repair, restore reconciliation, discovery/readyz layering, secret/path redaction, release/profile/workflow hardening. | Requires exact claim/subclaim/acceptance evidence, non-placeholder replacement, focused command, and `bash scripts/verify-ga-release.sh` where release evidence changes. |
| Selected optional | Workload positive, template positive, purge positive, and other fixture-enabled positives. | Blocks final only when selected by `claimed_optional_capabilities`; default profile still requires denied/fail-closed evidence. |
| Runtime-support/doc-sync | Deployment envelope, runtime prerequisites, residual-risk shape, release wording, generated reports, and runbook/doc alignment. | Repo-local schema/contract/doc/evidence guard only; never a production deployment, manual, or external project gate. |

Do not promote `auto_verified`, research recommendations, broad smoke wording, or
reviewer approval into final GA evidence. Research findings must land as the
smallest exact claim slice with manifest guards and negative shape tests.

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

Generated artifacts MUST live under:

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
| Trusted caller | Caller identity, namespace authorization, AFSCP API calls, relaying first-create WebDAV credentials to a connector, caller-scoped operation/audit trace for its namespace and operations. | Issuing WebDAV passwords, seeing raw root paths or SecretRefs, bypassing namespace policy, global audit/intervention/fence/session/stale lease inspection. |
| Client connector | Receiving short-lived WebDAV credentials from the trusted caller and accessing the AFSCP gateway. | Calling AFSCP admin/caller APIs directly, reading `.jvs`, replaying raw passwords, managing storage credentials. |
| Orchestrator | Consuming workload plans only when an orchestrator capability is explicitly enabled. | Being a default GA dependency or exposing mount plans to ordinary callers. |
| Operator/admin | Bootstrap, preflight, global inspection for audit/intervention/fence/session/stale lease/audit lag, one allowlisted repair entry, audit review. | Manual GA approval, arbitrary SQL repair, arbitrary state rewrite. |
| Deployment/runtime | Providing Postgres, managed volume, JVS binary, WebDAV runtime, audit sink, CI, and optional orchestrator runtime. | Serving as this repo's release gate or replacing repo-local evidence. |

## Actor Mental Model

Keep the product concepts narrow by actor. AFSCP can expose richer internal
state to operator/recovery code, but each external actor should learn only the
concepts needed for its job.

| Actor | Concepts they should understand | Concepts they should not need |
| --- | --- | --- |
| Trusted caller | Namespace, repo, repo projection/list, savepoint/history, restore preview/run/discard, WebDAV export/revoke, caller-scoped operation status, caller-scoped audit/recovery status, stable denial. | Volume root path, SecretRef, host path, metadata URL, control root, `.jvs`, mount plan, fence internals, deployment runtime state, global audit/intervention/fence/session/stale lease state. |
| Client connector | Short-lived WebDAV credential relayed by trusted caller, gateway URL/session expiry, revoked/expired access behavior. | AFSCP caller/admin API, namespace policy, repo lifecycle internals, storage credentials, raw password replay. |
| Operator/admin | Volume preflight, namespace binding policy, capability/readiness matrix, intervention queue, held fence/session, stale lease, audit lag, allowlisted repair, residual-risk record shape. | Business catalog workflow, connector UI, arbitrary SQL/state rewrite, manual GA approval. |
| Orchestrator | Only when optional capability is enabled: scoped mount/teardown state, heartbeat/release/revoke/terminal evidence, denied/default-disabled status. | Default GA positive path, ordinary caller repo projection, raw SecretRef/path/credential, business workload management. |
| Deployment/runtime | Runtime envelope, config detection, redaction policy, rollback/roll-forward policy, CI service availability, fixture/runtime prerequisites. | Repo-local final acceptance authority, branch protection/real artifact/GitHub environment state as local gate proof. |

## Security Disclosure Boundaries

Security disclosure boundaries are part of the product contract, not polish.

| Surface | Disclosure rule |
| --- | --- |
| Caller discovery/API | No SecretRef, raw root path, host path, credential, workload runtime material, raw `.jvs` path, or globally scoped audit/intervention/fence/session/stale lease state. |
| Orchestrator discovery | Only scoped workload state needed for denied/default-disabled, release, revoke, heartbeat, and terminal evidence. No credential, SecretRef, raw path, payload subdir, or derivable mount material. |
| Operator inspection | More global than caller surfaces, but still redacted by default: IDs, hashes, policy refs, runbook refs, and redacted paths over raw secrets/material. |
| Evidence artifacts | Use hashes, IDs, policy refs, redacted paths, stable denial codes, and redaction status. Do not preserve raw credentials, SecretRefs, raw paths, or workload runtime material. |
| Optional positives | Do not appear as default release proof. Selected optional fixture positives live only under `repo-local-fixture-enabled` evidence and selector-selected final blocking. |

## Product Decisions

| Decision area | Package/owner | Decision |
| --- | --- | --- |
| Operator repair entry | P3 | Authoritative contract artifact is `docs/contracts/operator-repair-v1.md`. GA requires one shared repair contract, one test suite, and one audit schema. The entry can be API or CLI. GA does not require both. Ad hoc SQL and arbitrary state rewrite are forbidden. |
| Purge release-note posture | P4/P5 | Purge and break-glass purge are optional, irreversible, capability-gated, and not default GA. Default release notes say purge is denied/fail-closed unless explicitly enabled with structured approval evidence. |
| Purge approval reference | P4 | Approval is a controlled evidence object or verifiable reference with subject, policy version, scope, expiry, reason, hash/correlation, audit binding, and anti-replay semantics. |
| Template naming/mental model | P4/P5 | For GA, position template as same-namespace/same-volume clone primitive unless controlled admin import/publish is explicitly designed. Do not imply marketplace or cross-namespace reusable templates by default. |
| Quota fields | P1/P5 | Do not imply hard quota enforcement from `quota_bytes_default`. Add or align machine-readable status such as `quota_enforcement_status`, `effective_quota_bytes`, or `enforced=false`, or rename toward policy wording. |
| Restore session drain | P4 | Fixed decision: `restore_archived` and `restore_tombstoned` restore access. Default GA must prove no active or uncertain session/fence; otherwise fail closed or enter `operator_intervention_required`. Contract, implementation, errors, tests, runbook, and evidence must agree. |
| WebDAV credential issuer | P1d/P5 | AFSCP issues the short-lived one-time WebDAV credential to the trusted caller. The trusted caller relays it to the connector. The connector does not call AFSCP APIs directly, and the caller does not generate the WebDAV password. |
| Product-neutral conformance | P1/P2/P4 evidence owner: contractcheck plus repo-local smoke/fixture hooks owned by touched API/WebDAV/orchestrator-facing packages. | Minimum scope is caller credential relay, connector WebDAV access/revoke, orchestrator default-denied/discovery, operation inspection negative cases, and negative authorization. It is repo-local fixture/smoke evidence and product-neutral, not a sibling gate. |
| Capability discovery layering | P2/P3 | Caller discovery, orchestrator discovery, operator inspection, readyz, and evidence classification read the matrix but expose different outputs and have separate acceptance. Readyz is not caller authorization; discovery is not readiness; evidence classification is not runtime admission. |
| Shared-volume residual risk | P4/P5 | Define namespace isolation assumptions, volume admin misconfiguration risk, backup/restore mismatch, POSIX/CSI drift, detection metrics, compensating controls, and when dedicated volume is required. Risk acceptance is not human release approval or production state; it is a repo-local schema, fixture, and audit shape that must be verified. |

## Architecture Decision Register

These decisions are the compact handoff contract for high-risk areas. They are
not a separate roadmap; they tell each slice what claim it may close, what must
stay optional, and what evidence strength is required.

| Decision | Claim(s) | Owner | Default/optional | Minimum evidence type | Review guard |
| --- | --- | --- | --- | --- | --- |
| AFSCP remains product-neutral and independently releasable. | `CLAIM_RELEASE_GATE_TRACEABLE`, `CLAIM_PROFILE_BOUNDARY` | P0/P5 | Default release contract | Contractcheck, release verifier, selector/digest tests, docs sync | No sibling project, business workflow, manual approval, or production deployment state as GA gate. |
| Capability matrix is the common source for API admission, worker execution/recovery, readyz, discovery, operator inspection, and evidence classification. | `CLAIM_CAPABILITY_MATRIX_CONSISTENT` | P2 | Default safety | Matrix row tests plus real API/worker runtime parity tests | Helper-only agreement cannot close runtime behavior. |
| Operation terminalization owns side-effect boundaries and `failed` vs `operator_intervention_required`. | `CLAIM_OPERATION_TERMINALIZATION` | P2 | Default safety | Operation contract, store/worker RunOnce recovery tests, audit/stable-error tests | `operator_intervention_required` is a safe runtime state, not human release approval. |
| Operator repair is one allowlisted repair contract, not an operator platform. | `CLAIM_OPERATOR_REPAIR_SAFE` | P3 | Default safety | `docs/contracts/operator-repair-v1.md`, one API or CLI entry, before/after evidence, audit schema | No arbitrary SQL or state rewrite. |
| WebDAV default access is default positive, but P1d must not overclaim full default loop. | `CLAIM_WEBDAV_DEFAULT_ACCESS`, `CLAIM_DEFAULT_USER_LOOP` | P1d/P5 | Default positive plus partial default-loop evidence | Real API/exportaccess/exportgateway/exportreconcile/store tests, credential relay/revoke/ledger e2e, redaction tests | AFSCP issues one-time short-lived credential to trusted caller; caller relays to connector. |
| Workload mount, template/clone, purge positive, hard quota enforcement, and shared-volume runtime specifics are not default positives. | `CLAIM_OPTIONAL_DENIED_SAFE`, selected optional claims, `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | P2/P4/P5 | Default negative or selected optional | Default-denial/recovery tests; selected fixture evidence only when selector claims capability; runtime-envelope docs/tests | Do not turn optional positives or deployment runtime into required local GA proof. |
| JVS save/restore and restore reconciliation are separate default concerns. | `CLAIM_DEFAULT_USER_LOOP`, `CLAIM_RESTORE_RECONCILIATION` | P1c/P4b | Default positive plus default safety | P1c API/repoexec/worker/store tests; restore reconciliation tests for no credential reissue/no purged resurrection/mismatch intervention | P1c JVS evidence does not close restore reconciliation. |
| Purge approval is optional positive; default purge denial is always required. | `CLAIM_PURGE_APPROVAL_SAFE`, `CLAIM_OPTIONAL_DENIED_SAFE` | P4d | Selected optional positive plus default negative | Default denial tests, fixture approval object, expiry/scope/policy/hash/replay/audit tests | Approval-like input in default profile must still deny. |
| Release evidence is selector-driven and artifact identity bound. | `CLAIM_RELEASE_GATE_TRACEABLE`, `CLAIM_WORKFLOW_HARDENING_GUARD` | P0/P5 | Default release safety | Manifest/selector/digest tests, generated report layout, workflow/script guard, release script | No manual `-mode final`; generated reports are outputs, not same-run inputs. |
| Product-neutral conformance is repo-local smoke/fixture evidence. | `CLAIM_DEFAULT_USER_LOOP`, `CLAIM_DISCOVERY_SURFACES`, selected optional fixture claims | P1/P2/P4 | Default smoke plus selected optional | Caller credential relay, connector WebDAV access/revoke, orchestrator default-denied/discovery, operation inspection negatives | Not a sibling gate and not a business project acceptance test. |

## User Journeys

| Journey | Default/optional | Acceptance |
| --- | --- | --- |
| Day-0 bootstrap | Default prerequisite | Volume health/preflight, namespace binding, caller/operator role readiness, path redaction, machine-checkable bootstrap evidence. |
| Trusted caller default loop | Default positive | Repo create/get/projection/list; JVS save/history/restore-preview/restore-run/discard; WebDAV export/gateway/revoke; operation/audit/recovery trace. |
| Retained lifecycle | Default positive | Archive, restore_archived, delete-tombstone, restore_tombstoned with admission, session/fence predicate, worker recovery, stable errors, audit, schema/OpenAPI, runbook, manifest evidence. |
| Default failure loop | Default negative | Unauthorized, policy denied, capability disabled, stale, revoked, expired, unsupported, and redaction paths fail closed and audit. |
| Workload teardown-only | Default safety for optional capability | Only scoped orchestrator/operator reader can see teardown-only plan; no mount material; audit emitted; stale closure depends on P3 repair. |
| Optional fixture positive | Selected optional | Fixture evidence only after selector claims capability. |

## Product-Neutral Happy And Failure Journeys

These journeys are developer handoff stories for API/runtime evidence. They are
not product UX, sibling project, or deployment gates.

| Journey | Happy path proof | Failure path proof |
| --- | --- | --- |
| Bootstrap to namespace-ready | Admin validates volume preflight, namespace binding, caller/operator policy, and redacted readyz. | Missing storage, missing caller role, or unsafe path fails closed with stable reason and no secret/path leak. |
| Trusted caller repo loop | Caller creates repo, reads repo projection/list, saves JVS point, previews/runs/discards restore, exports WebDAV access, and revokes it. | Namespace deny, capability deny, stale operation, revoked/expired access, or JVS ambiguity returns stable error/audit and recoverable operation state. |
| Connector WebDAV relay | AFSCP issues one-time short-lived credential to trusted caller; connector uses gateway only. | Replay, expiry, revoke, wrong namespace, and runtime ledger mismatch deny without credential reissue. |
| Operator investigation | Operator finds operation/intervention/fence/session/audit evidence through scoped inspection. | Missing or ambiguous state stays visible to recovery and enters `operator_intervention_required` with runbook ref. |
| Optional capability default denial | Workload mount, template/clone, purge, and runtime positives are discoverable as disabled/denied. | New operations deny before queue; historical operations remain visible and terminalize or require intervention. |

## Architecture Contract Seeds Required Before Implementation

These are development deliverables and contract seeds, not a new stage model.
Positive default-loop implementation should not get ahead of the contracts that
decide admission, recovery, terminalization, and evidence behavior.

| Contract seed | Owner | Required before |
| --- | --- | --- |
| `capability-matrix-v1` row inventory | P2a | Contract seed for surface-decision rows. It is not runtime parity closure. New default positive or optional-gated mutations rely on one matrix for API admission, worker execution, recovery, readyz/discovery, operator inspection, and evidence classification. |
| Operation terminalization/state-machine extension | P2a | Contract seed in `docs/contracts/operation-state-machine-v1.md`. It is not full API/worker/recovery implementation closure. Any slice that creates or recovers durable operations needs side-effect boundary, replay rule, failed/intervention decision, stable errors, audit, and historical recovery visibility. |
| Operator repair contract | P3 | `docs/contracts/operator-repair-v1.md`; any stale/intervention closure that would otherwise require ad hoc SQL; workload teardown-only stale closure must use this shared contract. |
| Restore consistency contract | P4b | Backup/restore reconciliation, retained lifecycle restore, purge invariant, and no credential reissue claims. |
| Release evidence contract | P0/P5 | Seed/final correctness, selector/digest semantics, generated artifacts, workflow/publication hardening, and doc-sync evidence. |

The semantic dependency is:

```text
P0 -> P2a capability + terminalization contract
   -> P2b runtime parity
   -> P1b/P1c/P1d default loop and dependent P3/P4 claim slices
   -> P5 publication hardening
```

This is not a rollout phase. It only says positive default-loop work must not
close without the capability/admission/recovery/terminalization runtime parity
it depends on.

## Current Next-Stage Queue

Use the current manifest as the source of truth. If a listed seed gap has
already been replaced by exact evidence, verify the replacement shape and skip
to the next open default blocker.

1. Default user loop aggregation: if `seed_gap_default_user_loop_open` remains
   open, close it only with `default_user_loop_positive_unit` proving exact
   P2b/P1b/P1c/P1d evidence plus caller-scoped operation/audit/recovery trace.
2. P3 operator repair: if `seed_gap_operator_repair_safe_open` remains open,
   add the allowlisted repair contract, one API or CLI entry, shared audit
   shape, and denial/intervention tests. If already closed, validate the
   evidence and skip.
3. P4b restore reconciliation: close `seed_gap_restore_reconciliation_open`
   with reconciliation, no credential reissue, no purged resurrection, and
   mismatch-to-intervention evidence.
4. Discovery surfaces: close remaining `CLAIM_DISCOVERY_SURFACES` gaps with
   caller/orchestrator/operator/readyz output evidence, not matrix rows alone.
5. Secret/path redaction: close remaining redaction gaps across caller,
   operator, runtime, audit, and evidence surfaces touched by the current
   slice.
6. Profile, workflow, release hardening: close profile boundary, workflow guard,
   selector/digest/artifact identity, generated report, and doc-sync gaps
   through repo-local evidence.
7. Residual/deployment envelope: finish runtime support and residual-risk
   schema/audit/runbook evidence without making deployment state a local final
   gate.

Optional fixture positives for workload, template, and purge remain outside the
default queue. They become final-blocking only when the final selector claims
the capability; hard quota enforcement and deployment-specific positives remain
runtime-support or selected-optional work, not default GA positive scope.

## Capability Matrix V1 Contract

One matrix must drive API admission, worker execution, worker recovery, readyz,
actor discovery, operator inspection, and evidence classification.

Matrix rows are surface decisions, not a coarse capability list. A route
mutation normally needs separate rows for API admission, worker execution,
worker recovery, and evidence. Internal/recovery-only operations such as
`export_session_reconcile` and conditional operations such as
`migration_cutover` must explicitly state that they have no caller API
admission and must still have worker/evidence decisions.

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
one row. If caller API, worker execution, worker recovery, operator inspection,
and evidence make different decisions for the same capability, they need
separate `surface_type` rows with the same `capability_id`, explicit
configuration/readiness posture, and explicit evidence.

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

Readyz, discovery, and evidence all read the matrix, but they are different
output surfaces with different acceptance:

- Readyz reports service readiness and bootstrap/runtime health. It is not
  caller authorization and cannot prove caller access.
- Caller/orchestrator discovery reports actor-scoped capability decisions. It
  is not readiness and must not expose operator/global/runtime material.
- Evidence classification maps claims to rows and commands. It is not runtime
  admission and cannot close runtime behavior without executable runtime tests.

## Operation Terminalization Contract

P2 owns the operation terminalization contract. It is a default GA safety claim.
The per-operation inventory, side-effect/replay boundary, and failed vs
intervention tables live in `docs/contracts/operation-state-machine-v1.md`.

Required rules:

- Maintain operation_type inventory for repo create, save, restore-preview,
  restore-run, discard, WebDAV export/revoke, retained lifecycle, workload
  mount, template/clone, purge, repair, and recovery-only terminalization.
- Define side-effect boundary for each operation type: before side effect, after
  durable side effect, uncertain side effect, and replay-safe side effect.
- Prefer idempotent replay before capability denial when an operation already
  has durable side-effect evidence. Capability-disabled recovery must not hide
  or orphan historical operations.
- New disabled/unsupported operations fail before queueing when no safe
  terminalization path exists.
- Historical operations do not disappear from recovery queries because
  capability is disabled or `configured=false`; recovery visibility is part of
  the contract, not a best-effort worker detail.
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

- Always deny purge and break-glass purge, even if input looks like an
  approval.
- Audit denial without treating the approval-like input as valid.

Fixture-positive profile:

- Selected optional positive can run only in `repo-local-fixture-enabled` profile
  and only when `repo_purge` is selected by the final selector in
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
| Capability matrix | P2 | API, worker, recovery, readyz, discovery, operator inspection, and evidence agree in contract and are proven through real runtime wiring where behavior is claimed. |
| Operation terminalization | P2 | New disabled work fails before queue; historical unsupported work terminalizes or intervenes through actual worker/runtime recovery paths. |
| Access/session/fence predicates | P1/P4 | WebDAV, restore, lifecycle, workload cleanup, and retained lifecycle share stable predicate semantics. |
| Operator repair | P3 | One shared repair contract/test suite/audit schema; API or CLI entry; no arbitrary SQL. |
| Retained lifecycle | P4 | Default positive archive/restore_archived/delete-tombstone/restore_tombstoned evidence covers admission, predicate, worker, errors, audit, schema/OpenAPI, runbook, manifest. |
| Release hardening | P5 | Single script, selector, digests, generated artifacts, workflow hardening, rollback/roll-forward, doc-sync. |

## Work Packages

These packages are ownership slices, not stage GA gates or rollout phases.
However, their semantic dependencies matter:

```text
P0 -> P2a capability + terminalization contract
   -> P2b runtime parity
   -> P1b/P1c/P1d default loop and dependent P3/P4 claim slices
   -> P5 publication hardening
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
- Verifier semantics, final correctness, selector/digest, and seed/final
  correctness are P0 responsibilities. CI/workflow publication hardening is P5.

Expected red tests:

```bash
go test -count=1 ./internal/releaseevidence -run 'Test.*Selector|Test.*EvidenceStatus|Test.*Final'
go test -count=1 ./cmd/afscp-evidence-verify -run 'Test.*Selector|Test.*Final|Test.*Mode'
go test -count=1 ./internal/contractcheck -run 'Test.*GA|Test.*Release|Test.*Evidence'
```

#### P0c: Seed Gap Closure Semantics

Seed/convergence mode accepts exactly one representation per claim gap:

- Open marker: one `seed_gap_*_open` placeholder exists and no replacement
  evidence for the same claim/subclaim/acceptance exists.
- Exact replacement: the open marker is removed and exact implemented/closed
  replacement evidence exists with the expected complete manifest shape.

An exact replacement must match the full expected manifest shape:

- `claim_id`, `subclaim_id`, `acceptance_id`, and `risk_id`.
- `evidence_profile`, `default_mode`, `fixture_enabled_mode`, polarity, and
  `required`.
- Non-doc-only command and repo-local anchors.
- `evidence_status=implemented|closed`.
- Fixed `id` when the seed gap contract names a required replacement ID.

Hard fail in seed/convergence:

- Both open marker and exact replacement exist for the same claim gap.
- Neither open marker nor exact replacement exists.
- Replacement evidence uses the wrong shape.

Hard fail in final:

- Any default-required seed gap open marker hard fails in final.
- A selected optional positive open marker hard fails only when its capability is
  selected by `claimed_optional_capabilities`.
- Replacement evidence is missing, placeholder, doc-only for high-risk behavior,
  wrong-shaped, or not selected by the final selector when optional.

Precise red tests:

```bash
go test -count=1 ./internal/releaseevidence -run '^TestPackage0SeedModeAllowsClosedSeedGapReplacement$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0SeedModeRejectsMissingSeedGapWithoutReplacement$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0SeedModeRejectsOpenAndClosedSeedGapConflict$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0FinalModeRequiresReplacementEvidenceForRequiredSeedGaps$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0FinalModeRejectsFakeSameClaimReplacementForDeletedSeedGap$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0FinalModeRejectsExactReplacementShapeWithFakeAssertionForDeletedSeedGap$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0FinalModeAcceptsExpectedAssertionForDeletedAdminSeedGap$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0FinalModeDoesNotRequireOrdinaryOptionalFixtureSeedGapReplacement$'
go test -count=1 ./internal/releaseevidence -run '^TestPackage0FinalModeRequiresTargetedOptionalFixtureClaimsWhenExplicitlyRequired$'
```

### P1: Bootstrap, Default Caller Loop, And Access Predicates

Owner: API/store/WebDAV/JVS.

Work:

- P1a Admin Bootstrap + Redacted Readiness: Day-0 bootstrap, volume
  health/preflight, namespace binding, caller/operator role readiness, redacted
  readiness/projection, and machine-checkable bootstrap evidence.
- P1b Repo create/projection/list: trusted caller repo create/get/projection/list
  under authorized namespace and redacted storage projection.
- P1c JVS save/restore: pinned JVS save/history/restore-preview/restore-run/
  discard, with operation/audit/recovery trace.
- P1d WebDAV: WebDAV export/gateway/revoke, first-create credential relay
  semantics, expiry/revoke denial, and gateway policy.
- Shared access/session/fence predicate seed for restore/lifecycle.
- Quota machine-readable status or conservative naming.
- Product-neutral conformance for caller credential relay and connector
  WebDAV access/revoke.

P1 positive work must not claim `CLAIM_DEFAULT_USER_LOOP` complete until P2b
runtime parity is proven and the P1b, P1c, and P1d evidence entries are present.

Expected red tests:

```bash
go test -count=1 ./internal/api ./internal/store/postgres ./internal/exportgateway ./internal/exportaccess ./internal/exportreconcile -run 'Test.*DefaultUserLoop|Test.*Bootstrap|Test.*WebDAV.*Revoke|Test.*Quota'
go test -count=1 ./internal/contractcheck -run 'Test.*OpenAPI|Test.*Schema|Test.*Readiness'
```

### P2: Capability Matrix And Operation Terminalization

Owner: capability/API/worker/recovery.

Work:

- P2a Capability + terminalization contract seed: capability matrix v1 row
  inventory, operation_type inventory, side-effect boundary rules, replay
  precedence, failed/intervention decision, stable denial codes, and runbook
  refs. P2a is a contract seed only; it does not prove complete API, worker,
  and recovery runtime parity or close all Finding 2 risk.
- P2b Runtime parity: API admission, worker executor registration, worker
  recovery historical visibility, unsupported/capability-disabled
  terminalization, actor discovery parity, and evidence parity all use the same
  surface-decision matrix.
- P2b closure requires real production runtime wiring evidence. Matrix/helper
  tests are allowed as support, but they cannot close
  `CLAIM_CAPABILITY_MATRIX_CONSISTENT`, `CLAIM_OPERATION_TERMINALIZATION`, or
  `CLAIM_DISCOVERY_SURFACES` runtime parity by themselves.
- API evidence must exercise real handlers and intake paths: disabled optional
  create/mutation requests deny before metadata lookup and before queueing, and
  existing idempotent operations replay before capability denial with stable
  audit and redaction.
- Worker evidence must exercise real `RunOnce`/runtime/coordinator behavior or
  the registry actually used by the runner: historical operations stay visible
  to recovery when capabilities are disabled or unavailable, unsupported work is
  persisted as `operator_intervention_required` or stable failure, and audit and
  stable errors are emitted.
- Default negatives for workload mount, template/clone, purge, deployment
  runtime.
- Discovery surfaces split by actor.

Expected red tests:

```bash
go test -count=1 ./internal/capability ./internal/contractcheck -run 'Test.*DecisionRows|Test.*Operation.*Contract'
go test -count=1 ./internal/api ./internal/workerapp -run 'Test.*Admission.*Disabled|Test.*Executor.*Registration|Test.*Recovery.*Historical|Test.*Unsupported.*Terminal'
go test -count=1 ./internal/contractcheck -run 'Test.*Capability|Test.*Discovery'
```

P2b expected red-test direction:

```bash
go test -count=1 ./internal/api -run 'Test.*AdmissionDisabled.*(ReplaysExisting|Rejects.*BeforeMetadata|BeforeQueue|Audits)'
go test -count=1 ./internal/workerapp -run 'TestRunOnce.*(Disabled|Unavailable).*PersistsUnsupportedIntervention'
go test -count=1 ./internal/capability -run 'Test.*DecisionRows.*(Map|EvidenceRefs)'
```

Avoid broad regexes that match only helper/matrix tests. The manifest command
selector for P2b evidence must name the real API and worker runtime tests needed
for closure, with helper tests included only as auxiliary mapping coverage.

P2b must not implement P1 default user loop positives. It only proves the
shared capability/terminalization runtime path that later P1/P3/P4 work depends
on.

### P3: Operator Inspection And Shared Repair

Owner: operator/admin surface.

Work:

- P3 is not a complete operator platform.
- Authoritative contract artifact: `docs/contracts/operator-repair-v1.md`.
- Inspection for correlated operation lookup, intervention queue, held
  fence/session, stale lease, recovery state, and audit lag.
- One shared repair contract/test suite/audit schema.
- One allowlisted repair entry implementation: API or CLI. GA does not require
  both.
- Reason, evidence reference, affected IDs, before/after state, safety
  predicate, audit event.
- The contract must define allowed actions, preconditions, reason vocabulary,
  evidence inputs, before/after state shape, audit schema, stable
  denial/intervention behavior, and relationship to
  `docs/contracts/operation-state-machine-v1.md`.
- Minimum operable loop: correlated operation lookup, intervention queue view,
  held fence/session visibility, stale lease and audit lag visibility, one
  allowlisted repair action, and audit tying reason/evidence to before/after
  state.
- Workload teardown-only stale closure uses this repair contract.
- Arbitrary SQL, generic state rewrite, unrestricted fence release, and
  workload-specific repair bypasses are forbidden.

Expected red tests:

```bash
go test -count=1 ./internal/api ./internal/workerapp ./internal/store/postgres -run 'Test.*Operator.*Inspection|Test.*Repair|Test.*Intervention|Test.*Audit'
go test -count=1 ./internal/contractcheck -run 'Test.*Runbook|Test.*Repair'
```

### P4: Retained Lifecycle, Restore Reconciliation, And Optional Fixture Positives

Owner: lifecycle/restore/optional capabilities.

Work:

- P4a Retained lifecycle: default positive archive/restore_archived/
  delete-tombstone/restore_tombstoned with admission, session/fence predicate,
  worker recovery, stable errors, audit, schema/OpenAPI, runbook, and manifest
  evidence.
- P4b Restore reconciliation: fixed restore drain decision, backup/restore
  reconciliation, storage generation/snapshot/tombstone markers, no credential
  reissue, no purged resurrection, and mismatch-to-intervention.
- P4c Optional fixture positives: workload mount fixture positive only when
  selected; template/clone fixture positive or clone-primitive naming alignment.
- P4d Purge approval: purge approval fixture positive, default denial even with
  approval-like input, expiry/scope/policy/hash/replay negatives, and audit hash
  binding.

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
- P5 owns CI/workflow/artifact publication hardening, doc sync, and runtime
  envelope. P0 owns verifier semantics/final correctness/selector/digest and
  seed/final semantics.
- Workflow hardening's repo-local boundary is checking workflow YAML
  declarations and release script wiring. Branch protection, real published
  artifacts, and live GitHub environment settings are not local gate proof.
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

## Per-Slice Handoff Template

Every slice that closes or advances a finding must state the following before
implementation and keep the manifest/release guard aligned:

| Required field | Slice answer must include |
| --- | --- |
| Seed gap | `seed_gap_id`, whether it stays open or is removed, and the exact replacement rule. |
| Evidence identity | Replacement evidence ID plus `claim_id`, `subclaim_id`, `acceptance_id`, risk, capability, profile, polarity, required/default/optional semantics, and evidence type. |
| Command | Exact focused `go test`/contract/release command from the evidence item, plus the full release gate command when release evidence or final-candidate behavior changes. |
| Real behavior | Runtime tests for the touched surface: API admission before queue, worker RunOnce/recovery, store atomicity, discovery/inspection output, redaction, or gateway/session behavior as applicable. |
| Non-scope | What the slice explicitly does not close, especially default-loop aggregation, optional positives, deployment runtime, retained lifecycle, restore reconciliation, or operator repair. |
| Negative guards | Missing item, placeholder, wrong claim/subclaim/acceptance, wrong profile/polarity, broad selector, helper-only selector, doc-only overclaim, partial-only closure, and selected-optional mismatch. |

Selectors must be precise enough that reviewers can see which production path is
covered. Helper/matrix tests may support a slice, but cannot be the only proof
for API admission, worker recovery, terminalization, discovery, default-loop,
WebDAV/JVS, operator repair, restore reconciliation, or release gate behavior.

## Package Evidence Index

This index tells PR authors what seed gap they are replacing and how seed/final
should behave. Exact command regexes may evolve, but each replacement must keep
the claim, profile, polarity, and selected/unselected semantics. Target commands
must become real repo-local tests before replacement evidence is marked
`implemented`/`closed`.

Runtime behavior claims in this table require executable repo-local runtime
evidence. Contract or helper tests may accompany them, but cannot be the only
selector content for API admission, worker recovery, discovery, terminalization,
or default-loop behavior.

Broad regexes in this document are discovery or draft-red-test examples only.
Manifest evidence commands must be replaced at implementation time with a
precise selector or exact test list, especially for P3/P4/P5 and
product-neutral conformance rows.

| Slice | Claim | Seed gap ID | Replacement evidence shape / ID | Type/profile | Target command rule | Seed/final behavior |
| --- | --- | --- | --- | --- | --- | --- |
| P1a admin bootstrap | `CLAIM_ADMIN_BOOTSTRAP_READY` | `seed_gap_admin_bootstrap_ready_open` | Current/closed-or-closing slice: `admin_bootstrap_ready_unit`; covers volume preflight, namespace binding, caller/operator role readiness, path redaction. | unit or contract / `default` | `go test -count=1 ./internal/api ./internal/apiapp ./internal/contractcheck -run 'Test.*(AdminBootstrap|Readiness.*Bootstrap|RedactsAdminBootstrap|Readiness|OpenAPI|Schema)'` | If manifest already has `admin_bootstrap_ready_unit` and no open admin seed gap, do not repeat P1a. Default-required final acceptance requires non-placeholder exact replacement. |
| P1b repo create/projection/list | `CLAIM_DEFAULT_USER_LOOP` | `seed_gap_default_user_loop_open` | `default_user_loop_repo_projection_unit`; repo create/get/projection/list under authorized namespace with redacted projection. | unit/contract / `default` | `go test -count=1 ./internal/api ./internal/store/postgres ./internal/workerapp -run '<selector from default_user_loop_repo_projection_unit>'` | Partial replacement only; does not close `CLAIM_DEFAULT_USER_LOOP` alone. If the manifest already has `default_user_loop_repo_projection_unit` and `seed_gap_default_user_loop_open` remains open, P1b repo projection is closed; do not repeat it. The selector must stay precise and hit real API/store/worker tests: create validation-before-intake, repo read validation-before-store, namespace-scoped boundary, store commit, and worker positive. Requires P1c/P1d plus existing P2b runtime parity before final default-loop closure. |
| P1c JVS save/restore | `CLAIM_DEFAULT_USER_LOOP` | `seed_gap_default_user_loop_open` | `default_user_loop_jvs_save_restore_unit`; save/history/restore-preview/restore-run/discard with operation/audit/recovery trace. | unit/integration / `default` | `go test -count=1 ./internal/api ./internal/repoexec ./internal/workerapp ./internal/store/postgres -run '<selector from default_user_loop_jvs_save_restore_unit>'` | Partial replacement only; does not close `CLAIM_DEFAULT_USER_LOOP` alone. If the manifest already has `default_user_loop_jvs_save_restore_unit` and `seed_gap_default_user_loop_open` remains open, P1c JVS save/restore is closed; do not repeat it. The selector must stay precise and hit real API/store/repoexec/worker tests for savepoint create/history, restore-preview, restore-run, restore-discard, JVS marker/doctor/pin checks, and atomic store boundaries. Requires P1d plus existing P2b runtime parity before final default-loop closure. |
| P1d WebDAV default access | `CLAIM_DEFAULT_USER_LOOP`, `CLAIM_WEBDAV_DEFAULT_ACCESS` | `seed_gap_default_user_loop_open`, `seed_gap_webdav_default_access_open` | P1d usually needs two manifest evidence items: `webdav_default_access_unit` closes `CLAIM_WEBDAV_DEFAULT_ACCESS` and `seed_gap_webdav_default_access_open`; `default_user_loop_webdav_access_unit` contributes only partial `CLAIM_DEFAULT_USER_LOOP` evidence and keeps `seed_gap_default_user_loop_open`. Different names are allowed only with equivalent exact claim/subclaim/acceptance guards. Covers first-create credential, trusted-caller relay, gateway access, expiry/revoke, replay redaction, ledger/audit. | integration/contract / `default` | `go test -count=1 ./internal/api ./internal/exportaccess ./internal/exportgateway ./internal/exportreconcile ./internal/store/postgres -run '<precise selector from P1d evidence item>'` | No single WebDAV evidence item may be read as closing the full default loop. If the manifest already has both `webdav_default_access_unit` and `default_user_loop_webdav_access_unit`, and `seed_gap_default_user_loop_open` remains open, P1d WebDAV default access is closed; do not repeat it. Selector must hit real WebDAV API/gateway/access/reconcile/store tests, not helper-only or broad regex. |
| Default user loop aggregation | `CLAIM_DEFAULT_USER_LOOP` | `seed_gap_default_user_loop_open` | `default_user_loop_positive_unit`; aggregation-only evidence that proves P1b repo projection, P1c JVS save/restore, P1d WebDAV default access, caller-scoped operation/audit/recovery trace, and P2b runtime parity evidence are all exact, non-placeholder, and selector-precise. | releaseevidence/contract / `default` | `go test -count=1 ./internal/releaseevidence ./cmd/afscp-evidence-verify -run 'Test.*DefaultUserLoop.*Aggregation|Test.*SeedGap'`; then `bash scripts/verify-ga-release.sh` | Only this slice may close `seed_gap_default_user_loop_open`. It must fail if any P1b/P1c/P1d/P2b item or caller-scoped trace evidence is missing, placeholder, helper-only, broad-regex, wrong profile/polarity, or not selected by exact claim/subclaim/acceptance guard. |
| Product-neutral conformance smoke | `CLAIM_DEFAULT_USER_LOOP`, `CLAIM_DISCOVERY_SURFACES`, selected optional fixture claims as applicable | Relevant manifest gaps only; not a sibling gate. | `product_neutral_conformance_*`; caller credential relay, connector WebDAV access/revoke, orchestrator default-denied/discovery, operation inspection negative cases, negative authorization. | smoke/fixture/contractcheck / `default` plus selected fixture | `go test -count=1 ./internal/contractcheck ./internal/api ./internal/exportgateway ./internal/exportaccess -run '^Test(ProductNeutral|ConnectorWebDAV|CallerCredentialRelay|Orchestrator.*Denied|OperationInspection.*Negative)'` | Repo-local fixture/smoke hook. It may support claim closure only when exact manifest evidence exists; it never waits for a sibling project. |
| P2a capability/terminalization contract seed | `CLAIM_CAPABILITY_MATRIX_CONSISTENT`, `CLAIM_OPERATION_TERMINALIZATION` | Existing implemented evidence plus any new open seed gaps from manifest. | `capability_matrix_v1_contract_unit`, `operation_terminalization_contract_unit`; operation inventory/side-effect/decision table seed. | unit/contract / `default` | `go test -count=1 ./internal/capability ./internal/contractcheck -run 'Test.*DecisionRows|Test.*Operation.*Contract'` | Contract seed only. Must not be described as full API/worker/recovery runtime parity or full Finding 2 closure. |
| P2b runtime parity | `CLAIM_CAPABILITY_MATRIX_CONSISTENT`, `CLAIM_OPERATION_TERMINALIZATION`, `CLAIM_DISCOVERY_SURFACES` | New P2b evidence or remaining open gaps from manifest. | `capability_runtime_parity_*`, `operation_runtime_terminalization_*`; real API handler/intake disabled admission and real worker RunOnce/recovery terminalization evidence, with matrix/helper tests as auxiliary mapping coverage only. | unit/contract / `default` | API selector must include concrete handler tests such as WebDAV export, workload mount, repo template, and repo purge disabled admission replay/before-metadata/audit cases; worker selector must include concrete `RunOnce*Disabled*PersistsUnsupportedIntervention` and unavailable-runtime tests. Avoid broad helper-only regex. | Required before P1b/P1c/P1d can close the caller loop. Does not itself implement caller-loop positives. Does not close discovery unless caller/orchestrator/operator/readyz output surface tests are present. |
| P3 operator repair | `CLAIM_OPERATOR_REPAIR_SAFE` | `seed_gap_operator_repair_safe_open` | `operator_repair_safe_*`; shared contract/test suite/audit schema, one API or CLI entry. | unit/contract / `default` | `go test -count=1 ./internal/api ./internal/workerapp ./internal/store/postgres -run 'Test.*Operator.*Inspection|Test.*Repair|Test.*Intervention|Test.*Audit'` | Seed accepts open marker until replacement exists. Final requires replacement for default safety claim. |
| P4a retained lifecycle | `CLAIM_RETAINED_LIFECYCLE_DEFAULT` | No current open seed gap if `repo_lifecycle_retained_positive_unit` remains exact replacement. | `repo_lifecycle_retained_positive_unit` plus any schema/OpenAPI/runbook replacements needed for final. | unit/contract / `default` | `go test -count=1 ./internal/api ./internal/repoexec ./internal/store/postgres ./internal/workerapp -run 'Test.*RetainedLifecycle|Test.*Lifecycle'` | Keep default positive; do not move purge into retained lifecycle. |
| P4b restore reconciliation | `CLAIM_RESTORE_RECONCILIATION` | `seed_gap_restore_reconciliation_open` | `restore_reconciliation_*`; reconciliation mode, no credential reissue, no purged resurrection, mismatch intervention. | integration/contract / `default` | `go test -count=1 ./internal/api ./internal/repoexec ./internal/store/postgres ./internal/workerapp -run 'Test.*Restore.*Reconciliation|Test.*Restore.*Drain'` | Final rejects open required restore reconciliation gap. |
| P4c optional fixture conformance umbrella | `CLAIM_OPTIONAL_FIXTURE_CONFORMANT` | `seed_gap_optional_fixture_conformant_open` | `optional_fixture_conformant_*`; selected optional capability aggregation and selector shape. | contract / `repo-local-fixture-enabled` | `go test -count=1 ./internal/releaseevidence -run '^Test.*Optional.*Fixture|^Test.*ClaimedOptional'` | Unselected optional positive gaps do not block default final. Selected optional positives require exact non-placeholder replacements. |
| P4c workload fixture readiness | `CLAIM_WORKLOAD_FIXTURE_READY` | `seed_gap_workload_fixture_ready_open` | `workload_fixture_ready_*`; plan fetch, heartbeat, release, revoke, terminal evidence. | fixture/integration / `repo-local-fixture-enabled` | `go test -count=1 ./internal/api ./internal/workerapp ./internal/store/postgres -run '^Test(Workload.*Fixture|Workload.*Heartbeat|Workload.*Release|Workload.*Revoke|Workload.*Terminal)'` | Only required when workload capability is selected by `claimed_optional_capabilities`. |
| P4c template/quota boundary | `CLAIM_TEMPLATE_QUOTA_BOUNDARY` | `seed_gap_template_quota_boundary_open` | `template_quota_boundary_*`; template default denial, selected same-namespace/same-volume clone boundary, quota status. | unit/contract / default plus selected fixture | `go test -count=1 ./internal/api ./internal/contractcheck -run '^Test(Template.*Boundary|Template.*Clone|Quota.*Status|Schema.*Quota)'` | Default template denial is required; selected template positive only blocks when selected. Quota status is default wording/schema safety. |
| P4d purge default denial | `CLAIM_OPTIONAL_DENIED_SAFE` | No current open purge-denial seed gap if `repo_purge_disabled_admission_unit` and `repo_purge_disabled_worker_recovery_unit` remain exact replacements. | `repo_purge_disabled_admission_unit`, `repo_purge_disabled_worker_recovery_unit`; deny before metadata/queue and recover historical disabled purge. | unit / `default` | `go test -count=1 ./internal/api ./internal/workerapp -run '^Test(RepoLifecyclePurgeAdmissionDisabledRejectsNewBeforeMetadataAndAudits|RunOnceRepoPurgeDisabledScansAndPersistsUnsupportedIntervention)$'` | Always required default negative, independent of whether purge positive is selected. |
| P4d purge approval fixture-positive | `CLAIM_PURGE_APPROVAL_SAFE` | `seed_gap_purge_approval_safe_open` | `purge_approval_safe_*`; fixture approval object and negative controls. | unit/contract / `repo-local-fixture-enabled` positive plus default denial guards | `go test -count=1 ./internal/api ./internal/repoexec ./internal/store/postgres -run '^Test.*Purge.*Approval|^Test.*Purge.*Replay|^Test.*Purge.*Scope'` | Positive only blocks when `repo_purge` is selected. Default profile still denies approval-like input. |
| P5 release hardening | `CLAIM_RELEASE_GATE_TRACEABLE`, `CLAIM_DEPLOYMENT_RISK_ENVELOPE`, workflow guard | `seed_gap_workflow_hardening_guard_open`, `seed_gap_deployment_risk_envelope_open` | `workflow_hardening_guard_*`, `deployment_risk_envelope_*`, generated artifact/report evidence. | contract/doc-guard/provenance / `default` or runtime support | `go test -count=1 ./internal/contractcheck -run 'Test.*GA|Test.*Release|Test.*Workflow|Test.*Docs'`; `bash scripts/verify-ga-release.sh` | Runtime envelope is never local positive proof; workflow guard checks repo-local YAML/script declarations only. |

### Open Seed Gap Backlog

The manifest is authoritative. This table is a handoff highlight list, not a
complete replacement for `docs/release-evidence/ga-manifest.json`. When the
manifest and this table differ, update this table or the relevant work package;
do not infer release status from this highlight list alone.

| Seed gap | Claim | Owner | Default/runtime/optional semantics |
| --- | --- | --- | --- |
| `seed_gap_default_user_loop_open` | `CLAIM_DEFAULT_USER_LOOP` | P1/P2/P5 | Default caller loop remains open until repo, JVS, WebDAV, operation/audit/recovery trace, and P2b runtime parity are all exact non-placeholder evidence. |
| `seed_gap_operator_repair_safe_open` | `CLAIM_OPERATOR_REPAIR_SAFE` | P3 | Default safety; final requires `docs/contracts/operator-repair-v1.md`, one API or CLI entry, stable audit/denial/intervention evidence, and no arbitrary SQL. |
| `seed_gap_restore_reconciliation_open` | `CLAIM_RESTORE_RECONCILIATION` | P4b | Default safety; final requires reconciliation mode, dangerous-write denial, no credential reissue, no purged resurrection, and mismatch-to-intervention evidence. |
| `seed_gap_webdav_default_access_open` | `CLAIM_WEBDAV_DEFAULT_ACCESS` | P1d/P5 | Default positive subclaim. If manifest already has exact `webdav_default_access_unit`, this gap is closed and should not be reintroduced. |
| `seed_gap_secret_path_redaction_open` | `CLAIM_SECRET_PATH_REDACTION` | P1/P2/P5 | Default safety; final requires redaction evidence for touched caller/operator/runtime/audit/evidence surfaces. |
| `seed_gap_discovery_surfaces_open` | `CLAIM_DISCOVERY_SURFACES` | P2/P3 | Default safety; caller/orchestrator/operator/readyz surfaces must stay layered. |
| `seed_gap_profile_boundary_open` | `CLAIM_PROFILE_BOUNDARY` | P0/P5 | Default release/profile guard; final requires profile boundary evidence. |
| `seed_gap_residual_risk_catalog_open` | `CLAIM_RESIDUAL_RISK_CATALOG` | P3/P4/P5 | Default/runtime-risk safety; risk acceptance shape is repo-local schema/fixture/audit, not human approval. |
| `seed_gap_optional_fixture_conformant_open` | `CLAIM_OPTIONAL_FIXTURE_CONFORMANT` | P4/P5 | Selected optional umbrella; does not block default final unless selector claims optional capability. |
| `seed_gap_workload_fixture_ready_open` | `CLAIM_WORKLOAD_FIXTURE_READY` | P4c | Selected optional workload fixture; only required under `repo-local-fixture-enabled` when selected by `claimed_optional_capabilities`. |
| `seed_gap_template_quota_boundary_open` | `CLAIM_TEMPLATE_QUOTA_BOUNDARY` | P4c/P5 | Default template/quota wording safety plus selected template positive boundary. Keep selected positive non-default. |
| `seed_gap_purge_approval_safe_open` | `CLAIM_PURGE_APPROVAL_SAFE` | P4d | Selected optional purge positive; default purge denial remains separate always-required negative evidence. |
| `seed_gap_workflow_hardening_guard_open` | `CLAIM_WORKFLOW_HARDENING_GUARD` | P5 | Release safety; repo-local workflow YAML/script declaration guard only. |
| `seed_gap_deployment_risk_envelope_open` | `CLAIM_DEPLOYMENT_RISK_ENVELOPE` | P5 | Runtime support only; never local positive final proof. |

## Next Slice Guidance

Recommended next slice depends on the current manifest state:

| Branch state | Recommended next slice | Why |
| --- | --- | --- |
| P2b/P1b/P1c/P1d evidence are exact but `seed_gap_default_user_loop_open` remains open | Default user loop aggregation guard. | Close the caller loop only through `default_user_loop_positive_unit`, with exact P2b/P1b/P1c/P1d evidence and caller-scoped operation/audit/recovery trace. |
| Default user loop aggregation is closed and `seed_gap_operator_repair_safe_open` remains open | P3 Operator repair. | Operator repair is the next default safety blocker; it must be allowlisted repair with shared audit/test contract, not a generic operations platform. |
| Operator repair is closed and `seed_gap_restore_reconciliation_open` remains open | P4b Restore reconciliation. | Restore reconciliation is separate from P1c restore-run and must prove no credential reissue, no purged resurrection, and mismatch-to-intervention behavior. |
| P3/P4b blockers are closed but discovery/redaction/profile/release gaps remain | Discovery surfaces, secret/path redaction, profile/workflow/release hardening. | These are default safety/release claims and should close with exact repo-local evidence before selected optional positives. |
| Default safety/release blockers are closed but runtime envelope gaps remain | Residual/deployment envelope. | Runtime-support/doc-sync only: use repo-local schema/doc/audit/runbook guards; do not require production deployment state and do not treat it as a default blocker or local positive final proof. |
| Default blockers are closed and optional capabilities are selected by the final selector | Selected optional fixture positives. | Workload/template/purge positives block only under selector-selected `repo-local-fixture-enabled` semantics. |
| P2a is not closed | Finish P2a Capability + Terminalization Contract Seed. | Later durable positives need the matrix/state-machine contract seed. |
| P2a is closed | P2b Runtime Parity. | Contract seed is not enough; API admission, worker registration/recovery, unsupported terminalization, discovery, and evidence must align at runtime. |
| P2b is closed but P1b or P1c is missing | Fill the missing P1 partial evidence first. | The default loop aggregation depends on exact repo and JVS partial evidence before WebDAV can complete the caller-loop set. |
| P2b, P1b, and P1c evidence are present but P1d is missing | P1d WebDAV default access. | Runtime parity plus repo/JVS partial evidence are present. WebDAV is the next caller-loop positive; it may close only `CLAIM_WEBDAV_DEFAULT_ACCESS` and add partial default-loop evidence. |

Fallback: if the current branch does not contain `admin_bootstrap_ready_unit`
replacing `seed_gap_admin_bootstrap_ready_open`, then P1a Admin Bootstrap +
Redacted Readiness is the prerequisite micro-slice. P1a may land before full
P2a only if it does not create durable repo/JVS/WebDAV mutation admission.

For older branches, use the table above to close missing prerequisites before
the current queue. If the branch already has P2b, P1b, P1c, and P1d evidence
present, do not repeat P1d; close the default user loop aggregation if its seed
gap remains open. If P1d is still missing, close WebDAV default access first:

- Add exact `CLAIM_WEBDAV_DEFAULT_ACCESS` replacement for
  `seed_gap_webdav_default_access_open`.
- Add default-user-loop WebDAV partial evidence that keeps
  `seed_gap_default_user_loop_open`.
- Add P1d manifest guards for exact claim/subclaim/acceptance/profile/polarity
  shape and partial-gap behavior.
- Use a precise P1d selector that hits real WebDAV API/gateway/access/
  reconcile/store behavior tests.

Keep open after P1d:

- `seed_gap_default_user_loop_open` until the separate aggregation guard closes
  it.
- Operator repair, restore reconciliation, discovery/redaction, release
  hardening, optional positives, and deployment runtime envelope gaps.
- Workload mount positive, template positive, purge positive, hard quota
  enforcement, and shared-volume deployment/runtime specifics unless a later
  selector explicitly claims them.

## TDD Rules

Every PR starts with a failing test, schema assertion, contract guard, doc guard,
or manifest evidence expectation that names the claim being closed.

Required PR shape:

1. Add failing evidence/test/guard.
2. Implement the smallest code, schema, doc, or manifest change.
3. If touching manifest evidence, assert exact `id`, `claim_id`,
   `subclaim_id`, `acceptance_id`, `risk_id`, `capability_id`,
   `evidence_status`, profile, polarity, `required`, `default_ga_required`,
   `optional_gated`, `doc_only_allowed`, command packages, and selector.
4. Keep partial evidence honest: the related seed gap stays open until exact
   replacement is present, and only exact replacement closes a seed gap.
5. Ensure selectors hit real behavior tests for the claim. Helper, matrix, or
   doc-only tests may support but cannot be the only evidence for runtime
   behavior.
6. Update touched contract/schema/OpenAPI/runbook/evidence entries.
7. Run the gate appropriate to the change:

```bash
# every slice
go test -count=1 <touched packages> -run '<precise selector>'

# manifest/evidence changes
go run ./cmd/afscp-evidence-verify -mode seed -manifest docs/release-evidence/ga-manifest.json -check-only

# release/final-candidate triggers
bash scripts/verify-ga-release.sh
```

Each PR must publish the unique rerunnable command set it used. Keep commands
focused to the touched claim; do not add broad unrelated test runs as a
substitute for precise evidence. Any PR that adds, replaces, or closes manifest
evidence must record and run the exact `command` from each changed evidence
item; the seed verifier `-check-only` is a structure guard and cannot replace
that evidence command. Run `bash scripts/verify-ga-release.sh` when any
release/final-candidate trigger is present: `docs/release-evidence/ga-release-selector.json`
exists with `release_intent=final_candidate`, `AFSCP_RELEASE_INTENT=final_candidate`
is set, the PR modifies selector/final evidence/evidence_status/seed-gap
closure/release workflow, or the PR is a release candidate. Final acceptance is
judged only from a clean checkout by the unique script exit code and
manifest/selector machine judgment.

Do not:

- Turn optional positive capabilities into default GA.
- Add sibling repo checks.
- Use manual approval as a release gate.
- Use deployment-runtime-support as local positive final proof.
- Let placeholder evidence satisfy required final acceptance.
- Leave high-risk claims as doc-only evidence.

## Evidence Strength Matrix

Runtime behavior claims require executable repo-local evidence. Doc, contract,
schema, and matrix/helper tests can seed or support runtime claims, but cannot
close API admission, worker recovery, discovery, terminalization, default-loop,
or WebDAV/JVS behavior unless the claim is explicitly contract-only.

P2b evidence selectors must be narrow enough to prove production runtime wiring:
real API handler/intake tests for before-queue denial and replay-before-denial,
and real worker `RunOnce`/runtime/coordinator tests or the registry actually
used by the runner for historical recovery visibility and persisted
intervention/audit. Broad regexes that match only helper tests are invalid for
P2b closure.

Evidence status:

| Status | Meaning |
| --- | --- |
| `placeholder` | Static manifest marker for an open gap. Never a passing final result. |
| `implemented` | Static manifest says repo-local evidence exists and should run or be checked. |
| `closed` | Static manifest says the gap is closed by evidence. It does not mean the current command run passed. |

| Claim/area | Minimum evidence strength | Explicitly insufficient |
| --- | --- | --- |
| Default caller loop aggregation | Releaseevidence/CLI guard proving P1b repo, P1c JVS, P1d WebDAV, caller-scoped operation/audit/recovery trace, and P2b runtime parity exact evidence all exist and selectors are precise. | Any single P1b/P1c/P1d partial item, missing trace evidence, broad regex, or helper-only check. |
| WebDAV default access | Real API/exportaccess/exportgateway/exportreconcile/store tests for one-time credential issuance to trusted caller, caller relay safety, gateway access, expiry/revoke, replay redaction, ledger/audit, and stable failures. | Gateway helper tests without handler/store/ledger path; doc-only credential description. |
| JVS save/restore | Real API/repoexec/worker/store tests for savepoint create/history, restore-preview/run/discard, JVS pin/marker/doctor behavior, crash recovery, and atomic plan/operation commits. | JVS helper or history parser tests alone. |
| Operator repair | `docs/contracts/operator-repair-v1.md`, one API or CLI entry, allowlisted actions/preconditions/reason/evidence/before-after/audit schema, stable denial/intervention tests. | Arbitrary SQL, runbook-only repair, or manual approval. |
| Restore reconciliation | Executable tests for no credential reissue, dangerous-write denial, no purged resurrection, metadata/storage mismatch to intervention, plus schema/runbook evidence. | P1c restore run evidence alone. |
| Retained lifecycle | Admission, session/fence predicate, worker recovery, stable errors, audit, schema/OpenAPI, runbook, manifest evidence. | Lifecycle doc or handler-only happy path. |
| Default negatives | Real denied/disabled/recovery/fail-closed tests proving no permanent queued operations and historical visibility. | Capability row existence without request-path or worker evidence. |
| Capability matrix | Surface-decision row tests across API, worker, recovery, readyz, discovery, operator inspection, evidence, plus runtime parity tests for behavior claims. | Coarse capability list or self-comparing helper sets. |
| Operation terminalization | Operation inventory, side-effect boundary, failed vs intervention, idempotent replay, historical visibility, and real worker/store recovery tests for runtime closure. | Contract table alone for runtime behavior. |
| Optional positives | Repo-local fixture-enabled evidence selected by final selector; default profile must still prove disabled/denied/fail-closed. | Manifest item marked required without selector claim; deployment-runtime-support. |
| Workload/template/purge positive and hard quota enforcement | Keep as selected optional or product wording/risk work unless explicitly selected by selector and exact fixture evidence exists. | Treating these as default positive scope. |
| Shared-volume/deployment runtime envelope | Detection, config, redaction, runbook, risk acceptance schema/audit; never required local positive proof. | CSI/POSIX/subPath/orchestrator/production state as GA gate. |
| Product-neutral conformance | Repo-local smoke/fixture hooks for caller credential relay, connector WebDAV access/revoke, orchestrator default-denied/discovery, operation inspection negative cases. | Sibling or business product acceptance. |
| Release evidence | Selector, digests, artifact identity, generated reports, workflow hardening, rollback/roll-forward, and release script guard. | Manual `-mode final`, generated report as same-run input, or human sign-off. |

## Handoff Definition Of Done

Current Slice DoD:

- The PR states which claim/subclaim/acceptance it advances or keeps open.
- The PR starts with a failing test, contract guard, schema/OpenAPI guard, doc
  guard, or manifest evidence expectation.
- The PR updates only the touched code, docs, contracts, schemas, runbooks, and
  evidence needed for that slice.
- Any PR that adds or replaces manifest evidence includes a manifest coverage
  test that precisely asserts evidence ID, claim, subclaim, acceptance, profile,
  polarity, status, required/default/optional semantics, target command, and
  whether each related seed gap stays open or is closed by exact replacement.
- The PR keeps default user loop, retained lifecycle, operator repair, optional
  positives, and release hardening as separate claims unless it explicitly owns
  those claims.
- The PR runs focused package tests for every slice, the seed verifier for
  manifest/evidence changes, and the exact `command` from every evidence item it
  adds, replaces, or closes.
- The PR records one rerunnable command block for reviewers. For code-only
  slices this can be focused tests; for manifest/evidence deltas it must include
  the seed verifier plus changed evidence commands; for release/final-candidate
  triggers it must include `bash scripts/verify-ga-release.sh`.
- The PR does not add sibling project, business project, manual approval, or
  production deployment gates.

Final GA DoD:

- PO contract is reflected in product, architecture, contracts, schema/OpenAPI,
  runbooks, release notes, and evidence manifest.
- Default caller loop has repo-local evidence across P1b/P1c/P1d plus P2b
  runtime parity, and the separate aggregation guard has closed
  `seed_gap_default_user_loop_open` without helper-only or broad-regex
  overclaim.
- Retained lifecycle default positive has explicit evidence owner and coverage
  for admission, session/fence predicate, worker recovery, stable errors, audit,
  schema/OpenAPI, runbook, and manifest evidence.
- Workload mount, template/clone, purge/break-glass, and real deployment runtime
  are capability-gated by default and have denied/recovery/fail-closed evidence.
- Optional positives only block final when selected by the final selector and
  exact manifest fields match.
- Deployment-runtime-support is never a required local GA positive gate.
- Restore_archived/restore_tombstoned drain behavior is fixed and proven.
- Operator repair has one shared contract/test suite/audit schema and one
  allowlisted API or CLI entry; no arbitrary SQL/state rewrite.
- Capability matrix v1 drives all surfaces and P2b production runtime parity is
  proven by real API handler/intake and worker RunOnce/runtime evidence, with
  helper/matrix tests only as supporting coverage.
- Operation terminalization rules are implemented and tested with historical
  recovery visibility.
- Generated report/evidence artifacts are deterministic and digestable.
- `bash scripts/verify-ga-release.sh` is the only release entrypoint and cannot
  pass final with open required/default gaps or selected optional positive gaps.
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
