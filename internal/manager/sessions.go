package manager

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SessionLifetime = 7 * 24 * time.Hour

var ErrInvalidCredentials = errors.New("invalid credentials")

func (s *Service) CreateSession(username, password string) (string, error) {
	settings := s.loadAppSettings()
	if !validateCredentials(settings, username, password) {
		return "", ErrInvalidCredentials
	}
	username = strings.TrimSpace(username)
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	hash, proof := sessionKeys(settings, token)
	now := time.Now()
	err := s.store.write(func() error {
		tx, err := s.store.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec("DELETE FROM sessions WHERE expires<=?", now.Unix()); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO sessions(token_hash,proof,username,expires) VALUES(?,?,?,?)", hash[:], proof, username, now.Add(SessionLifetime).Unix()); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) SessionUser(token string) (string, bool) {
	if len(token) != base64.RawURLEncoding.EncodedLen(32) {
		return "", false
	}
	hash, proof := sessionKeys(s.loadAppSettings(), token)
	var username string
	err := s.store.db.QueryRow("SELECT username FROM sessions WHERE token_hash=? AND proof=? AND expires>?",
		hash[:], proof, time.Now().Unix()).Scan(&username)
	return username, err == nil
}

func (s *Service) DeleteSession(token string) error {
	if len(token) != base64.RawURLEncoding.EncodedLen(32) {
		return nil
	}
	hash := sha256.Sum256([]byte(token))
	_, err := s.store.db.Exec("DELETE FROM sessions WHERE token_hash=?", hash[:])
	return err
}

func sessionKeys(settings appSettings, token string) ([32]byte, []byte) {
	// 令牌仅存哈希。更改登录配置会使旧会话的校验值失效。
	proof := hmac.New(sha256.New, []byte(token))
	fmt.Fprintf(proof, "%t:%q:%q", settings.authEnabled, strings.TrimSpace(settings.authUsername), settings.authPassword)
	return sha256.Sum256([]byte(token)), proof.Sum(nil)
}
