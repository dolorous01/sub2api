import { resolve } from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: [
      { find: '@/stores/canvas/use-canvas-store', replacement: resolve(__dirname, 'src/adapters/use-canvas-store.ts') },
      { find: '@/stores/use-config-store', replacement: resolve(__dirname, 'src/adapters/use-config-store.ts') },
      { find: '@/services/image-storage', replacement: resolve(__dirname, 'src/adapters/image-storage.ts') },
      { find: '@/services/file-storage', replacement: resolve(__dirname, 'src/adapters/file-storage.ts') },
      { find: '@/services/api/image', replacement: resolve(__dirname, 'src/adapters/image-api.ts') },
      { find: '@/services/api/video', replacement: resolve(__dirname, 'src/adapters/video-api.ts') },
      { find: '@/services/api/audio', replacement: resolve(__dirname, 'src/adapters/audio-api.ts') },
      { find: '@/lib/canvas/plugin-loader', replacement: resolve(__dirname, 'src/adapters/plugin-loader.ts') },
      { find: '@/lib/canvas/plugin-registry', replacement: resolve(__dirname, 'src/adapters/plugin-registry.ts') },
      { find: '@/lib/canvas/canvas-generation-helpers', replacement: resolve(__dirname, 'src/adapters/canvas-generation-helpers.ts') },
      { find: '@sub2api', replacement: resolve(__dirname, 'src') },
      { find: '@', replacement: resolve(__dirname, 'src/upstream') }
    ]
  },
  test: { environment: 'jsdom', globals: true }
})
