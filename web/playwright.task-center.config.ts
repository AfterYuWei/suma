import { defineConfig } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import base from './playwright.config'

export default defineConfig({
  ...base,
  testDir: './e2e-task-center',
  outputDir: join(tmpdir(), 'suma-task-center-playwright'),
  use: { ...base.use, baseURL: 'http://127.0.0.1:5182', channel: process.env.PLAYWRIGHT_CHANNEL },
  webServer: {
    command: 'SUMA_VITE_CACHE_DIR=/tmp/suma-task-center-vite-cache npm run dev -- --host 127.0.0.1 --port 5182',
    url: 'http://127.0.0.1:5182',
    reuseExistingServer: false,
  },
})
