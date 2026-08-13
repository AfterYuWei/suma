import ReactECharts from 'echarts-for-react'
import { useEffect, useState } from 'react'
import { useUIStore } from '../../stores/ui'

interface Point { time: string; cpu: number; memory: number; rx: number; tx: number; read: number; write: number; pids: number }

export function StatsView({ nodeID, containerId }: { nodeID: string; containerId: string }) {
  const [points, setPoints] = useState<Point[]>([])
  const theme = useUIStore((state) => state.theme)
  useEffect(() => {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/containers/${containerId}/stats`)
    socket.onmessage = (event) => {
      const value = JSON.parse(String(event.data))
      const cpuDelta = value.cpu_stats?.cpu_usage?.total_usage - value.precpu_stats?.cpu_usage?.total_usage
      const systemDelta = value.cpu_stats?.system_cpu_usage - value.precpu_stats?.system_cpu_usage
      const cpu = systemDelta > 0 ? (cpuDelta / systemDelta) * (value.cpu_stats?.online_cpus || 1) * 100 : 0
      const networks = Object.values(value.networks || {}) as { rx_bytes: number; tx_bytes: number }[]
      const blocks = value.blkio_stats?.io_service_bytes_recursive || []
      const point: Point = { time: new Date().toLocaleTimeString(), cpu, memory: value.memory_stats?.usage || 0, rx: networks.reduce((sum, row) => sum + row.rx_bytes, 0), tx: networks.reduce((sum, row) => sum + row.tx_bytes, 0), read: blocks.filter((row: { op: string }) => row.op === 'read').reduce((sum: number, row: { value: number }) => sum + row.value, 0), write: blocks.filter((row: { op: string }) => row.op === 'write').reduce((sum: number, row: { value: number }) => sum + row.value, 0), pids: value.pids_stats?.current || 0 }
      setPoints((current) => [...current, point].slice(-60))
    }
    return () => socket.close()
  }, [nodeID, containerId])
  const last = points.at(-1)
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  const palette = dark
    ? { label: '#86909c', line: '#303846', split: '#272e3b', cpu: '#4080ff', cpuArea: 'rgba(64,128,255,.12)' }
    : { label: '#86909c', line: '#e5e6eb', split: '#f2f3f5', cpu: '#165dff', cpuArea: 'rgba(22,93,255,.1)' }
  const option = { animation: false, backgroundColor: 'transparent', grid: { left: 42, right: 16, top: 22, bottom: 28 }, tooltip: { trigger: 'axis' }, xAxis: { type: 'category', data: points.map((item) => item.time), axisLabel: { color: palette.label, fontSize: 10 }, axisLine: { lineStyle: { color: palette.line } } }, yAxis: { type: 'value', axisLabel: { color: palette.label, fontSize: 10 }, splitLine: { lineStyle: { color: palette.split } } }, series: [{ name: 'CPU %', type: 'line', showSymbol: false, smooth: true, data: points.map((item) => item.cpu.toFixed(2)), lineStyle: { color: palette.cpu, width: 1.5 }, areaStyle: { color: palette.cpuArea } }] }
  const size = (bytes = 0) => bytes > 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(1)} MB`
  return <div><div className="mb-6 grid grid-cols-2 divide-x divide-border border-y border-border py-4 md:grid-cols-4"><Metric label="CPU" value={`${last?.cpu.toFixed(1) ?? '0.0'}%`} /><Metric label="Memory" value={size(last?.memory)} /><Metric label="Network RX / TX" value={`${size(last?.rx)} / ${size(last?.tx)}`} /><Metric label="PIDs" value={String(last?.pids ?? 0)} /></div><div className="h-[360px]"><ReactECharts option={option} style={{ height: '100%' }} /></div><p className="mt-3 text-xs text-text-subtle">Block read {size(last?.read)} · Block write {size(last?.write)}</p></div>
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="px-5 first:pl-0"><p className="text-[10px] uppercase tracking-wider text-text-subtle">{label}</p><p className="mt-2 text-sm font-medium tabular-nums">{value}</p></div> }
