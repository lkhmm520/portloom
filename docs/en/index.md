---
layout: home
hero:
  name: PortLoom
  text: Stable, reliable, fast self-hosted tunneling
  tagline: Publish NAS, homelab, and internal services through a public host. v0.4 supports HTTPS, HTTP, TCP, UDP, path prefixes, custom public ports, and traffic/resource metrics.
  image: { src: /hero-loom.svg, alt: PortLoom tunnel flow }
  actions:
    - { theme: brand, text: Start installing, link: /en/guide/quick-start }
    - { theme: alt, text: Explore v0.4 routes, link: /en/usage/routes }
features:
  - { title: Two hosts, one Agent command, details: 'Run Server and managed sshd on the public host and only Agent on the NAS. The installer handles keys, host pinning, and the one-time credential.' }
  - { title: Four protocols and flexible routing, details: 'Create HTTPS, HTTP, TCP, and UDP routes in the WebUI. Web routes support path prefixes, prefix stripping, and custom public ports.' }
  - { title: Status, traffic, and resources, details: 'Inspect Local, Tunnel, and Public separately; Dashboard shows 60-minute traffic and resources, while the metrics API also exposes per-route counters.' }
---
<HomeFlow />
