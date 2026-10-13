import type { ContainerPort } from './types'

export const portLabel = (port: ContainerPort) => `${port.public_port ? `${port.public_port}:` : ''}${port.private_port}${port.type === 'tcp' ? '' : `/${port.type}`}`

export const fullPortLabel = (port: ContainerPort) => {
  if (!port.public_port) return `${port.private_port}/${port.type}`
  const ip = port.ip || '0.0.0.0'
  return `${ip.includes(':') ? `[${ip}]` : ip}:${port.public_port}:${port.private_port}/${port.type}`
}
