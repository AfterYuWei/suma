export class ApiError extends Error {
  constructor(message: string, readonly code: number, readonly status: number, readonly data?: unknown) {
    super(message)
  }
}
