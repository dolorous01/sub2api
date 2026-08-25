import { describe, expect, it } from 'vitest'
import { resolveCanvasManifestEntry } from '../canvasLoader'

describe('resolveCanvasManifestEntry', () => {
  it('loads CSS emitted as a standalone Vite manifest entry', () => {
    const entry = resolveCanvasManifestEntry({
      'src/entry.tsx': {
        file: 'assets/canvas-abc.js',
        isEntry: true
      },
      'style.css': {
        file: 'assets/style-def.css'
      }
    })

    expect(entry).toEqual({
      file: 'assets/canvas-abc.js',
      isEntry: true,
      css: ['assets/style-def.css']
    })
  })

  it('deduplicates entry CSS and ignores non-style assets', () => {
    const entry = resolveCanvasManifestEntry({
      'src/entry.tsx': {
        file: 'assets/canvas-abc.js',
        css: ['assets/style-def.css'],
        isEntry: true
      },
      'style.css': {
        file: 'assets/style-def.css'
      },
      'asset.png': {
        file: 'assets/asset.png'
      }
    })

    expect(entry.css).toEqual(['assets/style-def.css'])
  })

  it('rejects a manifest without a browser entry', () => {
    expect(() => resolveCanvasManifestEntry({
      'style.css': { file: 'assets/style-def.css' }
    })).toThrow('Canvas entry is missing from the build manifest')
  })
})
