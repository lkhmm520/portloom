# Security hardening

- keep the administration listener on loopback/private network behind HTTPS;
- use at least 32 random bytes for the administrator token;
- use a dedicated non-admin SSH account and key;
- enforce `GatewayPorts no`, remote-only forwarding, no TTY/agent/X11;
- pin and independently verify the SSH host key;
- run non-root, read-only containers with all capabilities dropped;
- never mount the Docker socket;
- use short-lived, single-use enrollment tokens and remove them after enrollment;
- protect state, secrets, and backups with 0700/0600 permissions.

Bearer tokens are credentials, not encryption. The control plane requires HTTPS by default; `TM_ALLOW_INSECURE_HTTP` is limited to loopback development endpoints.

The docs image uses Node/Vite only in the build stage. The final runtime contains static files and unprivileged Nginx, not the Node dependency tree.
