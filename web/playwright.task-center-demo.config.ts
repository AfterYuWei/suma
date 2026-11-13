import { defineConfig } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import base from './playwright.task-center.config'

export default defineConfig({
  ...base,
  testDir: './e2e-task-center-demo',
  outputDir: join(tmpdir(), 'suma-task-center-demo-playwright'),
  use: { ...base.use, baseURL: 'http://127.0.0.1:5183' },
  webServer: {
    command: 'SUMA_VITE_CACHE_DIR=/tmp/suma-task-center-demo-cache npm run dev:demo -- --host 127.0.0.1 --port 5183',
    url: 'http://127.0.0.1:5183',
    reuseExistingServer: false,
  },
})
