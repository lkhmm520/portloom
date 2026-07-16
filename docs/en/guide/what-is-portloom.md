# What is PortLoom?

PortLoom is a reverse SSH tunnel control plane for NAS, homelab, and small self-hosted environments. It does not replace Nginx Proxy Manager (NPM), DNS, or OpenSSH. It turns tunnel configuration previously scattered across scripts and Compose files into observable desired state.

## What it solves

A plain `ssh -R` is reliable, but larger installations struggle with mapping ownership, false-positive process checks, unsafe root keys, and undocumented rollback. PortLoom adds:

- a Web console and Bearer-protected administration API;
- one-time, expiring agent enrollment tokens;
- client heartbeat and desired/observed revisions;
- an HTTP `Host` gateway shared by many domains;
- separate local, tunnel, and convergence health layers;
- SQLite persistence with no external database.

## What it does not do

- issue certificates, modify DNS, or call the NPM API;
- start or configure the host SSH daemon;
- bind reverse forwards to public interfaces;
- provide an active/active Server cluster;
- automatically expose raw TCP routes in the current release.

PortLoom is most useful once you have multiple domains, clients, or tunnels to operate. For one temporary port, a direct `ssh -R` may still be simpler.
