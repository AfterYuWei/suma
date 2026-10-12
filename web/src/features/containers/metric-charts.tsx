import { useEffect, useMemo, useRef } from 'react'
import { init, use as registerCharts, type ECharts } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, MarkLineComponent, TitleComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { TooltipComponentFormatterCallbackParams, TooltipComponentPositionCallback } from 'echarts'
import { popoverSurfaceClassName } from '../../components/ui/popover-styles'
import { useUIStore } from '../../stores/ui'
import { useDateTime } from '../../lib/time-zone'
import type { MetricPoint } from './types'

registerCharts([LineChart, GridComponent, MarkLineComponent, TitleComponent, TooltipComponent, CanvasRenderer])
const emptyPoints: MetricPoint[] = []

// Canvas accepts modern CSS colors; zrender's emphasis/gradient math requires
// RGB. Resolve through the browser rather than approximating theme tokens.
function chartColors(values: string[]) {
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = 1
  const context = canvas.getContext('2d', { willReadFrequently: true })
  if (!context) return values
  return values.map(value => {
    context.clearRect(0, 0, 1, 1)
    context.fillStyle = value
    context.fillRect(0, 0, 1, 1)
    const [r, g, b, a] = context.getImageData(0, 0, 1, 1).data
    return `rgba(${r},${g},${b},${a / 255})`
  })
}

function metricTooltip(params: TooltipComponentFormatterCallbackParams, name: string, color: string, suffix: string, formatDateTime: (time: number) => string) {
  const sample = Array.isArray(params) ? params[0] : params
  if (!sample || !Array.isArray(sample.value)) return ''
  const [time, value] = sample.value
  const popup = document.createElement('div')
  popup.dataset.slot = 'chart-tooltip'
  popup.className = `${popoverSurfaceClassName} space-y-2 p-2.5 text-xs leading-4`
  const date = document.createElement('div')
  date.className = 'whitespace-nowrap text-muted-foreground'
  date.textContent = typeof time === 'number' ? formatDateTime(time) : '—'
  const row = document.createElement('div')
  row.className = 'flex items-center justify-between gap-4'
  const label = document.createElement('span')
  label.className = 'flex items-center gap-1.5 text-muted-foreground'
  const dot = document.createElement('span')
  dot.className = 'size-1.5 shrink-0 rounded-full'
  dot.style.backgroundColor = color
  label.append(dot, document.createTextNode(name))
  const number = document.createElement('span')
  number.className = 'font-mono font-medium tabular-nums'
  number.textContent = typeof value === 'number' ? `${value.toFixed(1)}${suffix}` : '—'
  row.append(label, number)
  popup.append(date, row)
  return popup
}

