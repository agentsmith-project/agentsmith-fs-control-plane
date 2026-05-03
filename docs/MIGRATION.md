# Migration

P0 should not force-migrate existing caller data.

## Strategy

- New repos use AFSCP and namespace volume bindings.
- Legacy resources continue using existing backends.
- Migration is explicit, audited, and reversible until verified.

## Legacy Migration Flow

1. Verify source legacy resource health.
2. Put the source into read-only/maintenance mode, or define an explicit initial-copy plus delta-sync plan.
3. Create target repo through AFSCP under the target namespace.
4. Copy payload data from old backend to target repo path.
5. Record manifest counts, hashes where feasible, source generation, and copy timestamp.
6. Run delta sync until the final cutover window if writes were allowed during initial copy.
7. Freeze source writes for final sync.
8. Initialize JVS repo or import baseline metadata.
9. Create a `migration-baseline` save point.
10. Run `jvs doctor --strict`.
11. Update the calling product's backend reference.
12. Test export and workload mount.
13. Preserve rollback metadata, source generation, and cutover timestamp.
14. Archive old backend only after operator approval.

## Rollback Conditions

Rollback is allowed while the legacy backend is preserved and the calling product can safely point back to the source generation. If writes have occurred on both source and target after cutover, rollback requires an operator decision because AFSCP does not provide merge semantics.

## Do Not

- Do not silently move data when namespace volume binding changes.
- Do not keep ordinary direct JuiceFS mount as the migrated user path.
- Do not delete legacy DB/bucket before validation.
- Do not migrate cross-namespace templates, because they are not supported in P0.
- Do not cut over without either source write freeze or a verified delta-sync/final-lock procedure.
