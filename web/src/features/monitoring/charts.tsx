import { useEffect, useId, useRef } from 'react'
import { init, use as register, connect, disconnect, type ECharts } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, MarkLineComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsOption } from 'echarts'
import { popoverSurfaceClassName } from '../../components/ui/popover-styles'
import { useUIStore } from '../../stores/ui'
import { useDateTime } from '../../lib/time-zone'
import { metricLabel, metricValue } from './range'
import type { Field, MonitorEvent, Series } from './types'

register([LineChart, GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, MarkLineComponent, CanvasRenderer])
const groups: Field[][] = [['cpu'], ['memory'], ['network_rx', 'network_tx'], ['block_read', 'block_write'], ['pids']]
const resolveColor = (token: string) => {
  const canvas = document.createElement('canvas'); canvas.width = canvas.height = 1
  const context = canvas.getContext('2d')!
  context.fillStyle = getComputedStyle(document.documentElement).getPropertyValue(token).trim()
  context.fillRect(0, 0, 1, 1)
  const [r, g, b, a] = context.getImageData(0, 0, 1, 1).data
  return 'rgba(' + [r, g, b, a / 255].join(',') + ')'
}

export function MonitoringCharts({ series, compact = false, relative = false, events = [], focusedEvent, onZoom, onEvent }: { series: Series[]; compact?: boolean; relative?: boolean; events?: MonitorEvent[]; focusedEvent?: string; onZoom?: (since: number, until: number) => void; onEvent?: (event: MonitorEvent) => void }) {
  const group = useId()
  const refs = useRef<(HTMLDivElement | null)[]>([])
  const charts = useRef<ECharts[]>([])
  const handlers = useRef({ onZoom, onEvent }); handlers.current = { onZoom, onEvent }
  const dark = useUIStore(s => s.resolvedDark), zh = useUIStore(s => s.language) === 'zh-CN'
  const { formatDate, formatTime } = useDateTime()
  useEffect(() => {
    charts.current = refs.current.map(element => init(element!))
    for (const chart of charts.current) {
      chart.group = group
      chart.on('datazoom', () => {
        const options = chart.getOption()
        const zoom = (options.dataZoom as { startValue?: number; endValue?: number; start?: number; end?: number }[])?.[0]
        const axis=(options.xAxis as {min:number;max:number}[])?.[0]
        if(!zoom||!axis)return
        const start=zoom.startValue??axis.min+(axis.max-axis.min)*(zoom.start??0)/100
        const end=zoom.endValue??axis.min+(axis.max-axis.min)*(zoom.end??100)/100
        if(end>start&&end-start<axis.max-axis.min-1)handlers.current.onZoom?.(start,end)
      })
      chart.on('click', params => {
        if (params.componentType === 'markLine' && params.name) {
          const event = eventsRef.current.find(e => e.id === params.name)
          if (event) handlers.current.onEvent?.(event)
        }
      })
    }
    connect(group)
    const sizes = refs.current.map(element => ({width: element!.clientWidth, height: element!.clientHeight}))
    const resize = new ResizeObserver(() => charts.current.forEach((chart,index) => {const element=refs.current[index];if(!element||chart.isDisposed()||!sizes[index])return;if(sizes[index].width===element.clientWidth&&sizes[index].height===element.clientHeight)return;sizes[index]={width:element.clientWidth,height:element.clientHeight};chart.resize()}))
    refs.current.forEach(element => element && resize.observe(element))
    return () => { resize.disconnect(); disconnect(group); charts.current.forEach(chart => chart.dispose()); charts.current = [] }
  }, [compact, group])
  const eventsRef = useRef(events); eventsRef.current = events
  useEffect(() => {
    const colors = [1, 2, 3, 4, 5].map(n => resolveColor('--chart-' + n))
    const label = resolveColor('--muted-foreground'), border = resolveColor('--border')
    const start = series[0] ? Date.parse(series[0].target.since) : Date.now() - 3600_000
    const end = series[0] ? Date.parse(series[0].target.until) : Date.now()
    charts.current.forEach((chart, index) => {
      const keys = groups[index]
      const grid = { left: 55, right: 16, top: compact ? 10 : 35, bottom: compact ? 26 : 50 }
      const plot: NonNullable<EChartsOption['series']> = []
      const tooltipMeta = new Map<string, { field: Field; offset: number; identity: string; caption: string; color: string }>()
      series.forEach((result, s) => {
        const offset = relative ? start - Date.parse(result.target.since) : 0
        for (const [k, field] of keys.entries()) {
          const scale = field === 'memory' ? 1024 ** 2 : field === 'cpu' || field === 'pids' ? 1 : 1024
          const color = colors[(series.length > 1 ? s : index + k) % colors.length]
          const legend = result.instance.name + (series.length > 1 ? ' · ' + result.instance.node_name + ' · ' + (s + 1) : '')
          for (const statistic of ['avg', ...(compact || keys.length > 1 || field === 'pids' ? [] : ['max'])] as ('avg' | 'max')[]) {
            const caption = metricLabel(field, zh) + (statistic === 'max' ? zh ? ' 峰值' : ' peak' : '')
            const name = legend + ' · ' + caption
            tooltipMeta.set(name, { field, offset, identity: legend, caption, color })
            const data: [number, number | null][] = []
            let last: Series['points'][number] | undefined
            for (const point of result.points) {
              const time = Date.parse(point.time) + offset
              if (last && (last.segment !== point.segment || Date.parse(point.time) - Date.parse(last.time) > result.step_seconds * 2500)) data.push([time - 1, null])
              const value = point.values[field]?.[statistic]
              data.push([time, value == null ? null : value / scale]); last = point
            }
            plot.push({
              id: String(s) + field + statistic, name, type: 'line', data,
              showSymbol: data.filter(p => p[1] != null).length < 2, symbolSize: 4, connectNulls: false,
              lineStyle: { width: statistic === 'max' ? 1 : 2, color, type: statistic === 'max' || k === 1 ? 'dashed' : 'solid' },
              itemStyle: { color },
              emphasis: { scale: false, focus: 'none', lineStyle: { width: statistic === 'max' ? 1 : 2, color }, itemStyle: { color } },
              markLine: s === 0 ? { symbol: 'none', label: { show: false }, data: events.flatMap(e => series.flatMap(result => { const time=Date.parse(e.time);if(e.instance_id!==result.instance.id||time<Date.parse(result.target.since)||time>Date.parse(result.target.until))return [];return [{name:e.id,xAxis:time+(relative?start-Date.parse(result.target.since):0),lineStyle:{width:e.id===focusedEvent?2:1,color:e.action==='oom'||e.health_status==='unhealthy'?colors[4]:border,type:'dotted' as const}}] })) } : undefined,
            })
          }
          if (field === 'memory' && !compact) plot.push({ id: 'limit' + s, name: legend + ' · ' + metricLabel('memory_limit', zh), type: 'line', showSymbol: false, connectNulls: false, lineStyle: { type: 'dotted', width: 1, color: colors[s % colors.length] }, data: result.points.map(p => [Date.parse(p.time) + offset, p.values.memory_limit?.avg ? p.values.memory_limit.avg / scale : null]) })
        }
      })
      chart.setOption({
        animation: false,
        textStyle: { fontFamily: getComputedStyle(document.body).fontFamily, color: label },
        legend: compact ? { show: false } : { type: 'scroll', top: 0, textStyle: { color: label, fontSize: 10 } },
        grid,
        xAxis: { type: 'time', min: start, max: end, splitNumber: compact ? 3 : 8, minInterval: compact ? 600_000 : Math.max(1000,(end-start)/12), axisLabel: { color: label, hideOverlap: true, fontSize: 10, formatter: (time: number) => formatTime(time) }, axisLine: { lineStyle: { color: border } } },
        yAxis: { type: 'value', min: 0, splitNumber: compact ? 2 : 5, name: keys[0] === 'cpu' ? '%' : keys[0] === 'memory' ? 'MiB' : keys[0] === 'pids' ? '' : 'KiB/s', nameTextStyle: { color: label }, axisLabel: { color: label, fontSize: 10, hideOverlap: true }, splitLine: { lineStyle: { color: border } } },
        tooltip: { trigger: 'axis', confine: true, transitionDuration: 0, axisPointer: { animation: false }, enterable: true, position: (point, _params, _dom, _rect, size) => {
          const [width, height] = size.contentSize
          const right = size.viewSize[0] - grid.right, bottom = size.viewSize[1] - grid.bottom
          const x = point[0] + width + 12 <= right ? point[0] + 12 : point[0] - width - 12
          const y = point[1] + height + 12 <= bottom ? point[1] + 12 : point[1] - height - 12
          return [Math.max(grid.left, Math.min(x, right - width)), Math.max(grid.top, Math.min(y, bottom - height))]
        }, padding: 0, borderWidth: 0, backgroundColor: 'transparent', extraCssText: 'box-shadow:none', formatter: params => {
          const items = Array.isArray(params) ? params : [params]
          const box = document.createElement('div'); box.dataset.slot = 'chart-tooltip'; box.className = popoverSurfaceClassName + ' space-y-1 overflow-y-auto whitespace-normal p-2.5 text-xs'
          box.style.maxWidth = Math.max(0, Math.min(320, chart.getWidth() - grid.left - grid.right - 8)) + 'px'
          box.style.maxHeight = Math.max(0, chart.getHeight() - grid.top - grid.bottom - 8) + 'px'
          for (const item of items) {
            const value = item.value as [number, number | null]
            const meta = tooltipMeta.get(item.seriesName ?? '')
            if (!meta || !Array.isArray(value)) continue
            const scale = meta.field === 'memory' ? 1024 ** 2 : meta.field === 'cpu' || meta.field === 'pids' ? 1 : 1024
            const row = document.createElement('div')
            row.className = 'space-y-1'
            const header = document.createElement('div')
            header.className = 'flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1'
            const identity = document.createElement('span')
            identity.className = 'min-w-0 max-w-full flex-auto [overflow-wrap:anywhere]'
            const marker = document.createElement('span')
            marker.className = 'mr-1.5 inline-block size-1.5 rounded-full align-middle'
            marker.style.backgroundColor = meta.color
            marker.setAttribute('aria-hidden', 'true')
            identity.append(marker, document.createTextNode(meta.identity))
            const metric = document.createElement('span')
            metric.dataset.slot = 'chart-tooltip-value'
            metric.className = 'shrink-0 whitespace-nowrap font-mono tabular-nums'
            metric.textContent = meta.caption + ': ' + metricValue(meta.field, value[1] == null ? null : value[1] * scale)
            header.append(identity, document.createTextNode(' '), metric)
            const timestamp = new Date(value[0] - meta.offset).toISOString()
            const time = document.createElement('time')
            time.dateTime = timestamp
            time.className = 'flex flex-wrap gap-x-1 text-muted-foreground'
            const date = document.createElement('span'), clock = document.createElement('span')
            date.className = clock.className = 'whitespace-nowrap'
            date.textContent = formatDate(timestamp)
            clock.textContent = formatTime(timestamp)
            time.append(date, document.createTextNode(' '), clock)
            row.append(header, time)
            box.append(row)
          }
          return box
        } },
        dataZoom: compact ? [] : [{ type: 'slider', realtime: false, bottom: 5, height: 16, borderColor: border, textStyle: { color: label }, filterMode: 'none' }],
        series: plot,
      } satisfies EChartsOption, { notMerge: true, lazyUpdate: true })
    })
  }, [series, compact, relative, events, focusedEvent, dark, zh, formatDate, formatTime])
  return <div className={compact ? 'space-y-4' : 'grid grid-cols-1 gap-x-6 gap-y-6 md:grid-cols-2'}>{groups.slice(0, compact ? 2 : groups.length).map((keys, i) => <section key={keys[0]} className="min-w-0"><h3 className="mb-1 text-xs font-medium">{keys.map(key => metricLabel(key, zh)).join(' / ')}</h3><div ref={element => { refs.current[i] = element }} role="img" aria-label={keys.map(key => metricLabel(key, zh)).join(' / ') + (zh ? '趋势' : ' trend')} className={compact ? 'h-28' : 'h-64'} /></section>)}</div>
}
