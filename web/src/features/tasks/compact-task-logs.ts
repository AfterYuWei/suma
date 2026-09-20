export interface TaskLogEntry {
  id: number
  message: string
}

const dockerLayerLine = /^([a-f\d]{5,64}):?\s+(?:Pulling fs layer|Waiting|Downloading|Verifying Checksum|Download complete|Extracting|Pull complete|Already exists)(?:\s|$)/i

// Docker Compose redraws a layer's progress line for every byte-counter update.
// Keep only the newest frame for each layer while preserving ordinary output.
export function compactTaskLogs<T extends TaskLogEntry>(logs: T[]) {
  const result: T[] = []
  const layerIndexes = new Map<string, number>()

  for (const log of logs) {
    const layer = dockerLayerLine.exec(log.message)?.[1]?.toLowerCase()
    if (!layer) {
      result.push(log)
      continue
    }
    const existing = layerIndexes.get(layer)
    if (existing === undefined) {
      layerIndexes.set(layer, result.length)
      result.push(log)
    } else {
      result[existing] = log
    }
  }

  return result.sort((left, right) => left.id - right.id)
}
