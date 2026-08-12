package accounts

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false
	}
	memory, err1 := strconv.ParseUint(strings.TrimPrefix(params[0], "m="), 10, 32)
	timeCost, err2 := strconv.ParseUint(strings.TrimPrefix(params[1], "t="), 10, 32)
	threads, err3 := strconv.ParseUint(strings.TrimPrefix(params[2], "p="), 10, 8)
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || len(want) != argonKeyLen || memory > 128*1024 || timeCost > 10 || threads > 8 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(timeCost), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 256 {
		return errors.New("password must contain 12 to 256 bytes")
	}
	return nil
}
