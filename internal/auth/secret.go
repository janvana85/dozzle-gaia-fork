package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

// LoadOrCreateJWTSecret reads the JWT signing secret from path. If the file
// does not exist, a new 32-byte random secret is generated, written to the
// file, and a warning is logged. The caller owns the returned slice.
func LoadOrCreateJWTSecret(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create secret dir: %w", err)
	}

	if data, err := os.ReadFile(path); err == nil {
		hexStr := strings.TrimSpace(string(data))
		secret, err := hex.DecodeString(hexStr)
		if err != nil || len(secret) != 32 {
			log.Warn().Str("path", path).Msg("auth: JWT secret file is corrupt — regenerating")
		} else {
			log.Info().Str("path", path).Msg("auth: JWT secret loaded")
			return secret, nil
		}
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate JWT secret: %w", err)
	}

	hexStr := hex.EncodeToString(secret)
	if err := os.WriteFile(path, []byte(hexStr), 0600); err != nil {
		return nil, fmt.Errorf("write JWT secret: %w", err)
	}

	log.Warn().Str("path", path).Msg("auth: JWT secret was missing — generated and saved")
	return secret, nil
}
