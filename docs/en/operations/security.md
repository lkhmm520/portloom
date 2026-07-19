# Security hardening

- keep the administration listener on loopback/private network behind HTTPS;
- use at least 32 random bytes for the administrator token;
- use a dedicated non-admin SSH account and key;
- enforce `GatewayPorts no`, remote-only forwarding, no TTY/agent/X11;
- pin and independently verify the SSH host key;
- run the long-lived Server as non-root UID 65532. Managed sshd keeps PID 1 and its listener at UID 0 so it can drop privileges per connection. Both use read-only roots and never mount the Docker socket. The default install applies `cap_drop: ALL`, then adds back only `NET_BIND_SERVICE` for Server and `SETUID`, `SETGID`, and `SYS_CHROOT` for sshd. The installer uses an additional short-lived UID 0 helper only to initialize data-directory ownership; that helper is not a resident service;
- use short-lived, single-use enrollment tokens and remove them after enrollment;
- delete/revoke generated Agent commands that will not be run;
- allow only actual TCP/UDP/custom-web route ports in the cloud firewall. Use `--disable-tcp-edge` on first install when stream publication is not needed. A non-empty bind value in an existing `.env` is preserved, so rerun flags are not a general toggle; verify both `/api/v1/system` and real listening ports after a change;
- protect state, secrets, and backups with 0700/0600 permissions.

Bearer tokens are credentials, not encryption. The control plane requires HTTPS by default; `TM_ALLOW_INSECURE_HTTP` is limited to loopback development endpoints.

Plain HTTP routes are an explicit unencrypted publication mode and must not carry the administration API or other credentials. UDP-over-SSH adds no application authentication; the real service still needs authentication, access control, and rate limiting.

The docs image uses Node/Vite only in the build stage. The final runtime contains static files and unprivileged Nginx, not the Node dependency tree.
