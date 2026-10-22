package main

import (
	"cmp"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// API keys in ~/.smash/config are stored as enc:v1:<base64(nonce|AES-256-GCM ciphertext)>, with the
// variable name as associated data. The AES key lives in smash.db. Both files sit in ~/.smash with
// mode 600, so this keeps keys out of plain text (synced or shared configs, grep, secret scanners);
// it doesn't stop malware running as the same user, which can read both.
const encPrefix = "enc:v1:"

var errUndecryptable = errors.New("can't decrypt the API key in ~/.smash/config")

func configPath(home string) string { return filepath.Join(smashDir(home), "config") }

// configKey returns the AES-256 key kept in smash.db, creating it on first use.
func configKey(db *sql.DB) ([]byte, error) {
	var key []byte
	err := db.QueryRow(`SELECT value FROM settings WHERE name = 'config_key'`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		key = make([]byte, 32)
		rand.Read(key)
		_, err = db.Exec(`INSERT INTO settings(name, value) VALUES ('config_key', ?)`, key)
	}
	return key, err
}

func seal(key []byte, name, plain string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	return encPrefix + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), []byte(name))), nil
}

func unseal(key []byte, name, val string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(val, encPrefix))
	if err != nil || len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("%s: %w", name, errUndecryptable)
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte(name))
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, errUndecryptable)
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("encryption key in smash.db: %w", errUndecryptable)
	}
	return cipher.NewGCM(block)
}

// writeConfig saves a fresh config with the API key encrypted.
func writeConfig(home string, db *sql.DB, provider, keyVar, key string) error {
	k, err := configKey(db)
	if err != nil {
		return err
	}
	enc, err := seal(k, keyVar, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(smashDir(home), 0o700); err != nil {
		return err
	}
	return os.WriteFile(configPath(home), []byte("PROVIDER="+provider+"\n"+keyVar+"="+enc+"\n"), 0o600)
}

// loadConfig reads KEY=VALUE lines from ~/.smash/config, decrypts *_API_KEY values (encrypting any
// plaintext ones in place) and returns the configured provider (PROVIDER=openai, the default, or
// anthropic) plus a label like "OpenAI gpt-6-luna".
func loadConfig(home string, db *sql.DB) (redactor, string, error) {
	data, err := os.ReadFile(configPath(home))
	if err != nil {
		return nil, "", err
	}
	k, err := configKey(db)
	if err != nil {
		return nil, "", err
	}
	lines := strings.Split(string(data), "\n")
	cfg := map[string]string{}
	changed := false
	for i, line := range lines {
		name, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		name, v = strings.TrimSpace(name), strings.TrimSpace(v)
		if !ok || strings.HasPrefix(name, "#") {
			continue
		}
		if strings.HasSuffix(name, "_API_KEY") && v != "" {
			if strings.HasPrefix(v, encPrefix) {
				if v, err = unseal(k, name, v); err != nil {
					return nil, "", err
				}
			} else {
				enc, err := seal(k, name, v)
				if err != nil {
					return nil, "", err
				}
				lines[i], changed = name+"="+enc, true
			}
		}
		cfg[name] = v
	}
	if changed {
		if err := writeAtomic(configPath(home), []byte(strings.Join(lines, "\n"))); err != nil {
			return nil, "", err
		}
	}

	switch p := cfg["PROVIDER"]; p {
	case "", "openai":
		if cfg["OPENAI_API_KEY"] == "" {
			return nil, "", errors.New("OPENAI_API_KEY missing in ~/.smash/config")
		}
		model := cmp.Or(cfg["OPENAI_MODEL"], defaultOpenAIModel)
		return &openAI{url: cmp.Or(cfg["OPENAI_BASE_URL"], defaultOpenAIURL), key: cfg["OPENAI_API_KEY"], model: model}, "OpenAI " + model, nil
	case "anthropic":
		if cfg["ANTHROPIC_API_KEY"] == "" {
			return nil, "", errors.New("ANTHROPIC_API_KEY missing in ~/.smash/config")
		}
		model := cmp.Or(cfg["ANTHROPIC_MODEL"], defaultAnthropicModel)
		return newAnthropic(cfg["ANTHROPIC_API_KEY"], cfg["ANTHROPIC_BASE_URL"], model), "Anthropic " + model, nil
	default:
		return nil, "", fmt.Errorf("unknown PROVIDER %q in ~/.smash/config (use openai or anthropic)", p)
	}
}
