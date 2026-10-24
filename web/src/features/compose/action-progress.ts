export interface ComposeTask {
  id: string
  type: string
  name: string
  status: string
  progress: number
  message: string
}

export interface ComposeTaskLog {
  id: number
  level: string
  message: string
  created_at: string
}

export interface ComposeOperation {
  action: string
  taskID: string
}

export interface ComposeResourceProgress {
  kind: 'image' | 'layer' | 'container' | 'network' | 'volume'
  name: string
  stage: string
  state: 'active' | 'complete' | 'warning' | 'failed'
  detail: string
  percent?: number
}

export function composeActionLabel(action: string, zh: boolean) {
  const labels: Record<string, [string, string]> = {
    up: ['启动', 'Start'], down: ['Down', 'Down'], start: ['启动', 'Start'],
    stop: ['停止', 'Stop'], restart: ['重启', 'Restart'], pull: ['拉取镜像', 'Pull images'],
    build: ['构建', 'Build'], update: ['拉取并重建', 'Pull & recreate'],
  }
  return labels[action]?.[zh ? 0 : 1] ?? action
}

const completedStages = new Set(['pulled', 'pull complete', 'already exists', 'skipped', 'started', 'running', 'healthy', 'created', 'recreated', 'removed', 'stopped', 'exited'])
const bytePair = /(\d+(?:\.\d+)?\s*(?:[kMGT]?i?B))\s*\/\s*(\d+(?:\.\d+)?\s*(?:[kMGT]?i?B))/i

function bytes(text: string) {
  const match = /^(\d+(?:\.\d+)?)\s*([kMGT]?)(i?)B$/i.exec(text)
  return match ? Number(match[1]) * (match[3] ? 1024 : 1000) ** (match[2] ? 'kmgt'.indexOf(match[2].toLowerCase()) + 1 : 0) : 0
}

export function parseComposeResource(message: string): ComposeResourceProgress | undefined {
  const text = message.trim()
  const layer = /^([a-f\d]{5,64}):?\s+(Pulling fs layer|Waiting|Downloading|Verifying Checksum|Download complete|Extracting|Pull complete|Already exists)\b(.*)$/i.exec(text)
  const resource = /^(Container|Network|Volume)\s+(\S+)\s+(Creating|Created|Recreating|Recreated|Starting|Started|Running|Waiting|Healthy|Stopping|Stopped|Removing|Removed|Restarting|Exited|Error)\b(.*)$/i.exec(text)
  const image = /^(?:Image\s+)?(\S+)\s+(Pulling|Pulled|Skipped|Error|Warning)\b(.*)$/i.exec(text)
  const kind = layer ? 'layer' : resource ? resource[1].toLowerCase() as ComposeResourceProgress['kind'] : image ? 'image' : undefined
  if (!kind) return
  const name = layer?.[1] ?? resource?.[2] ?? image![1]
  const rawStage = layer?.[2] ?? resource?.[3] ?? image![2]
  const rawDetail = layer?.[3] ?? resource?.[4] ?? image![3]
  const stage = rawStage.toLowerCase()
  const pair = bytePair.exec(rawDetail)
  const current = pair ? bytes(pair[1]) : 0
  const total = pair ? bytes(pair[2]) : 0
  return {
    kind, name, stage,
    state: stage === 'error' ? 'failed' : stage === 'warning' ? 'warning' : completedStages.has(stage) ? 'complete' : 'active',
    // Keep byte counters, but discard the CLI's ASCII bar and elapsed-time decoration.
    detail: pair ? `${pair[1]} / ${pair[2]}` : /\d+(?:\.\d+)?\s*(?:[kMGT]?i?B)\b/i.exec(rawDetail)?.[0] ?? '',
    percent: total > 0 ? Math.min(100, Math.max(0, Math.round(current / total * 100))) : undefined,
  }
}

export function composeProgress(action: string, task: ComposeTask | undefined, logs: ComposeTaskLog[]) {
  const resources = new Map<string, ComposeResourceProgress>()
  let recreating = false
  // The current message carries the latest byte-counter frame, which is not
  // persisted to TaskLog for every redraw. Never use task.progress as bytes.
  for (const { message } of logs) {
    if (message.trim() === 'Images ready; recreating Compose services') recreating = true
    const row = parseComposeResource(message)
    if (row) resources.set(`${row.kind}:${row.name}`, row)
  }
  if (task?.message.trim() === 'Images ready; recreating Compose services') recreating = true
  const current = parseComposeResource(task?.message ?? '')
  if (current) {
    const key = `${current.kind}:${current.name}`
    const previous = resources.get(key)
    // Status and logs are polled independently. A slower task response must
    // not overwrite a newer lifecycle transition already present in the log.
    if (!previous || previous.stage === current.stage) resources.set(key, current)
  }
  const success = task?.status === 'success'
  const phase = action === 'update' ? (recreating ? 'recreate' : 'pull') : action === 'pull' ? 'pull' : 'execute'
  const rows = [...resources.values()]
  return {
    phase,
    pullComplete: success || recreating,
    images: rows.filter(row => row.kind === 'image'),
    layers: rows.filter(row => row.kind === 'layer'),
    resources: rows.filter(row => row.kind !== 'image' && row.kind !== 'layer'),
  }
}

export function composeStageLabel(stage: string, zh: boolean) {
  const labels: Record<string, string> = {
    pulling: '拉取中', pulled: '已就绪', skipped: '已跳过', error: '失败', warning: '警告',
    'pulling fs layer': '等待下载', waiting: '等待中', downloading: '下载中',
    'verifying checksum': '校验中', 'download complete': '下载完成', extracting: '解压中',
    'pull complete': '已就绪', 'already exists': '已缓存', creating: '创建中', created: '已创建',
    recreating: '重建中', recreated: '已重建', starting: '启动中', started: '已启动',
    running: '运行中', healthy: '健康', stopping: '停止中', stopped: '已停止',
    removing: '移除中', removed: '已移除', restarting: '重启中', exited: '已退出',
  }
  return zh ? labels[stage] ?? stage : stage.charAt(0).toUpperCase() + stage.slice(1)
}
