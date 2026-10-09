package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
	_ "modernc.org/sqlite"
)

const passwordIterations = 210_000

// ErrInvalidCredentials is returned for an unknown user or a wrong password.
var ErrInvalidCredentials = errors.New("Invalid username or password")

// dummyPasswordHash is a well-formed hash no password matches, used to keep
// login timing uniform for unknown usernames.
var dummyPasswordHash = func() string {
	hash, _ := hashPassword("cliff-dummy-password", "0000000000000000")
	return hash
}()

const sessionDays = 14

func (s *Store) HasUser(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) CreateUser(ctx context.Context, username string, password string) (User, error) {
	hasUser, err := s.HasUser(ctx)
	if err != nil {
		return User{}, err
	}
	if hasUser {
		return User{}, errors.New("Initial user already exists")
	}
	if len(strings.TrimSpace(username)) < 3 {
		return User{}, errors.New("Username must be at least 3 characters")
	}
	if len(password) < 10 {
		return User{}, errors.New("Password must be at least 10 characters")
	}

	id, err := randomHex(16)
	if err != nil {
		return User{}, err
	}
	passwordHash, err := hashPassword(password, "")
	if err != nil {
		return User{}, err
	}
	user := User{ID: id, Username: strings.TrimSpace(username), Role: RoleAdmin}
	_, err = s.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`, user.ID, user.Username, passwordHash, user.Role, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) Authenticate(ctx context.Context, username string, password string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, role, totp_enabled, password_hash FROM users WHERE username = ?`, strings.TrimSpace(username))
	var user User
	var totpEnabled string
	var passwordHash string
	if err := row.Scan(&user.ID, &user.Username, &user.Role, &totpEnabled, &passwordHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Burn the same PBKDF2 cost as a real check so response time
			// does not reveal whether the username exists.
			verifyPassword(password, dummyPasswordHash)
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	if !verifyPassword(password, passwordHash) {
		return User{}, ErrInvalidCredentials
	}
	user.TotpEnabled = totpEnabled == "true"
	return user, nil
}

func (s *Store) UpdateUserAccount(ctx context.Context, userID string, username string, currentPassword string, newPassword string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, role, totp_enabled, password_hash FROM users WHERE id = ?`, userID)
	var user User
	var totpEnabled string
	var passwordHash string
	if err := row.Scan(&user.ID, &user.Username, &user.Role, &totpEnabled, &passwordHash); err != nil {
		if err == sql.ErrNoRows {
			return User{}, errors.New("Account not found")
		}
		return User{}, err
	}

	nextUsername := strings.TrimSpace(username)
	if nextUsername == "" {
		nextUsername = user.Username
	}
	if len(nextUsername) < 3 {
		return User{}, errors.New("Username must be at least 3 characters")
	}
	if newPassword != "" {
		if len(newPassword) < 10 {
			return User{}, errors.New("Password must be at least 10 characters")
		}
		if !verifyPassword(currentPassword, passwordHash) {
			return User{}, errors.New("Current password is incorrect")
		}
	}

	var existingID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ? AND id <> ?`, nextUsername, user.ID).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return User{}, err
	}
	if existingID != "" {
		return User{}, errors.New("Username is already in use")
	}

	if newPassword != "" {
		nextHash, err := hashPassword(newPassword, "")
		if err != nil {
			return User{}, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET username = ?, password_hash = ? WHERE id = ?`, nextUsername, nextHash, user.ID); err != nil {
			return User{}, err
		}
	} else if _, err := s.db.ExecContext(ctx, `UPDATE users SET username = ? WHERE id = ?`, nextUsername, user.ID); err != nil {
		return User{}, err
	}

	return User{ID: user.ID, Username: nextUsername, Role: user.Role, TotpEnabled: totpEnabled == "true"}, nil
}

func (s *Store) CreateSession(ctx context.Context, userID string) (string, time.Time, error) {
	sessionID, err := randomHex(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(sessionDays * 24 * time.Hour)
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`, sessionID, userID, expires.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return "", time.Time{}, err
	}
	return sessionID, expires, nil
}

func (s *Store) UserBySession(ctx context.Context, sessionID string) (User, bool, error) {
	if sessionID == "" {
		return User{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT users.id, users.username, users.role, users.totp_enabled FROM sessions JOIN users ON users.id = sessions.user_id WHERE sessions.id = ? AND sessions.expires_at > ?`, sessionID, time.Now().UTC().Format(time.RFC3339))
	user, err := scanUser(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, false, nil
		}
		return User{}, false, err
	}
	return user, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func hashPassword(password string, salt string) (string, error) {
	if salt == "" {
		nextSalt, err := randomHex(16)
		if err != nil {
			return "", err
		}
		salt = nextSalt
	}
	saltBytes := []byte(salt)
	hash := pbkdf2.Key([]byte(password), saltBytes, passwordIterations, 32, sha256.New)
	return fmt.Sprintf("%s:%s", salt, hex.EncodeToString(hash)), nil
}

func verifyPassword(password string, stored string) bool {
	salt, expectedHex, ok := strings.Cut(stored, ":")
	if !ok {
		return false
	}
	candidate, err := hashPassword(password, salt)
	if err != nil {
		return false
	}
	_, candidateHex, ok := strings.Cut(candidate, ":")
	if !ok {
		return false
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	actual, err := hex.DecodeString(candidateHex)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(expected, actual) == 1
}
