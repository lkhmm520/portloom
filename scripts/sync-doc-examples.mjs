import { chmod, copyFile, mkdir, rm } from 'node:fs/promises'

const source = new URL('../examples/', import.meta.url)
const destination = new URL('../docs/public/examples/', import.meta.url)
const publicFiles = [
  'compose.yml',
  'compose.env.example',
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
  const target = new URL(name, destination)
  await copyFile(new URL(name, source), target)
  await chmod(target, 0o644)
}
