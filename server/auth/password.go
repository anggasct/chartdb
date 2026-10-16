package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

func HashPassword(password string, memory, time uint32, threads uint8) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, time, memory, threads, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(encodedHash, password string) (bool, error) {
	var memory, time uint32
	var threads uint8
	var saltB64, hashB64 string
	rest, ok := strings.CutPrefix(encodedHash, "$argon2id$v=19$")
	if !ok {
		return false, fmt.Errorf("unsupported password hash format")
	}
	parts := strings.Split(rest, "$")
	if len(parts) != 3 {
		return false, fmt.Errorf("unsupported password hash format")
	}
	if _, err := fmt.Sscanf(parts[0], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("unsupported password hash format")
	}
	saltB64, hashB64 = parts[1], parts[2]
	salt, err := base64.RawStdEncoding.DecodeString(saltB64)
	if err != nil {
		return false, fmt.Errorf("unsupported password hash format")
	}
	want, err := base64.RawStdEncoding.DecodeString(hashB64)
	if err != nil {
		return false, fmt.Errorf("unsupported password hash format")
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if len(got) != len(want) {
		return false, nil
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
