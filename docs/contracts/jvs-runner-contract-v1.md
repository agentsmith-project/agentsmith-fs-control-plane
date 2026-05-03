# Contract: JVS Runner V1

Status: draft

AFSCP integrates with JVS through CLI JSON output in P0.

## Rules

- AFSCP invokes JVS with canonical repo paths.
- JVS output is captured and stored with operation records.
- Each mutating JVS command runs under a per-repo operation lock.
- AFSCP maps JVS exit codes and JSON errors into stable internal errors.
- `jvs doctor --strict` is part of repo create/restore/clone validation.

## Required Commands

- init
- save
- history/list
- restore preview
- restore run
- repo clone
- repo lifecycle
- doctor
