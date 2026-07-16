---
search: false
---

# PortLoom MVP Work Plan

## Goal
Build a Dockerized server and NAS agent that manage OpenSSH reverse tunnels through a Web UI. Public TLS remains on the DMIT NPM; FRP is not used.

## Acceptance criteria
- Server and agent images build and start.
- Tests pass in a clean Go container.
- SQLite state persists.
- Admin creates one-time enrollment tokens.
- Agent enrolls, heartbeats and receives desired route revisions.
- Route CRUD allocates collision-free DMIT loopback ports.
- Dynamic gateway routes HTTP by Host.
- Agent reconciles safe OpenSSH remote forwards.
- UI manages clients, tokens and routes.

## Checklist
- [x] T1 Workspace and plan
- [ ] T2 Domain validation and allocator
- [ ] T3 SQLite store
- [ ] T4 Auth, enrollment and heartbeat API
- [ ] T5 Route API and revisions
- [ ] T6 Host gateway
- [ ] T7 Agent and SSH reconciliation
- [ ] T8 Web UI
- [ ] T9 Docker/Compose/docs
- [ ] T10 Integration and review

## Safety
Development never modifies current NPM, DNS, containers, SSH tunnels or public ports. Test services use isolated loopback ports.
