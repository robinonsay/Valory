// @{"req": ["REQ-FECOURSE-622", "REQ-FECOURSE-623", "REQ-FECONTENT-220", "REQ-FECONTENT-221"]}

import { describe, it, expect } from 'vitest'
import {
  validateImage,
  MAX_IMAGE_SIZE_BYTES,
  MAX_CHAT_IMAGES,
  MAX_HOMEWORK_IMAGES,
  ALLOWED_IMAGE_TYPES
} from './imageUpload'

function makeFile(name: string, sizeBytes: number, mimeType: string): File {
  const content = new Uint8Array(sizeBytes)
  return new File([content], name, { type: mimeType })
}

describe('imageUpload — validation (REQ-FECOURSE-622)', () => {
  it('accepts image/png', () => {
    const file = makeFile('pic.png', 1024, 'image/png')
    expect(validateImage(file)).toBeNull()
  })

  it('accepts image/jpeg', () => {
    const file = makeFile('photo.jpg', 2048, 'image/jpeg')
    expect(validateImage(file)).toBeNull()
  })

  it('accepts image/gif', () => {
    const file = makeFile('anim.gif', 512, 'image/gif')
    expect(validateImage(file)).toBeNull()
  })

  it('accepts image/webp', () => {
    const file = makeFile('modern.webp', 1500, 'image/webp')
    expect(validateImage(file)).toBeNull()
  })

  it('rejects image/bmp with error message', () => {
    const file = makeFile('bitmap.bmp', 1024, 'image/bmp')
    const result = validateImage(file)
    expect(result).not.toBeNull()
    expect(result).toContain('Image type not supported')
  })

  it('rejects image/svg+xml with error message', () => {
    const file = makeFile('graphic.svg', 512, 'image/svg+xml')
    const result = validateImage(file)
    expect(result).not.toBeNull()
    expect(result).toContain('Image type not supported')
  })

  it('rejects oversized image exceeding 5 MB', () => {
    const oversized = MAX_IMAGE_SIZE_BYTES + 1
    const file = makeFile('huge.png', oversized, 'image/png')
    const result = validateImage(file)
    expect(result).not.toBeNull()
    expect(result).toContain('Image exceeds 5 MB')
  })

  it('accepts image exactly at 5 MB boundary', () => {
    const file = makeFile('boundary.png', MAX_IMAGE_SIZE_BYTES, 'image/png')
    expect(validateImage(file)).toBeNull()
  })

  it('error message lists allowed types for unsupported type', () => {
    const file = makeFile('tiff.tiff', 1024, 'image/tiff')
    const result = validateImage(file)
    expect(result).toContain('png')
    expect(result).toContain('jpeg')
    expect(result).toContain('gif')
    expect(result).toContain('webp')
  })
})

describe('imageUpload — constants', () => {
  it('MAX_CHAT_IMAGES is 4', () => {
    expect(MAX_CHAT_IMAGES).toBe(4)
  })

  it('MAX_HOMEWORK_IMAGES is 8', () => {
    expect(MAX_HOMEWORK_IMAGES).toBe(8)
  })

  it('MAX_IMAGE_SIZE_BYTES is 5 MB', () => {
    expect(MAX_IMAGE_SIZE_BYTES).toBe(5 * 1024 * 1024)
  })

  it('ALLOWED_IMAGE_TYPES has exactly 4 entries', () => {
    expect(ALLOWED_IMAGE_TYPES.size).toBe(4)
  })

  it('ALLOWED_IMAGE_TYPES contains image/png', () => {
    expect(ALLOWED_IMAGE_TYPES.has('image/png')).toBe(true)
  })

  it('ALLOWED_IMAGE_TYPES contains image/jpeg', () => {
    expect(ALLOWED_IMAGE_TYPES.has('image/jpeg')).toBe(true)
  })

  it('ALLOWED_IMAGE_TYPES contains image/gif', () => {
    expect(ALLOWED_IMAGE_TYPES.has('image/gif')).toBe(true)
  })

  it('ALLOWED_IMAGE_TYPES contains image/webp', () => {
    expect(ALLOWED_IMAGE_TYPES.has('image/webp')).toBe(true)
  })
})
