# Deploy

Deployment manifests are intentionally not implemented yet.

Expected MVP deployment shape:

- AFSCP API/worker Deployment.
- Internal Service.
- Dedicated ServiceAccount.
- Dedicated Secret references for JuiceFS root credentials.
- Optional WebDAV sidecar or same-image subprocess.
- Network policy allowing trusted calling product services and privileged admin jobs only.
