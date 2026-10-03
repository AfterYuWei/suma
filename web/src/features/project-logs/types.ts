export interface LogSource { container_id: string; container_name: string; service: string; instance: number; state: string; one_off: boolean; orphan: boolean }
export interface LogRecord { id: string; time: string; service: string; container_id: string; container_name: string; stream: string; text: string; truncated?: boolean }
export interface LogSnapshot { entries: LogRecord[]; sources: LogSource[]; errors: { container_id: string; message: string }[]; truncated: boolean }
export interface LogEvent extends Partial<LogSnapshot> { type: string }
const recordBytes = (row: LogRecord) => (row.text.length + row.id.length + row.container_id.length + row.container_name.length + row.service.length + row.stream.length) * 2 + 256
export function mergeLogs(previous: LogRecord[], next: LogRecord[], tail: number) {
 const seen = new Set(previous.map(row => row.id)); const entries = [...previous]
 for (const row of next) if (!seen.has(row.id)) { entries.push(row); seen.add(row.id) }
 let bytes = entries.reduce((sum, row) => sum + recordBytes(row), 0); let dropped = 0
 while (entries.length > tail || bytes > 8 * 1024 * 1024) { const row = entries[dropped++]; bytes -= recordBytes(row); if (entries.length - dropped <= tail && bytes <= 8 * 1024 * 1024) break }
 return { entries: entries.slice(dropped), truncated: dropped > 0 || next.some(row => row.truncated) }
}
