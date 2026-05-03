# Migration

P0 should not force-migrate existing caller data.

## Strategy

- New repos use AFSCP and namespace volume bindings.
- Legacy resources continue using existing backends.
- Migration is explicit, audited, and reversible until verified.

## Legacy Migration Flow

1. Verify source legacy resource health.
2. Create target repo through AFSCP under the target namespace.
3. Copy payload data from old backend to target repo path.
4. Initialize JVS repo or import baseline metadata.
5. Create a `migration-baseline` save point.
6. Run `jvs doctor --strict`.
7. Update the calling product's backend reference.
8. Test export and workload mount.
9. Preserve rollback metadata.
10. Archive old backend only after operator approval.

## Do Not

- Do not silently move data when namespace volume binding changes.
- Do not keep ordinary direct JuiceFS mount as the migrated user path.
- Do not delete legacy DB/bucket before validation.
- Do not migrate cross-namespace templates, because they are not supported in P0.
