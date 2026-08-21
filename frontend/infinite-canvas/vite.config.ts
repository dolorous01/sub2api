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

function browserRuntimeGuard(): Plugin {
  return {
    name: 'canvas-browser-runtime-guard',
    generateBundle(_options, bundle) {
      for (const output of Object.values(bundle)) {
        if (output.type === 'chunk' && /\bprocess\.env\b/.test(output.code)) {
          this.error(`canvas browser bundle contains an unresolved process.env reference: ${output.fileName}`)
        }
      }
    }
  }
}

export default defineConfig(({ mode }) => {
  const sourceURL = process.env.VITE_CANVAS_SOURCE_URL || (mode === 'production' ? '' : 'https://github.com/basketikun/infinite-canvas')
  if (mode === 'production' && !sourceURL.startsWith('https://')) {
    throw new Error('VITE_CANVAS_SOURCE_URL must be an HTTPS corresponding-source URL')
  }
  return {
    base: '/infinite-canvas/',
    plugins: [react(), browserRuntimeGuard(), releaseNotices()],
    define: {
      __CANVAS_SOURCE_URL__: JSON.stringify(sourceURL),
      'process.env.NODE_ENV': JSON.stringify(mode === 'production' ? 'production' : 'development')
    },
    resolve: {
      alias: {
        '@': resolve(__dirname, 'src/upstream'),
        '@sub2api': resolve(__dirname, 'src')
      }
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
