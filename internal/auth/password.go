package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime      = 1
	argonMemory    = 65536
	argonThreads   = 4
	argonKeyLen    = 32
	argonSaltLen   = 16
	maxArgonMemory = 1 << 20 // 1 GiB in KiB
	maxArgonTime   = 10
	maxArgonThreads = 16
)

// @{"req": ["REQ-AUTH-008", "REQ-SYS-041"]}
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encoded := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// @{"req": ["REQ-AUTH-008", "REQ-SYS-041"]}
func CheckPassword(plain, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, fmt.Errorf("invalid argon2id hash format")
	}

	if parts[2] != "v=19" {
		return false, fmt.Errorf("unsupported argon2id version")
	}

	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, fmt.Errorf("invalid argon2id parameters")
	}

	var memory, iterations uint32
	var threads uint8
	_, err := fmt.Sscanf(params[0], "m=%d", &memory)
	if err != nil {
		return false, fmt.Errorf("invalid memory parameter")
	}
	_, err = fmt.Sscanf(params[1], "t=%d", &iterations)
	if err != nil {
		return false, fmt.Errorf("invalid time parameter")
	}
	var threadsInt int
	_, err = fmt.Sscanf(params[2], "p=%d", &threadsInt)
	if err != nil {
		return false, fmt.Errorf("invalid threads parameter")
	}

	if memory < 1 || iterations < 1 || memory > maxArgonMemory || iterations > maxArgonTime ||
		threadsInt < 1 || threadsInt > int(maxArgonThreads) {
		return false, fmt.Errorf("argon2id parameters out of allowed range")
	}

	threads = uint8(threadsInt)

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("invalid salt encoding")
	}

	storedKey, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("invalid key encoding")
	}

	if len(storedKey) != argonKeyLen {
		return false, fmt.Errorf("invalid key length: got %d, want %d", len(storedKey), argonKeyLen)
	}

	derivedKey := argon2.IDKey([]byte(plain), salt, iterations, memory, threads, argonKeyLen)

	if subtle.ConstantTimeCompare(derivedKey, storedKey) == 1 {
		return true, nil
	}
	return false, nil
}
