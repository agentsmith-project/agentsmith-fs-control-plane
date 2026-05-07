# Team Review 2026-05-03

Status: historical review summary, superseded by
`docs/GA_PRE_DEV_READINESS.md` for active scope.

The team review accepted AFSCP as a robust functional storage substrate rather
than a caller-specific product layer. AFSCP should manage volumes, namespaces,
repos, repo templates, exports, workload mount bindings, orchestrator mount
plans, JVS execution, operations, logs, and audit. It should not understand
caller job objects, caller catalog objects, caller workspaces, projects,
template catalog UX, or user-facing product permission models.

The active planning rule is stricter than the historical notes: reference
consumer adoption can provide requirements and compatibility feedback, but it
cannot become an AFSCP gate or release dependency.
