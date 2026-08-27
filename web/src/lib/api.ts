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

  const contentType = response.headers.get('content-type')?.toLowerCase() ?? ''
  const payload = await response.text()
  let body: ApiEnvelope<T>
  try {
    body = JSON.parse(payload) as ApiEnvelope<T>
  } catch {
    const detail = contentType.includes('text/html') || payload.trimStart().startsWith('<!doctype') || payload.trimStart().startsWith('<html')
      ? `The server returned the web application instead of the API response for ${path}. Restart or upgrade the SUMA backend and check the /api proxy.`
      : `The API returned an invalid response for ${path} (HTTP ${response.status}).`
    throw new ApiError(detail, -1, response.status)
  }
  if (!contentType.includes('json') || typeof body !== 'object' || body === null || typeof body.code !== 'number') {
    throw new ApiError(`The API returned an unexpected response type for ${path} (HTTP ${response.status}).`, -1, response.status)
  }
  if (!response.ok || body.code !== 0) throw new ApiError(body.message || 'Request failed', body.code, response.status)
  return body.data
}
