import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e-overview',
  workers: 1,
  timeout: 30_000,
  use: {
    baseURL: 'http://127.0.0.1:5188',
    viewport: { width: 1440, height: 1000 },
    launchOptions: { args: ['--no-sandbox'] },
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1 --port 5188',
    url: 'http://127.0.0.1:5188',
    reuseExistingServer: false,
  },
})
