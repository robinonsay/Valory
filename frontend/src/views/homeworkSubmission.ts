// @{"req": ["REQ-FECONTENT-021", "REQ-FECONTENT-022", "REQ-FECONTENT-111", "REQ-FECONTENT-112", "REQ-FECONTENT-113", "REQ-FECONTENT-114"]}

// ACCEPTED_EXTENSIONS matches the set accepted by DetectFormat in the submission backend.
export const ACCEPTED_EXTENSIONS = ['.tex', '.md', '.markdown', '.adoc', '.asciidoc']

// MAX_SIZE_BYTES is 10 MiB = 10 * 1024 * 1024, matching the backend defaultMaxUploadBytes constant.
export const MAX_SIZE_BYTES = 10_485_760

// @{"req": ["REQ-FECONTENT-022", "REQ-FECONTENT-112", "REQ-FECONTENT-113", "REQ-FECONTENT-114"]}
// validateFile returns null when the file is acceptable, or an error string to display to the student.
// Validation runs entirely client-side so no network call is made for invalid files.
export function validateFile(file: File): string | null {
  const ext = '.' + file.name.split('.').pop()?.toLowerCase()
  if (!ACCEPTED_EXTENSIONS.includes(ext)) {
    return `Accepted formats: ${ACCEPTED_EXTENSIONS.join(', ')}`
  }
  if (file.size > MAX_SIZE_BYTES) {
    return 'File exceeds 10 MB limit.'
  }
  return null
}
