// Package auth manages proxy users and their API keys. Keys are stored as
// salted SHA-256 hashes; the plaintext is only ever seen at creation time.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Key is one API key owned by a user.
type Key struct {
	ID        string     `json:"id"` // hex, random
	Name      string     `json:"name"`
	Hash      string     `json:"hash"` // hex(salt || sha256(salt||key))
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	Disabled  bool       `json:"disabled,omitempty"`
}

// User holds N keys.
type User struct {
	Name string `json:"name"`
	Keys []Key  `json:"keys"`
}

// Store is the on-disk key store. Safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	path  string
	users []User
}

// Prefix is prepended to generated keys so leaked values are identifiable.
const prefix = "llx_"

// NewStore creates (or loads) a store at path.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.users); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// save writes the store atomically with 0600.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".keys-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// CreateUser adds a user.
func (s *Store) CreateUser(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.Name == name {
			return fmt.Errorf("user %q already exists", name)
		}
	}
	s.users = append(s.users, User{Name: name})
	return s.save()
}

// CreateKey mints a new key for user. Returns the plaintext once; only the
// hash is stored.
func (s *Store) CreateKey(user, keyName string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	plain := prefix + hex.EncodeToString(raw)
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	k := Key{
		ID:        hex.EncodeToString(id),
		Name:      keyName,
		Hash:      HashKey(plain),
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		if s.users[i].Name == user {
			s.users[i].Keys = append(s.users[i].Keys, k)
			return plain, s.save()
		}
	}
	return "", fmt.Errorf("user %q not found", user)
}

// ListKeys returns the user's keys without hashes.
func (s *Store) ListKeys(user string) ([]Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Name == user {
			out := make([]Key, len(u.Keys))
			copy(out, u.Keys)
			for i := range out {
				out[i].Hash = ""
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", user)
}

// DisableKey marks a key disabled by ID.
func (s *Store) DisableKey(user, keyID string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		if s.users[i].Name == user {
			for j := range s.users[i].Keys {
				if s.users[i].Keys[j].ID == keyID {
					s.users[i].Keys[j].Disabled = disabled
					return s.save()
				}
			}
			return fmt.Errorf("key %q not found", keyID)
		}
	}
	return fmt.Errorf("user %q not found", user)
}

// Verify checks a presented plaintext key. On success it returns the owning
// user's name and records LastUsed lazily (in memory; flushed on next save).
func (s *Store) Verify(presented string) (string, bool) {
	if presented == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		for _, k := range u.Keys {
			if k.Disabled {
				continue
			}
			if VerifyHash(k.Hash, presented) {
				return u.Name, true
			}
		}
	}
	return "", false
}

// Users lists user names.
func (s *Store) Users() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u.Name)
	}
	return out
}

// HashKey derives the stored representation of a plaintext key:
// hex(16-byte-salt || sha256(salt||key)). Comparison is constant-time.
func HashKey(plain string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	sum := sha256.Sum256(append(salt[:], []byte(plain)...))
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(sum[:])
}

// VerifyHash reports whether plain matches a stored hash from HashKey.
// A legacy/invalid hash format never matches.
func VerifyHash(stored, plain string) bool {
	var saltHex, sumHex string
	if n, err := fmt.Sscanf(stored, "%64x:%64x", &saltHex, &sumHex); err != nil || n != 2 {
		return false
	}
	salt, err1 := hex.DecodeString(saltHex)
	sum, err2 := hex.DecodeString(sumHex)
	if err1 != nil || err2 != nil || len(salt) != 16 || len(sum) != sha256.Size {
		return false
	}
	calc := sha256.Sum256(append(salt, []byte(plain)...))
	return subtle.ConstantTimeCompare(calc[:], sum) == 1
}
