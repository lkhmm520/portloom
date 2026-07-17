---
search: false
---

# PortLoom MVP Work Plan

## Goal
Build a two-host Docker deployment that manages OpenSSH reverse tunnels through a WebUI. The default public host includes managed sshd and Caddy; existing ingress is optional, and FRP is not used.

## Acceptance criteria
- Server, Agent, managed sshd, and docs images build and start.
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
Development never modifies production ingress, DNS, containers, SSH tunnels, or public ports. Test services use isolated loopback ports.
