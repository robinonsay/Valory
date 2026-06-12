// @{"req": ["REQ-FECOURSE-622", "REQ-FECOURSE-623"]}

// Image upload validation and constants per design spec.

export const ALLOWED_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp'])
export const MAX_IMAGE_SIZE_BYTES = 5 * 1024 * 1024 // 5 MB
export const MAX_CHAT_IMAGES = 4
export const MAX_HOMEWORK_IMAGES = 8

// @{"req": ["REQ-FECOURSE-623"]}
// validateImage returns null when the image is acceptable, or an error message
// for display to the student. Validation is client-side.
export function validateImage(file: File): string | null {
  if (!ALLOWED_IMAGE_TYPES.has(file.type)) {
    const accepted = Array.from(ALLOWED_IMAGE_TYPES).join(', ')
    return `Image type not supported. Allowed: ${accepted}`
  }
  if (file.size > MAX_IMAGE_SIZE_BYTES) {
    return 'Image exceeds 5 MB limit.'
  }
  return null
}

// Interface for image upload response from POST /api/v1/courses/{id}/images
export interface ImageUploadResponse {
  image_id: string
  url: string
}
