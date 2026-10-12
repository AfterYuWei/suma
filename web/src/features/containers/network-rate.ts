import type { ContainerMetrics } from './types'

export type NetworkRate = { status: 'sampling' | 'unavailable' } | { status: 'ready'; receive: number; transmit: number }

export function networkRate(current: ContainerMetrics, previous?: ContainerMetrics): NetworkRate {
  if (!current.available) return { status: 'unavailable' }
  const sampled = Date.parse(current.sampled_at ?? '')
  const started = Date.parse(current.started_at ?? '')
  if (!Number.isFinite(sampled) || !Number.isFinite(started) || started > sampled) return { status: 'unavailable' }
  if (!previous?.available || previous.started_at !== current.started_at) return { status: 'sampling' }
  const seconds = (sampled - Date.parse(previous.sampled_at ?? '')) / 1000
  if (!Number.isFinite(seconds) || seconds <= 0 || seconds > 15) return { status: 'sampling' }
  const receive = current.network_rx_bytes - previous.network_rx_bytes
  const transmit = current.network_tx_bytes - previous.network_tx_bytes
  if (!Number.isFinite(receive) || !Number.isFinite(transmit) || receive < 0 || transmit < 0) return { status: 'sampling' }
  return { status: 'ready', receive: receive / seconds, transmit: transmit / seconds }
}

export function rateLabel(bytes: number): string {
  if (bytes < 1024) return `${Math.round(bytes)} B/s`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB/s`
  return `${(bytes / 1024 ** 2).toFixed(1)} MB/s`
}
