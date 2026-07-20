---
layout: home
hero:
  name: PortLoom
  text: Stable, reliable, fast self-hosted tunneling
  tagline: Publish NAS, homelab, and internal services through a public host. v0.4 supports HTTPS, HTTP, TCP, UDP, path prefixes, custom public ports, and traffic/resource metrics.
  image: { src: /hero-loom.svg, alt: PortLoom tunnel flow }
  actions:
    - { theme: brand, text: Install with Compose, link: /en/guide/compose-install }
    - { theme: alt, text: Five-minute quick start, link: /en/guide/quick-start }
features:
  - { title: Conventional compose.yml template, details: 'Download compose.yml and its env template, then edit only the hostname and administrator token; the automated installer remains optional.' }
  - { title: Four protocols and flexible routing, details: 'Create HTTPS, HTTP, TCP, and UDP routes in the WebUI. Web routes support path prefixes, prefix stripping, and custom public ports.' }
  - { title: Status, traffic, and resources, details: 'Inspect Local, Tunnel, and Public separately; Dashboard shows 60-minute traffic and resources, while the metrics API also exposes per-route counters.' }
---
<HomeFlow />
