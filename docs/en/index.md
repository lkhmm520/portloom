---
layout: home
hero:
  name: PortLoom
  text: Weave reverse tunnels into manageable infrastructure
  tagline: A self-hosted control plane for NAS and homelab publishing. Keep your existing NPM, TLS, and DNS while adding restricted OpenSSH tunnels, layered health, and rollback-friendly desired state.
  image: { src: /hero-loom.svg, alt: Animated PortLoom tunnel fabric }
  actions:
    - { theme: brand, text: Five-minute quick start, link: /en/guide/quick-start }
    - { theme: alt, text: Install with Docker, link: /en/install/docker }
    - { theme: alt, text: GitHub, link: 'https://github.com/lkhmm520/portloom' }
features:
  - { title: Preserve your ingress, details: 'NPM keeps certificates and HTTPS. Multiple domains share one Host-routing gateway while old paths remain available during migration.' }
  - { title: Observe every layer, details: 'Local reachability, SSH tunnel state, and desired/observed revisions are shown separately.' }
  - { title: Restricted by default, details: 'Dedicated SSH account, pinned host key, loopback forwards, non-root containers, read-only roots, and dropped capabilities.' }
---
<HomeFlow />