export function MetricCharts({ points, minutes, compact = false, sparkline = false }: { points: MetricPoint[]; minutes: number; compact?: boolean; sparkline?: boolean }) {
  useUIStore(state => state.resolvedDark)
  const zh = useUIStore(state => state.language) === 'zh-CN'
  const { formatTime, formatDateTime } = useDateTime()
  const elements = useRef<(HTMLDivElement | null)[]>([])
  const charts = useRef<ECharts[]>([])
  const values = useMemo(() => {
    if (!points.length) return emptyPoints
    const end = points.at(-1)!.time
    return points.filter(point => point.time >= end - minutes * 60_000)
  }, [points, minutes])
  const styles = getComputedStyle(document.documentElement)
  const rawLabel = styles.getPropertyValue('--muted-foreground').trim()
  const rawBorder = styles.getPropertyValue('--border').trim()
  const rawCPU = styles.getPropertyValue(sparkline ? '--foreground' : '--chart-1').trim()
  const rawMemory = styles.getPropertyValue(sparkline ? '--muted-foreground' : '--chart-2').trim()
  const [label, border, cpuColor, memoryColor] = useMemo(() => chartColors([rawLabel, rawBorder, rawCPU, rawMemory]), [rawLabel, rawBorder, rawCPU, rawMemory])

  useEffect(() => {
    const instances = elements.current.map(element => init(element!))
    charts.current = instances
    const sizes = elements.current.map(element => ({ width: element!.clientWidth, height: element!.clientHeight }))
    const resize = new ResizeObserver(() => {
      instances.forEach((chart, index) => {
        const element = elements.current[index]
        if (!element || chart.isDisposed()) return
        const width = element.clientWidth, height = element.clientHeight
        if (sizes[index].width === width && sizes[index].height === height) return
        sizes[index] = { width, height }
        chart.resize({ width, height })
      })
    })
    for (const element of elements.current) if (element) resize.observe(element)
    return () => { resize.disconnect(); charts.current = []; instances.forEach(chart => chart.dispose()) }
  }, [])

  // Keep appearance separate from sample updates so a new point does not
  // replace hover styles or reconfigure the whole chart.
  useEffect(() => {
    for (const [index, key] of (['cpu', 'memory'] as const).entries()) {
      const chart = charts.current[index]
      if (!chart || chart.isDisposed()) continue
      const color = index === 0 ? cpuColor : memoryColor
      const name = key === 'cpu' ? 'CPU' : zh ? '内存' : 'Memory'
      const width = sparkline ? 2 : 1.5
      const area = { color, opacity: sparkline ? 0.08 : 0 }
      const tooltipPosition: TooltipComponentPositionCallback = (point, _params, _dom, _rect, size) => {
        const bounds = chart.getDom().getBoundingClientRect()
        const left = Math.max(8, Math.min(bounds.left + point[0] + 12, window.innerWidth - size.contentSize[0] - 8))
        const top = Math.max(8, Math.min(bounds.top + point[1] + 12, window.innerHeight - size.contentSize[1] - 8))
        return [left - bounds.left, top - bounds.top]
      }
      chart.setOption({
        animation: false,
        backgroundColor: 'transparent',
        title: { show: !sparkline, text: key === 'cpu' ? 'CPU %' : zh ? '内存 MB' : 'Memory MB', textStyle: { color: label, fontSize: 11, fontWeight: 'normal' }, left: 0 },
        grid: sparkline ? { left: 3, right: 3, top: 3, bottom: 3 } : { left: 46, right: 12, top: 26, bottom: 24 },
        tooltip: { trigger: 'axis', renderMode: 'html', appendTo: () => document.body, confine: false, position: tooltipPosition, transitionDuration: 0, axisPointer: { animation: false }, padding: 0, borderWidth: 0, backgroundColor: 'transparent', textStyle: { fontFamily: 'inherit' }, formatter: (params: TooltipComponentFormatterCallbackParams) => metricTooltip(params, name, color, key === 'cpu' ? '%' : ' MB', formatDateTime), extraCssText: 'box-shadow:none;border-radius:0' },
        xAxis: { type: 'time', show: !sparkline, axisLabel: { color: label, fontSize: 10, formatter: (value: number) => formatTime(value) }, axisLine: { lineStyle: { color: border } } },
        yAxis: { type: 'value', show: !sparkline, min: 0, max: sparkline && key === 'cpu' ? (extent: { max: number }) => Math.max(100, extent.max) : undefined, axisLabel: { color: label, fontSize: 10 }, splitLine: { lineStyle: { color: border } } },
        series: [{ id: key, name, type: 'line', symbolSize: 4, connectNulls: false, lineStyle: { width, color }, itemStyle: { color }, areaStyle: area, emphasis: { scale: false, lineStyle: { width, color }, itemStyle: { color }, areaStyle: area }, markLine: { silent: true, symbol: 'none', animation: false, label: { show: false }, lineStyle: { type: 'solid', width, color }, emphasis: { disabled: true } } }],
      }, { lazyUpdate: true })
    }
  }, [label, border, cpuColor, memoryColor, formatTime, formatDateTime, zh, sparkline])

  useEffect(() => {
    for (const [index, key] of (['cpu', 'memory'] as const).entries()) {
      const chart = charts.current[index]
      if (!chart || chart.isDisposed()) continue
      const end = values.at(-1)?.time ?? Date.now()
      const data = values.map(point => [point.time, point[key] == null ? null : key === 'memory' ? point.memory! / 1024 ** 2 : point.cpu])
      // One measurement is a current-value guide, not invented history. Once
      // samples accumulate, fit their actual time span to the plotting width.
      const initial = values.length === 1 ? data[0][1] : null
      chart.setOption({
        xAxis: { min: values.length > 1 ? values[0].time : end - 5000, max: end },
        series: [{ id: key, showSymbol: values.length > 1 && values.filter(point => point[key] != null).length < 2, data, markLine: { data: initial == null ? [] : [{ yAxis: initial }] } }],
      }, { lazyUpdate: true })
    }
  }, [values, sparkline])

  return <div className={sparkline ? 'grid min-w-0 grid-cols-2 gap-4' : 'space-y-2'}>{(['cpu', 'memory'] as const).map((key, index) => <div key={key} role="img" aria-label={key === 'cpu' ? 'CPU trend' : zh ? '内存趋势' : 'Memory trend'} ref={element => { elements.current[index] = element }} className={sparkline ? 'h-16 min-w-0' : compact ? 'h-28' : 'h-56'} />)}</div>
}
