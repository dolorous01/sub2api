import { existsSync, readFileSync } from 'node:fs'

const noticeFiles = ['LICENSE', 'LICENSE.upstream', 'NOTICE', 'UPSTREAM.md']
const requiredFiles = [
  ...noticeFiles.map((file) => `infinite-canvas/${file}`),
  'public/infinite-canvas/manifest.json',
  ...noticeFiles.map((file) => `public/infinite-canvas/${file}`),
  '../backend/internal/web/dist/infinite-canvas/manifest.json',
  ...noticeFiles.map((file) => `../backend/internal/web/dist/infinite-canvas/${file}`)
]

for (const file of requiredFiles) {
  if (!existsSync(file)) {
    throw new Error(`missing required canvas release file: ${file}`)
  }
}

const sourceURL = process.env.VITE_CANVAS_SOURCE_URL
if (!sourceURL?.startsWith('https://')) {
  throw new Error('VITE_CANVAS_SOURCE_URL must be an HTTPS corresponding-source URL')
}

const license = readFileSync('infinite-canvas/LICENSE', 'utf8')
if (!license.includes('GNU AFFERO GENERAL PUBLIC LICENSE')) {
  throw new Error('canvas LICENSE is not AGPL-3.0')
}

const manifest = JSON.parse(readFileSync('public/infinite-canvas/manifest.json', 'utf8'))
if (!manifest['src/entry.tsx']?.isEntry) {
  throw new Error('canvas manifest does not contain the expected entry')
}

console.log('Canvas release metadata verified')
