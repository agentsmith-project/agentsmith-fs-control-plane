# Migration

P0 should not force-migrate existing file libraries.

## Strategy

- New libraries use AFSCP and the workspace storage profile.
- Legacy libraries continue using existing backends.
- Migration is explicit, audited, and reversible until verified.

## Legacy Migration Flow

1. Verify source legacy file library health.
2. Create target repo through AFSCP under the workspace storage profile.
3. Copy payload data from old backend to target payload directory.
4. Initialize JVS repo or import baseline metadata.
5. Create a `migration-baseline` save point.
6. Run `jvs doctor --strict`.
7. Update AgentSmith file library backend record.
8. Test Desktop/Web export and sandbox mount.
9. Preserve rollback metadata.
10. Archive old backend only after operator approval.

## Do Not

- Do not silently move data when workspace storage profile changes.
- Do not keep ordinary Desktop direct mount as the migrated path.
- Do not delete legacy DB/bucket before validation.
- Do not migrate cross-workspace templates, because they are not supported.
