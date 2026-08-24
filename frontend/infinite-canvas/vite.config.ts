import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

function releaseNotices(): Plugin {
  return {
    name: 'canvas-release-notices',
    generateBundle() {
      for (const fileName of ['LICENSE', 'LICENSE.upstream', 'NOTICE', 'UPSTREAM.md']) {
        this.emitFile({ type: 'asset', fileName, source: readFileSync(resolve(__dirname, fileName)) })
      }
    }
  }
}

export default defineConfig(({ mode }) => {
  const sourceURL = process.env.VITE_CANVAS_SOURCE_URL || ''
  if (mode === 'production' && !sourceURL.startsWith('https://')) {
    throw new Error('VITE_CANVAS_SOURCE_URL must be an HTTPS corresponding-source URL')
  }
  return {
    base: '/infinite-canvas/',
    plugins: [react(), releaseNotices()],
    define: {
      __CANVAS_SOURCE_URL__: JSON.stringify(sourceURL),
      __APP_VERSION__: JSON.stringify('0.16.0-sub2api.1'),
      __APP_RELEASES__: JSON.stringify([])
    },
    resolve: {
      alias: [
        {
          find: '@/stores/canvas/use-canvas-store',
          replacement: resolve(__dirname, 'src/adapters/use-canvas-store.ts')
        },
        {
          find: '@/stores/use-config-store',
          replacement: resolve(__dirname, 'src/adapters/use-config-store.ts')
        },
        {
          find: '@/services/image-storage',
          replacement: resolve(__dirname, 'src/adapters/image-storage.ts')
        },
        {
          find: '@/services/api/image',
          replacement: resolve(__dirname, 'src/adapters/image-api.ts')
        },
        {
          find: '@/services/api/video',
          replacement: resolve(__dirname, 'src/adapters/video-api.ts')
        },
        {
          find: '@/services/api/audio',
          replacement: resolve(__dirname, 'src/adapters/audio-api.ts')
        },
        {
          find: '@/lib/canvas/plugin-loader',
          replacement: resolve(__dirname, 'src/adapters/plugin-loader.ts')
        },
        {
          find: '@/lib/canvas/plugin-registry',
          replacement: resolve(__dirname, 'src/adapters/plugin-registry.ts')
        },
        {
          find: '@/lib/canvas/canvas-generation-helpers',
          replacement: resolve(__dirname, 'src/adapters/canvas-generation-helpers.ts')
        },
        {
          find: '@/services/file-storage',
          replacement: resolve(__dirname, 'src/adapters/file-storage.ts')
        },
        { find: '@sub2api', replacement: resolve(__dirname, 'src') },
        { find: '@', replacement: resolve(__dirname, 'src/upstream') }
      ]
    },
    build: {
      outDir: '../public/infinite-canvas',
      emptyOutDir: true,
      manifest: 'manifest.json',
      lib: { entry: resolve(__dirname, 'src/entry.tsx'), formats: ['es'] },
      rollupOptions: {
        output: {
          entryFileNames: 'assets/canvas-[hash].js',
          chunkFileNames: 'assets/chunk-[hash].js',
          assetFileNames: 'assets/[name]-[hash][extname]'
        }
      }
    }
  }
})
