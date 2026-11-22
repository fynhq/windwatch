package secret

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
	maxArgonWorkers  = 4
	maxArgonInFlight = 16
)

var (
	ErrInvalidHash  = errors.New("invalid password hash")
	ErrPasswordBusy = errors.New("password hashing capacity is exhausted")
	argonQueue      = make(chan struct{}, maxArgonInFlight)
	argonWorkers    = make(chan struct{}, maxArgonWorkers)
)

func acquireArgonSlot(ctx context.Context) (func(), error) {
	select {
	case argonQueue <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, ErrPasswordBusy
	}
	select {
	case argonWorkers <- struct{}{}:
	case <-ctx.Done():
		<-argonQueue
		return nil, ctx.Err()
	}
	return func() {
		<-argonWorkers
		<-argonQueue
	}, nil
}

func HashPassword(password string) (string, error) {
	return HashPasswordContext(context.Background(), password)
}

func HashPasswordContext(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	release, err := acquireArgonSlot(ctx)
	if err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	release()
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	return VerifyPasswordContext(context.Background(), encoded, password)
}

func VerifyPasswordContext(ctx context.Context, encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}

	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, ErrInvalidHash
	}
	const (
		maxArgonMemory = 128 * 1024

		maxArgonIterations  = 10
		maxArgonParallelism = 16
	)

	if memory == 0 || iterations == 0 || parallelism == 0 {
		return false, ErrInvalidHash
	}
	if memory > maxArgonMemory || iterations > maxArgonIterations || parallelism > maxArgonParallelism {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return false, ErrInvalidHash
	}

	release, err := acquireArgonSlot(ctx)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	release()
	return subtle.ConstantTimeCompare(expected, actual) == 1, nil
}

func VerifyDummyPassword(password string) error {
	return VerifyDummyPasswordContext(context.Background(), password)
}

func VerifyDummyPasswordContext(ctx context.Context, password string) error {
	dummyHashMu.Lock()
	hash := dummyHash
	if hash == "" {
		created, err := HashPasswordContext(ctx, "windwatch-dummy-password")
		if err != nil {
			dummyHashMu.Unlock()
			return err
		}
		dummyHash = created
		hash = created
	}
	dummyHashMu.Unlock()
	_, err := VerifyPasswordContext(ctx, hash, password)
	return err
}

var (
	dummyHashMu sync.Mutex
	dummyHash   string
)

func RandomToken(byteLength int) (string, error) {
	if byteLength <= 0 {
		return "", errors.New("token length must be positive")
	}
	buffer := make([]byte, byteLength)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
