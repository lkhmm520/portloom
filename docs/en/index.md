---
layout: home
hero:
  name: PortLoom
  text: Stable, reliable, fast tunnel proxying
  tagline: Publish NAS, homelab, and internal web services through a public host. Run Server on the public Docker host and Agent inside the private network. The Agent makes the encrypted outbound connection, so the private network needs no inbound port.
  image: { src: /hero-loom.svg, alt: PortLoom tunnel connection diagram }
  actions:
    - { theme: brand, text: Start installing, link: /en/guide/quick-start }
    - { theme: alt, text: How it works, link: /en/guide/what-is-portloom }
features:
  - { title: Two hosts, one install command, details: 'Install Server on the public Docker host, then copy one command from the WebUI to the NAS. The installer handles keys, host identity, and enrollment.' }
  - { title: Manage routes in the WebUI, details: 'Choose an Agent and enter the internal address, port, and public hostname. Adding a service does not require editing SSH commands.' }
  - { title: Safe defaults are included, details: 'Agents connect outbound. The managed SSH account has no shell and can create loopback reverse forwards only. Containers never mount the Docker socket.' }
---
<HomeFlow />
