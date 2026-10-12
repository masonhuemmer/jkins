package vault

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"filippo.io/age"
)

type Credential struct {
	User  string `json:"user"`
	Token string `json:"token"`
}

type Store struct{ Dir string }

var vaultMu sync.Mutex

type envelope struct {
	Version int    `json:"version"`
	Data    string `json:"ciphertext"`
}

type contents struct {
	Credential *Credential `json:"credential"`
}

func (s Store) vaultPath() string    { return filepath.Join(s.Dir, "vault.json") }
func (s Store) identityPath() string { return filepath.Join(s.Dir, "keys", "identity.txt") }

func (s Store) Load() (Credential, bool, error) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	return s.load()
}

func (s Store) load() (Credential, bool, error) {
	var zero Credential
	if err := protectExisting(s); err != nil {
		return zero, false, err
	}
	data, err := os.ReadFile(s.vaultPath())
	if errors.Is(err, os.ErrNotExist) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, fmt.Errorf("read vault: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Version != 1 || env.Data == "" {
		return zero, false, errors.New("invalid vault envelope")
	}
	identity, err := s.readIdentity()
	if err != nil {
		return zero, false, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return zero, false, errors.New("invalid vault ciphertext")
	}
	reader, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return zero, false, errors.New("vault decryption failed")
	}
	plain, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		return zero, false, errors.New("vault decryption failed")
	}
	var record contents
	if err := json.Unmarshal(plain, &record); err != nil {
		return zero, false, errors.New("invalid vault contents")
	}
	if record.Credential == nil {
		return zero, false, nil
	}
	if record.Credential.User == "" || record.Credential.Token == "" {
		return zero, false, errors.New("invalid vault credential")
	}
	return *record.Credential, true, nil
}

func (s Store) Save(credential Credential) error {
	if credential.User == "" || credential.Token == "" {
		return errors.New("user and token are required")
	}
	vaultMu.Lock()
	defer vaultMu.Unlock()
	if _, _, err := s.load(); err != nil {
		return err
	}
	return s.write(contents{Credential: &credential})
}

func (s Store) Delete() error {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	_, present, err := s.load()
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	return s.write(contents{})
}

func (s Store) write(record contents) error {
	if err := privateDir(s.Dir); err != nil {
		return err
	}
	if err := privateDir(filepath.Join(s.Dir, "keys")); err != nil {
		return err
	}
	identity, err := s.readIdentity()
	if errors.Is(err, os.ErrNotExist) {
		identity, err = s.createIdentity()
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	plain, err := json.Marshal(record)
	if err != nil {
		return err
	}
	var encrypted bytes.Buffer
	writer, err := age.Encrypt(&encrypted, identity.Recipient())
	if err != nil {
		return err
	}
	if _, err := writer.Write(plain); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	data, err := json.Marshal(envelope{Version: 1, Data: base64.StdEncoding.EncodeToString(encrypted.Bytes())})
	if err != nil {
		return err
	}
	return atomicWrite(s.vaultPath(), append(data, '\n'))
}

func (s Store) createIdentity() (*age.X25519Identity, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(s.identityPath()), ".jkins-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := privateFile(file); err != nil {
		return nil, err
	}
	if _, err := file.WriteString(identity.String() + "\n"); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(file.Name(), s.identityPath()); err != nil {
		if errors.Is(err, os.ErrExist) {
			return s.readIdentity()
		}
		return nil, err
	}
	return identity, nil
}

func (s Store) readIdentity() (*age.X25519Identity, error) {
	data, err := os.ReadFile(s.identityPath())
	if err != nil {
		return nil, err
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, errors.New("invalid vault identity")
	}
	return identity, nil
}

func atomicWrite(path string, data []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".jkins-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := privateFile(file); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
