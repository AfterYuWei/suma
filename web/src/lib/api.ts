export interface ApiEnvelope<T> {
  code: number
  message: string
  data: T
}

export class ApiError extends Error {
  constructor(message: string, readonly code: number, readonly status: number) {
    super(message)
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...init?.headers },
    ...init,
  })
  const body = (await response.json()) as ApiEnvelope<T>
  if (!response.ok || body.code !== 0) throw new ApiError(body.message || 'Request failed', body.code, response.status)
  return body.data
}
