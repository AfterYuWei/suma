import ReactECharts from 'echarts-for-react'
import { Card, CardContent } from '../../components/ui/card'
import { useEffect, useState } from 'react'
import { useUIStore } from '../../stores/ui'
import { demoMode, subscribeDemoStream } from '../../lib/api'

interface Point { time: string; cpu: number; memory: number; rx: number; tx: number; read: number; write: number; pids: number }

export function StatsView({ nodeID, containerId }: { nodeID: string; containerId: string }) {
  const [points, setPoints] = useState<Point[]>([])
  useUIStore((state) => state.theme)
  useEffect(() => {
    const consume = (payload: string) => {
      const value = JSON.parse(payload)
      const cpuDelta = value.cpu_stats?.cpu_usage?.total_usage - value.precpu_stats?.cpu_usage?.total_usage
      const systemDelta = value.cpu_stats?.system_cpu_usage - value.precpu_stats?.system_cpu_usage
      const cpu = systemDelta > 0 ? (cpuDelta / systemDelta) * (value.cpu_stats?.online_cpus || 1) * 100 : 0
      const networks = Object.values(value.networks || {}) as { rx_bytes: number; tx_bytes: number }[]
      const blocks = value.blkio_stats?.io_service_bytes_recursive || []
      const point: Point = { time: new Date().toLocaleTimeString(), cpu, memory: value.memory_stats?.usage || 0, rx: networks.reduce((sum, row) => sum + row.rx_bytes, 0), tx: networks.reduce((sum, row) => sum + row.tx_bytes, 0), read: blocks.filter((row: { op: string }) => row.op === 'read').reduce((sum: number, row: { value: number }) => sum + row.value, 0), write: blocks.filter((row: { op: string }) => row.op === 'write').reduce((sum: number, row: { value: number }) => sum + row.value, 0), pids: value.pids_stats?.current || 0 }
      setPoints((current) => [...current, point].slice(-60))
    }
    if (demoMode) {
      let disposed = false
      let cleanup: () => void = () => undefined
      void subscribeDemoStream('stats', consume).then((next) => { if (disposed) next(); else cleanup = next })
      return () => { disposed = true; cleanup() }
    }
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/containers/${containerId}/stats`)
    socket.onmessage = (event) => consume(String(event.data))
    return () => socket.close()
  }, [nodeID, containerId])
  const last = points.at(-1)
  const styles = getComputedStyle(document.documentElement)
  const palette = {
    label: styles.getPropertyValue('--muted-foreground'),
    line: styles.getPropertyValue('--border'),
    split: styles.getPropertyValue('--border'),
    cpu: styles.getPropertyValue('--chart-1'),
    cpuArea: styles.getPropertyValue('--primary'),
  }
  const option = { animation: false, backgroundColor: 'transparent', grid: { left: 42, right: 16, top: 22, bottom: 28 }, tooltip: { trigger: 'axis' }, xAxis: { type: 'category', data: points.map((item) => item.time), axisLabel: { color: palette.label, fontSize: 10 }, axisLine: { lineStyle: { color: palette.line } } }, yAxis: { type: 'value', axisLabel: { color: palette.label, fontSize: 10 }, splitLine: { lineStyle: { color: palette.split } } }, series: [{ name: 'CPU %', type: 'line', showSymbol: false, smooth: true, data: points.map((item) => item.cpu.toFixed(2)), lineStyle: { color: palette.cpu, width: 1.5 }, areaStyle: { color: palette.cpuArea } }] }
  const size = (bytes = 0) => bytes > 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(1)} MB`
  return <Card>
    <CardContent className="flex flex-col gap-3">
      <dl className="grid grid-cols-2 gap-x-8 gap-y-2.5 text-sm lg:grid-cols-4">
        <div><dt className="text-xs text-muted-foreground">CPU</dt><dd>{last?.cpu.toFixed(1) ?? '0.0'}%</dd></div>
        <div><dt className="text-xs text-muted-foreground">Memory</dt><dd>{size(last?.memory)}</dd></div>
        <div><dt className="text-xs text-muted-foreground">Network RX / TX</dt><dd className="break-all">{size(last?.rx)} / {size(last?.tx)}</dd></div>
        <div><dt className="text-xs text-muted-foreground">PIDs</dt><dd>{last?.pids ?? 0}</dd></div>
      </dl>
      <div className="h-[360px]"><ReactECharts option={option} style={{ height: '100%' }} /></div>
      <p className="text-xs text-muted-foreground">Block read {size(last?.read)} · Block write {size(last?.write)}</p>
    </CardContent>
  </Card>
}
