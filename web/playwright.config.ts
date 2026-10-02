import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  workers: 1,
  timeout: 45_000,
  use: {
    baseURL: 'http://127.0.0.1:5178',
    viewport: { width: 1440, height: 1000 },
    launchOptions: { args: ['--no-sandbox'] },
    screenshot: 'only-on-failure'
  },
  webServer: {
    command: 'npm run dev:demo -- --host 127.0.0.1 --port 5178',
    url: 'http://127.0.0.1:5178',
    reuseExistingServer: true
  }
})
