# Runbooks

Runbooks are GA operational artifacts covered by `scripts/verify-ga-release.sh`,
not optional follow-up notes and not separate role-approval gates.

Initial handoff:

- [local-dev-handoff.md](local-dev-handoff.md)
- [ga-runbooks.md](ga-runbooks.md)

Required repo-local runbook coverage:

- failed repo create
- failed save point create
- failed direct restore to a save point
- direct restore blocked by active or uncertain writer sessions
- writer-session fence stuck or leaked
- JVS doctor failure
- failed template create
- failed template clone
- repo archive blocked or failed
- repo restore-from-archive failed
- repo delete blocked by active or uncertain export/mount sessions
- repo tombstone restore failed
- repo purge denied or failed
- repo purge requested without valid caller approval reference or retention approval
- WebDAV export incident
- WebDAV credential leak and revoke
- stale workload mount lease reconciliation
- workload mount revoke stuck before confirmed-unmounted
- operation reconciliation after crash
- operation marked `operator_intervention_required`
- caller-service authorization denial investigation
- namespace disable and session drain
- JuiceFS Secret rotation
- volume health degradation
- audit outbox lag or replay
- failed migration rollback, if migration tooling is enabled

Each runbook must include:

- symptoms and alerts
- affected resources
- required role
- immediate containment
- recovery decision tree
- audit events to expect
- customer/calling-product impact notes
- verification steps
- rollback or escalation path
- repo-local verification evidence
