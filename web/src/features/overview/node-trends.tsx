import { useEffect, useMemo, useRef } from 'react'
import { init, use as registerCharts, type ECharts } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, MarkLineComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { TooltipComponentFormatterCallbackParams, TooltipComponentPositionCallback } from 'echarts'
import { popoverSurfaceClassName } from '../../components/ui/popover-styles'
import { chartColors } from '../../lib/chart-colors'
import { useDateTime } from '../../lib/time-zone'
import { useUIStore } from '../../stores/ui'
import type { NodeMetricPoint } from './observation'

registerCharts([LineChart, GridComponent, MarkLineComponent, TooltipComponent, CanvasRenderer])

export function NodeTrend({ points, metric }: { points: NodeMetricPoint[]; metric: 'cpu' | 'memory' }) {
  useUIStore(state => state.resolvedDark)
  const zh = useUIStore(state => state.language) === 'zh-CN'
  const { formatTime, formatDateTime } = useDateTime()
  const element = useRef<HTMLDivElement>(null)
  const instance = useRef<ECharts | null>(null)
  const name = metric === 'cpu' ? (zh ? '容器 CPU' : 'Container CPU') : (zh ? '容器内存' : 'Container memory')
  const styles = getComputedStyle(document.documentElement)
  const rawLabel = styles.getPropertyValue('--muted-foreground').trim()
  const rawBorder = styles.getPropertyValue('--border').trim()
  const rawColor = styles.getPropertyValue(metric === 'cpu' ? '--chart-1' : '--chart-2').trim()
  const [label, border, color] = useMemo(() => chartColors([rawLabel, rawBorder, rawColor]), [rawLabel, rawBorder, rawColor])

  useEffect(() => {
    const target = element.current!
    const chart = init(target)
    instance.current = chart
    let width = target.clientWidth, height = target.clientHeight
    const resize = new ResizeObserver(() => {
      if (chart.isDisposed()) return
      if (width === target.clientWidth && height === target.clientHeight) return
      width = target.clientWidth; height = target.clientHeight
      chart.resize({ width, height })
    })
    resize.observe(target)
    return () => { resize.disconnect(); instance.current = null; chart.dispose() }
  }, [])

  useEffect(() => {
    const chart = instance.current
    if (!chart) return
    const position: TooltipComponentPositionCallback = (point, _params, _dom, _rect, bounds) => {
      const rect = chart.getDom().getBoundingClientRect()
      return [Math.max(8, Math.min(rect.left + point[0] + 12, window.innerWidth - bounds.contentSize[0] - 8)) - rect.left,
        Math.max(8, Math.min(rect.top + point[1] + 12, window.innerHeight - bounds.contentSize[1] - 8)) - rect.top]
    }
    const formatter = (params: TooltipComponentFormatterCallbackParams) => {
      const sample = Array.isArray(params) ? params[0] : params
      if (!sample || !Array.isArray(sample.value)) return ''
      const [time, value] = sample.value
      const popup = document.createElement('div')
      popup.dataset.slot = 'chart-tooltip'
      popup.className = `${popoverSurfaceClassName} space-y-2 p-2.5 text-xs leading-4`
      const date = document.createElement('div')
      date.className = 'text-muted-foreground'
      date.textContent = `${zh ? '观测时间' : 'Observed'} · ${typeof time === 'number' ? formatDateTime(time) : '—'}`
      const row = document.createElement('div')
      row.className = 'flex items-center justify-between gap-4'
      const caption = document.createElement('span')
      caption.textContent = name
      const number = document.createElement('span')
      number.className = 'font-mono font-medium tabular-nums'
      number.textContent = typeof value === 'number' ? `${value.toFixed(1)}%` : '—'
      row.append(caption, number); popup.append(date, row)
      return popup
    }
    chart.setOption({
      animation: false,
      backgroundColor: 'transparent',
      grid: { left: 34, right: 8, top: 8, bottom: 24 },
      tooltip: { trigger: 'axis', renderMode: 'html', appendTo: () => document.body, confine: false, position, transitionDuration: 0, axisPointer: { animation: false }, padding: 0, borderWidth: 0, backgroundColor: 'transparent', textStyle: { fontFamily: 'inherit' }, formatter, extraCssText: 'box-shadow:none;border-radius:0' },
      xAxis: { type: 'time', splitNumber: 2, axisLabel: { color: label, fontSize: 11, hideOverlap: true, formatter: (value: number) => formatTime(value, { hour: '2-digit', minute: '2-digit', second: undefined, hour12: false }) }, axisTick: { show: false }, axisLine: { show: false }, axisPointer: { lineStyle: { color: border } } },
      yAxis: { type: 'value', min: 0, max: (extent: { max: number }) => Math.max(5, Math.ceil(extent.max / (extent.max > 25 ? 25 : 5)) * (extent.max > 25 ? 25 : 5)), splitNumber: 2, axisLabel: { color: label, fontSize: 11, formatter: '{value}%' }, splitLine: { lineStyle: { color: border, opacity: 0.35 } } },
      series: [{ id: metric, name, type: 'line', symbolSize: 4, connectNulls: false, lineStyle: { width: 1.5, color }, areaStyle: { color, opacity: 0.08 }, itemStyle: { color }, emphasis: { scale: false, lineStyle: { width: 1.5, color }, areaStyle: { color, opacity: 0.08 }, itemStyle: { color } }, markLine: { silent: true, symbol: 'none', animation: false, label: { show: false }, lineStyle: { type: 'solid', width: 1.5, color }, emphasis: { disabled: true } } }],
    }, { lazyUpdate: true })
  }, [label, border, color, metric, name, formatTime, formatDateTime, zh])

  useEffect(() => {
    const chart = instance.current
    if (!chart) return
    const end = points.at(-1)?.time ?? Date.now()
    const data = points.map(point => [point.time, point[metric]])
    const observations = points.filter(point => point[metric] != null)
    // Switching from summary to detail can yield two observations within the
    // first sampling interval. Keep a current-value reference until the
    // observed time span can show a meaningful trend.
    const latest = points.at(-1)
    const first = latest && observations.length > 0 && latest.time - observations[0].time < 10_000 ? latest[metric] : null
    chart.setOption({
      xAxis: { min: end - 300_000, max: end },
      series: [{ id: metric, data, showSymbol: observations.length < 2,
        markLine: { data: first == null ? [] : [{ yAxis: first }] } }],
    }, { lazyUpdate: true })
  }, [points, metric])

  return <div ref={element} role="img" aria-label={`${name} · ${zh ? '最近 5 分钟趋势' : 'Last 5 minutes trend'}`} data-node-trend={metric} data-sample-count={points.length} className="h-28 min-w-0 w-full @xl:h-40" />
}
