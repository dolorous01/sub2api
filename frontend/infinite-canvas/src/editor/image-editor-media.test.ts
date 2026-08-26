import { describe, expect, it } from 'vitest'
import { outpaintGeometry } from './image-editor-media'

describe('outpaintGeometry', () => {
  it('centers a portrait source inside a wider target', () => {
    expect(outpaintGeometry(800, 1200, 16 / 9)).toEqual({
      width: 2134,
      height: 1200,
      sourceX: 667,
      sourceY: 0,
      sourceWidth: 800,
      sourceHeight: 1200
    })
  })

  it('centers a landscape source inside a taller target', () => {
    expect(outpaintGeometry(1200, 800, 3 / 4)).toEqual({
      width: 1200,
      height: 1600,
      sourceX: 0,
      sourceY: 400,
      sourceWidth: 1200,
      sourceHeight: 800
    })
  })

  it('rejects output beyond the server canvas limit', () => {
    expect(() => outpaintGeometry(16_000, 16_000, 16 / 9)).toThrow(/supported size/)
  })
})
