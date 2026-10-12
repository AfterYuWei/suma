import type { ContainerPort } from './types'

export const portLabel = (port: ContainerPort) => port.public_port
  ? `${port.public_port}→${port.private_port}/${port.type}` : `${port.private_port}/${port.type}`

export const fullPortLabel = (port: ContainerPort) => {
  if (!port.public_port) return portLabel(port)
  const ip = port.ip || '0.0.0.0'
  return `${ip.includes(':') ? `[${ip}]` : ip}:${portLabel(port)}`
}
