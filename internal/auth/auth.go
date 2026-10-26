package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLen     = 16
	argonKeyLen      = 32
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonIterations, argonParallelism,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword returns whether the password matches and whether the hash
// should be upgraded to the current parameters.
func VerifyPassword(password, encoded string) (bool, bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, fmt.Errorf("unsupported password hash")
	}
	if parts[2] != "v=19" {
		return false, false, fmt.Errorf("unsupported argon2 version")
	}
	var memory, iterations uint32
	var parallelism uint8
	for _, p := range strings.Split(parts[3], ",") {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return false, false, fmt.Errorf("invalid argon2 params")
		}
		v, err := strconv.ParseUint(kv[1], 10, 32)
		if err != nil {
			return false, false, fmt.Errorf("invalid argon2 param %s", kv[0])
		}
		switch kv[0] {
		case "m":
			memory = uint32(v)
		case "t":
			iterations = uint32(v)
		case "p":
			parallelism = uint8(v)
		default:
			return false, false, fmt.Errorf("unknown argon2 param %s", kv[0])
		}
	}
	if memory == 0 || iterations == 0 || parallelism == 0 {
		return false, false, fmt.Errorf("missing argon2 params")
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return false, false, fmt.Errorf("decode hash: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	match := subtle.ConstantTimeCompare(got, want) == 1
	needsRehash := memory != argonMemory || iterations != argonIterations ||
		parallelism != argonParallelism || len(want) != argonKeyLen
	return match, needsRehash, nil
}

func ValidatePassword(password string) error {
	if len([]rune(password)) < 10 {
		return fmt.Errorf("password must be at least 10 characters")
	}
	return nil
}

// NewToken returns a URL-safe random token with n bytes of entropy.
func NewToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func NewSessionToken() (string, error) { return NewToken(32) }
func NewCSRFSecret() (string, error)   { return NewToken(32) }

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TokenPrefix(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12]
}

func TokenLast4(token string) string {
	if len(token) <= 4 {
		return token
	}
	return token[len(token)-4:]
}

type APIToken struct {
	Plaintext string
	Hash      string
	Prefix    string
	Last4     string
}

func NewAPIToken() (APIToken, error) {
	secret, err := NewToken(32)
	if err != nil {
		return APIToken{}, err
	}
	plain := "skarb_pat_" + secret
	return APIToken{
		Plaintext: plain,
		Hash:      HashToken(plain),
		Prefix:    TokenPrefix(plain),
		Last4:     TokenLast4(plain),
	}, nil
}

func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
