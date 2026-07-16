# Production deployment

## Server state

```bash
sudo install -d -o 65532 -g 65532 -m 0700 /opt/portloom/server
openssl rand -hex 32
```

Use the random value for `TM_ADMIN_TOKEN`. Keep administration and Gateway listeners on loopback or a protected private interface.

## Dedicated SSH account

Create a non-administrative account with no login shell and install only Agent public keys:

```bash
sudo useradd --system --create-home --shell /usr/sbin/nologin tunnel
sudo install -d -o tunnel -g tunnel -m 0700 /home/tunnel/.ssh
```

Prefix each `authorized_keys` entry so command sessions, TTYs, user RC files, and non-loopback listeners are denied:

```text
command="/usr/sbin/nologin",no-agent-forwarding,no-X11-forwarding,no-pty,no-user-rc,permitlisten="127.0.0.1:*" ssh-ed25519 AAAA... portloom-agent
```

Install the repository's `sshd_config` fragment:

```text
Match User tunnel
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding remote
    GatewayPorts no
    PermitListen 127.0.0.1:*
    PermitTTY no
    X11Forwarding no
    AllowAgentForwarding no
    PermitUserRC no
    ForceCommand /usr/sbin/nologin
```

Run `sshd -t` before reload. Confirm normal commands and interactive sessions are rejected while the Agent's `ssh -N -R 127.0.0.1:port:target` still works. Do not use `DisableForwarding yes`, which would also disable the required reverse tunnel.

## Keys and host verification

```bash
sudo install -d -o 65532 -g 65532 -m 0700 /opt/portloom/secrets
sudo -u '#65532' ssh-keygen -t ed25519 -a 64 -N '' -f /opt/portloom/secrets/id_ed25519 -C portloom-agent
ssh-keyscan -p 22 tunnel.example.com | sudo tee /opt/portloom/secrets/known_hosts
sudo chown 65532:65532 /opt/portloom/secrets/*
sudo chmod 600 /opt/portloom/secrets/id_ed25519
sudo chmod 644 /opt/portloom/secrets/known_hosts
```

Verify the host fingerprint through a trusted server-side source. Some NAS Docker implementations report matching numeric ownership while denying a non-root bind mount read. Test an actual file open as UID 65532; use an initialized named volume when bind mounts are unreliable.

## Safe migration

Run old and new paths in parallel. Test the Gateway with a preserved Host header, switch one NPM hostname at a time, record the previous upstream, and compare status, content hashes, Range requests, and long-lived protocols. Stop old containers after an observation window; do not immediately delete rollback images or keys.
