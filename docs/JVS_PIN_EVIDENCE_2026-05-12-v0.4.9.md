# JVS Pin Evidence 2026-05-12 v0.4.9

Status: historical release evidence. The current pre-GA active AFSCP JVS pin is
`docs/JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-16.md`.

This artifact is retained only as historical context for the old
external-control-root runner baseline. It is not the active
save/list/restore/status/doctor contract and must not be used to enable AFSCP
direct restore.

## Historical Release

- Release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.9
- Asset: `jvs-linux-amd64`
- SHA-256 from `SHA256SUMS`:
  `0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944`

The checksum was observed from:

```bash
curl -fsSL https://github.com/agentsmith-project/jvs/releases/download/v0.4.9/SHA256SUMS | rg 'jvs-linux-amd64'
```

Observed:

```text
0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944  jvs-linux-amd64
```

## Historical Boundary

The `v0.4.9` release did not provide the active AFSCP direct contract:

```bash
jvs afscp --control-root <control> --home <home> <save|list|restore|status|doctor> --json
```

AFSCP active direct save/list/restore/status/doctor must use the current local
direct-capable pin until a formal JVS release replaces it.

Historical evidence remains useful for understanding earlier repo init,
external-control-root, repo clone, and strict doctor work, but it is not active
release-pin evidence for the current pre-GA direct contract.
