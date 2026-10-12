import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e-containers',
  workers: 1,
  timeout: 30_000,
  use: {
    baseURL: 'http://127.0.0.1:5189',
    viewport: { width: 1440, height: 1000 },
    launchOptions: { args: ['--no-sandbox'] },
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1 --port 5189',
    url: 'http://127.0.0.1:5189',
    reuseExistingServer: false,
  },
})
