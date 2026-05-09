# Residual Risk Catalog v1

This contract defines the repo-local, machine-checkable residual runtime risk
catalog guard for AFSCP as an independent shared file system control plane. A
structured residual risk record is not a GA release gate, not a human release
decision, and not deployment proof. The catalog guard itself remains
repo-local, machine-checkable release evidence for
`CLAIM_RESIDUAL_RISK_CATALOG`; it verifies structured residual risk record,
catalog guard, and audit shape. As a GA implementation-baseline contract,
current readiness governance and evidence status are tracked by
`docs/READINESS_EVIDENCE.md`. GA release decisions stay with
`scripts/verify-ga-release.sh`; this contract supplies the repo-local shape
that the release evidence verifier checks.

## Catalog Rows

The shared-volume threat model covers namespace isolation assumptions,
volume-admin misconfiguration, backup/restore cross-namespace residue,
POSIX/CSI drift, detection signals/metrics, compensating controls, and the
dedicated-volume escalation rule.

Every row is default-profile catalog evidence for
`CLAIM_RESIDUAL_RISK_CATALOG`. Unknown `risk_id` values, missing fields,
expired or unreviewable rows, profile mismatch, optional profile rows, and
deployment-runtime-support rows are invalid for this guard.

| risk_id | claim | scope | profile | status_decision | impact | mitigation | owner_role | review_trigger | evidence_ref | dedicated_volume_escalation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RR-SV-namespace-isolation | CLAIM_RESIDUAL_RISK_CATALOG | shared-volume namespace isolation assumptions | default | catalog_guarded | cross-namespace file visibility if namespace binding or path policy is wrong | namespace binding policy, repo path contract, operation inspection redaction, audit detection | operator_admin | review_at on namespace binding policy or path contract change | docs/contracts/namespace-volume-binding-v1.md; docs/contracts/repo-path-contract-v1.md | escalate_to_dedicated_volume when namespace isolation evidence is insufficient |
| RR-SV-volume-admin-misconfiguration | CLAIM_RESIDUAL_RISK_CATALOG | volume-admin misconfiguration | default | catalog_guarded | incorrect shared volume target can expose or orphan repo payloads | volume ensure/readiness checks, namespace binding review, operator inspection, audit trail | operator_admin | review_at on volume integration or admin role policy change | docs/contracts/namespace-volume-binding-v1.md; docs/contracts/afscp-internal-api-v1.md | escalate_to_dedicated_volume when shared volume administration cannot be constrained |
| RR-SV-backup-restore-residue | CLAIM_RESIDUAL_RISK_CATALOG | backup/restore cross-namespace residue | default | catalog_guarded | restored payload or control residue can mismatch metadata and namespace ownership | restore reconciliation, purge marker checks, dangerous write denial, mismatch audit | operator_admin | review_at on restore reconciliation or backup runbook change | docs/contracts/restore-reconciliation-v1.md; docs/contracts/operation-state-machine-v1.md | escalate_to_dedicated_volume when residue cannot be reconciled per namespace |
| RR-SV-posix-csi-drift | CLAIM_RESIDUAL_RISK_CATALOG | POSIX/CSI drift | default | catalog_guarded | filesystem permission, mount option, CSI, or subPath drift can weaken isolation | path contract, runtime readiness checks, redacted operator inspection, detection signals/metrics | operator_admin | review_at on CSI driver, mount option, or path policy change | docs/contracts/repo-path-contract-v1.md; docs/contracts/workload-mount-binding-v1.md | escalate_to_dedicated_volume when drift cannot be detected or corrected |
| RR-SV-detection-signals | CLAIM_RESIDUAL_RISK_CATALOG | detection signals/metrics | default | catalog_guarded | missed drift or residue can persist without operator-visible evidence | audit outbox, operation inspection, readiness findings, restore reconciliation observations | operator_admin | review_at on audit, readiness, or inspection schema change | docs/contracts/afscp-internal-api-v1.md; docs/contracts/restore-reconciliation-v1.md | escalate_to_dedicated_volume when detection signals cannot cover shared-volume assumptions |
| RR-SV-compensating-controls | CLAIM_RESIDUAL_RISK_CATALOG | compensating controls | default | catalog_guarded | residual runtime risk can outlive a single operation without bounded controls | deny dangerous writes, require operator intervention, redact evidence, keep review point | operator_admin | review_at on operator repair, denial, or redaction contract change | docs/contracts/operator-repair-v1.md; docs/contracts/operation-state-machine-v1.md | escalate_to_dedicated_volume when compensating controls are insufficient |
| RR-SV-dedicated-volume-escalation | CLAIM_RESIDUAL_RISK_CATALOG | dedicated-volume escalation rule | default | catalog_guarded | shared-volume assumptions may be unacceptable for some namespaces or policy regimes | require dedicated volume when isolation evidence, compliance policy, or detection coverage is insufficient | operator_admin | review_at on namespace policy, compliance boundary, or volume class change | docs/contracts/namespace-volume-binding-v1.md; docs/contracts/afscp-internal-api-v1.md | escalate_to_dedicated_volume when a row condition cannot be satisfied |

## Structured Record Shape

A runtime risk record is a product safety record, not release acceptance. It is
valid only when all required fields are present and redacted.

| field | required | rule |
| --- | --- | --- |
| risk_id | yes | predefined risk_id from Catalog Rows; unknown risk_id fails |
| scope | yes | namespace_id, volume_id, repo_id, or operation_id; missing scope fails |
| reason | yes | stable operator reason; missing reason fails |
| evidence_ref | yes | repo-local evidence reference; missing evidence_ref fails |
| actor | yes | actor must be operator_admin; wrong actor fails |
| review_at | conditional | one of review_at or expires_at is required; missing review_at and expires_at fails |
| expires_at | conditional | one of review_at or expires_at is required; expired record fails |
| status_decision | yes | one of recorded, superseded, or expired; subjective states are invalid |

## Audit Shape

Every structured residual risk record emits a redacted audit event named
`residual_risk_recorded` with `actor=operator_admin`, predefined `risk_id`,
scope, reason, evidence_ref, review_at or expires_at, and status_decision.
Audit details must be redacted and must fail closed when they contain raw
storage or credential material such as SecretRef, /srv/afscp, .jvs, payload/,
control/, token, password, credential, or raw command text.
