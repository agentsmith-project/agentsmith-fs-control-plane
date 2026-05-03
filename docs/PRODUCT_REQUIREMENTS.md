# Product Requirements

## Problem

The current AgentSmith file library model can create JuiceFS-backed libraries, but using a separate JuiceFS metadata DB and bucket for every notebook task is too heavy. It creates operational overhead, credential sprawl, and poor template/clone ergonomics.

AgentSmith needs a workspace-aware file library control plane that can use one shared JuiceFS filesystem by default while preserving user authorization, sandbox isolation, desktop access, and JVS version management.

## Users

- Workspace admins: configure storage profiles for tenant workspaces.
- AgentSmith users: access file libraries from notebooks, tasks, Web, and Desktop.
- Agent workloads: read and write persistent workspace files through sandbox mounts.
- Platform operators: operate JuiceFS, JVS, exports, and migrations safely.

## Core Requirements

- Admins can configure a workspace storage profile when creating or managing an AgentSmith workspace.
- Different AgentSmith workspaces can use different AFSCP instances, storage pools, filesystem IDs, AFSCP-computed workspace roots, quota defaults, and export policies.
- New file libraries default to a shared JuiceFS filesystem/storage pool selected by the workspace profile.
- File libraries are represented as repos with stable IDs and controlled repo paths.
- AFSCP runs JVS operations for save points, restore, history, repo clone, and repo lifecycle.
- Users can save a notebook task result as a workspace-scoped template.
- Users in the same AgentSmith workspace can clone a template into their own independent repo.
- Saving a template clones the source task/file-library repo into a template repo. Using a template clones that template repo into a new target file-library repo.
- Cross-workspace template sharing and clone are not allowed.
- User PCs can access authorized repos through controlled exports, initially WebDAV.
- Sandboxes can mount authorized repos through controlled subdirectory mounts.
- Ordinary users, Desktop, and sandbox workloads must not see JuiceFS root credentials.
- Ordinary concurrent reads and writes are allowed. AgentSmith does not enforce single-writer behavior.
- Version merge and conflict resolution are out of scope.

## Non-Goals

- Real-time collaboration.
- Git remote, push, pull, origin, or merge workflows.
- A global template marketplace.
- Cross-workspace repo template sharing.
- Per-file ACL management UI.
- Per-user NAS account management.
- Raw JuiceFS direct mount for ordinary users.
- Automated legacy migration in P0.
- Multi-region replication, billing, tiering, or retention automation in P0.

## MVP Acceptance Criteria

1. Admin can configure a workspace storage profile.
2. New file libraries under that workspace are created through AFSCP.
3. New file libraries use the workspace profile's storage pool instead of creating a new JuiceFS DB/bucket.
4. AFSCP initializes a JVS repo for each new file library.
5. Sandbox binding v2 mounts the repo subdirectory without exposing JuiceFS root credentials to the workload.
6. Desktop/Web receives `ExportAccess` rather than JuiceFS direct mount credentials.
7. JVS save point, history, and restore flow work through AFSCP.
8. A notebook task can be saved as a workspace template.
9. A same-workspace user can clone a template into an independent repo with a new JVS repo identity.
10. Cross-workspace template clone is rejected.
11. WebDAV/export cannot access `.jvs`.
12. All mutating operations produce operation records and audit events.
