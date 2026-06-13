// @{"req": ["REQ-FEUSER-004"]}
//
// This client policy is intentionally STRICTER than the server's authoritative
// floor. The backend (internal/user/service.go ChangePassword) enforces only a
// minimum length of 8; the UI additionally requires upper, lower, digit, and
// special characters to nudge users toward stronger passwords. The relationship
// is one-directional and safe: any password the UI accepts also satisfies the
// server, so the UI never approves something the server would reject. A caller
// hitting the API directly is still bounded by the server floor.

export interface PasswordValidationResult {
  isValid: boolean
  error: string | null
}

const PASSWORD_MIN_LENGTH = 8
const PASSWORD_REQUIRES_UPPERCASE = true
const PASSWORD_REQUIRES_LOWERCASE = true
const PASSWORD_REQUIRES_DIGIT = true
const PASSWORD_REQUIRES_SPECIAL = true

export function validatePasswordStrength(password: string): PasswordValidationResult {
  if (password.length < PASSWORD_MIN_LENGTH) {
    return {
      isValid: false,
      error: `Password must be at least ${PASSWORD_MIN_LENGTH} characters`
    }
  }

  if (PASSWORD_REQUIRES_UPPERCASE && !/[A-Z]/.test(password)) {
    return {
      isValid: false,
      error: 'Password must contain at least one uppercase letter'
    }
  }

  if (PASSWORD_REQUIRES_LOWERCASE && !/[a-z]/.test(password)) {
    return {
      isValid: false,
      error: 'Password must contain at least one lowercase letter'
    }
  }

  if (PASSWORD_REQUIRES_DIGIT && !/\d/.test(password)) {
    return {
      isValid: false,
      error: 'Password must contain at least one digit'
    }
  }

  if (PASSWORD_REQUIRES_SPECIAL && !/[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(password)) {
    return {
      isValid: false,
      error: 'Password must contain at least one special character'
    }
  }

  return {
    isValid: true,
    error: null
  }
}
