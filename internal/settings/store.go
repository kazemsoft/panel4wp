package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Mail struct {
	Enabled    bool   `json:"enabled"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Encryption string `json:"encryption"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	FromEmail  string `json:"from_email"`
	FromName   string `json:"from_name"`
}

type ApplyRequest struct {
	SiteID string `json:"site_id"`
	Mail   Mail   `json:"mail"`
}

type envelope struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

type Store struct {
	path string
	key  [32]byte
}

func New(path string, key []byte) (*Store, error) {
	if len(key) < 32 {
		return nil, errors.New("settings encryption key must contain at least 32 bytes")
	}
	return &Store{path: path, key: sha256.Sum256(key)}, nil
}

func ValidateMail(mail Mail) error {
	if !mail.Enabled {
		return nil
	}
	mail.Host = strings.TrimSpace(mail.Host)
	if mail.Host == "" || len(mail.Host) > 253 || strings.ContainsAny(mail.Host, " /\\\t\r\n\x00") {
		return errors.New("invalid SMTP host")
	}
	if mail.Port < 1 || mail.Port > 65535 {
		return errors.New("invalid SMTP port")
	}
	if mail.Encryption != "none" && mail.Encryption != "starttls" && mail.Encryption != "tls" {
		return errors.New("invalid SMTP encryption mode")
	}
	if len(mail.Username) > 254 || strings.ContainsAny(mail.Username, "\r\n\x00") || len(mail.Password) > 1024 || strings.ContainsRune(mail.Password, 0) {
		return errors.New("invalid SMTP credentials")
	}
	if mail.FromEmail == "" || len(mail.FromEmail) > 254 || !strings.Contains(mail.FromEmail, "@") || strings.ContainsAny(mail.FromEmail, " \t\r\n\x00") {
		return errors.New("invalid sender email")
	}
	if strings.TrimSpace(mail.FromName) == "" || len([]rune(mail.FromName)) > 120 || strings.ContainsAny(mail.FromName, "\r\n\x00") {
		return errors.New("invalid sender name")
	}
	return nil
}

func (s *Store) Save(mail Mail) error {
	mail.Host = strings.TrimSpace(mail.Host)
	mail.Username = strings.TrimSpace(mail.Username)
	mail.FromEmail = strings.TrimSpace(mail.FromEmail)
	mail.FromName = strings.TrimSpace(mail.FromName)
	if err := ValidateMail(mail); err != nil {
		return err
	}
	plain, err := json.Marshal(mail)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nil, nonce, plain, []byte("panel4wp-settings-v1"))
	data, err := json.Marshal(envelope{Version: 1, Nonce: base64.RawStdEncoding.EncodeToString(nonce), Data: base64.RawStdEncoding.EncodeToString(sealed)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Load() (Mail, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Mail{}, nil
	}
	if err != nil {
		return Mail{}, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Version != 1 {
		return Mail{}, errors.New("invalid encrypted settings")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return Mail{}, errors.New("invalid settings nonce")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(env.Data)
	if err != nil {
		return Mail{}, errors.New("invalid settings data")
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return Mail{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Mail{}, err
	}
	plain, err := gcm.Open(nil, nonce, sealed, []byte("panel4wp-settings-v1"))
	if err != nil {
		return Mail{}, errors.New("unable to decrypt settings")
	}
	var mail Mail
	if err := json.Unmarshal(plain, &mail); err != nil {
		return Mail{}, errors.New("invalid settings payload")
	}
	return mail, nil
}
