import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'
import path from 'node:path'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const apiTarget = env.SUMA_DEV_API || 'http://127.0.0.1:8081'
  const demoMode = mode === 'demo'

  return {
    plugins: [react(), tailwindcss()],
    define: {
      'import.meta.env.VITE_SUMA_DEMO_MODE': JSON.stringify(demoMode ? 'true' : 'false'),
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    server: {
      proxy: {
        '/api': apiTarget,
        '/ws': { target: apiTarget, ws: true },
      },
    },
  }
})
