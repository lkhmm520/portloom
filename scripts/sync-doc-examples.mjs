import { copyFile, mkdir, rm } from 'node:fs/promises'

const source = new URL('../examples/', import.meta.url)
const destination = new URL('../docs/public/examples/', import.meta.url)
const publicFiles = [
  'docker-compose.server.yml',
  'server.env.example',
  'docker-compose.agent.yml',
  'agent.env.example',
  'docker-compose.dual-agent.yml',
  'agent-web.env.example',
  'agent-media.env.example',
  'sshd_config.portloom.conf'
]

await rm(destination, { recursive: true, force: true })
await mkdir(destination, { recursive: true })
for (const name of publicFiles) {
  await copyFile(new URL(name, source), new URL(name, destination))
}
