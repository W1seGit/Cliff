package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

// Account roles. An admin can do everything; a member can only do what has
// been granted to them on specific servers.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// AllServers is the server id used in a grant that applies to every server.
const AllServers = "*"

// Permissions a member can be granted on a server.
const (
	PermView     = "view"     // see the server, its status and join details
	PermConsole  = "console"  // read the console and run commands
	PermPower    = "power"    // start, stop and restart
	PermFiles    = "files"    // browse and edit files
	PermMods     = "mods"     // manage mods, plugins and modpacks
	PermPlayers  = "players"  // ops, whitelist and bans
	PermWorlds   = "worlds"   // manage worlds and datapacks
	PermBackups  = "backups"  // create, restore and delete backups
	PermSettings = "settings" // edit the server profile, properties and upgrades
)

// AllPermissions lists every permission, in the order the dashboard shows them.
var AllPermissions = []string{PermView, PermConsole, PermPower, PermFiles, PermMods, PermPlayers, PermWorlds, PermBackups, PermSettings}

// ValidPermission reports whether name is a known permission.
func ValidPermission(name string) bool {
	for _, permission := range AllPermissions {
		if permission == name {
			return true
		}
	}
	return false
}

// Account is a user with the details only admins see.
type Account struct {
	User
	Permissions map[string][]string `json:"permissions"`
}

const userColumns = `id, username, role, totp_enabled`

func scanUser(row rowScanner) (User, error) {
	var user User
	var totpEnabled string
	if err := row.Scan(&user.ID, &user.Username, &user.Role, &totpEnabled); err != nil {
		return User{}, err
	}
	user.TotpEnabled = totpEnabled == "true"
	return user, nil
}

func (s *Store) GetUser(ctx context.Context, id string) (User, bool, error) {
	user, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, false, nil
		}
		return User{}, false, err
	}
	return user, true, nil
}

func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	accounts := make([]Account, 0, len(users))
	for _, user := range users {
		grants, err := s.Permissions(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, Account{User: user, Permissions: grants})
	}
	return accounts, nil
}

func validateNewCredentials(username string, password string) error {
	if len(strings.TrimSpace(username)) < 3 {
		return errors.New("Username must be at least 3 characters")
	}
	if len(password) < 10 {
		return errors.New("Password must be at least 10 characters")
	}
	return nil
}

// CreateAccount adds a user after the first one (CreateUser makes the first).
func (s *Store) CreateAccount(ctx context.Context, username string, password string, role string) (User, error) {
	if err := validateNewCredentials(username, password); err != nil {
		return User{}, err
	}
	if role != RoleAdmin {
		role = RoleMember
	}
	username = strings.TrimSpace(username)
	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, username).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	if existing != "" {
		return User{}, errors.New("Username is already in use")
	}
	id, err := randomHex(16)
	if err != nil {
		return User{}, err
	}
	passwordHash, err := hashPassword(password, "")
	if err != nil {
		return User{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, username, passwordHash, role, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Role: role}, nil
}

func (s *Store) adminCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&count)
	return count, err
}

// ErrLastAdmin is returned when a change would leave no administrator.
var ErrLastAdmin = errors.New("There must always be at least one admin")

func (s *Store) SetAccountRole(ctx context.Context, id string, role string) error {
	if role != RoleAdmin && role != RoleMember {
		return errors.New("Unknown role")
	}
	user, ok, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("Account not found")
	}
	if user.Role == RoleAdmin && role != RoleAdmin {
		if count, err := s.adminCount(ctx); err != nil {
			return err
		} else if count <= 1 {
			return ErrLastAdmin
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id)
	return err
}

// SetAccountPassword resets a password and signs the user out everywhere.
func (s *Store) SetAccountPassword(ctx context.Context, id string, password string) error {
	if len(password) < 10 {
		return errors.New("Password must be at least 10 characters")
	}
	hash, err := hashPassword(password, "")
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return errors.New("Account not found")
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	return err
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	user, ok, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("Account not found")
	}
	if user.Role == RoleAdmin {
		if count, err := s.adminCount(ctx); err != nil {
			return err
		} else if count <= 1 {
			return ErrLastAdmin
		}
	}
	for _, statement := range []string{
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM user_permissions WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := s.db.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	return nil
}

// Permissions returns a user's grants as server id to permission names.
func (s *Store) Permissions(ctx context.Context, userID string) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id, permissions FROM user_permissions WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := map[string][]string{}
	for rows.Next() {
		var serverID, permissions string
		if err := rows.Scan(&serverID, &permissions); err != nil {
			return nil, err
		}
		grants[serverID] = splitEvents(permissions)
	}
	return grants, rows.Err()
}

// SetPermissions replaces all of a user's grants. Unknown permissions are
// dropped, and a grant that does not include "view" gets it, since nothing
// else is usable without it.
func (s *Store) SetPermissions(ctx context.Context, userID string, grants map[string][]string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM user_permissions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for serverID, requested := range grants {
		set := map[string]bool{}
		for _, name := range requested {
			if ValidPermission(name) {
				set[name] = true
			}
		}
		if len(set) == 0 || strings.TrimSpace(serverID) == "" {
			continue
		}
		set[PermView] = true
		names := make([]string, 0, len(set))
		for _, name := range AllPermissions {
			if set[name] {
				names = append(names, name)
			}
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO user_permissions (user_id, server_id, permissions) VALUES (?, ?, ?)`, userID, serverID, strings.Join(names, ",")); err != nil {
			return err
		}
	}
	return nil
}

// Allowed reports whether the user may do perm on the server. Admins may do
// anything; a member needs a grant for that server or for all servers.
func (s *Store) Allowed(ctx context.Context, user User, serverID string, perm string) (bool, error) {
	if user.Role == RoleAdmin {
		return true, nil
	}
	grants, err := s.Permissions(ctx, user.ID)
	if err != nil {
		return false, err
	}
	return grantsAllow(grants, serverID, perm), nil
}

func grantsAllow(grants map[string][]string, serverID string, perm string) bool {
	for _, key := range []string{serverID, AllServers} {
		for _, name := range grants[key] {
			if name == perm {
				return true
			}
		}
	}
	return false
}

// GrantsAllow is grantsAllow for callers that already loaded the grants.
func GrantsAllow(grants map[string][]string, serverID string, perm string) bool {
	return grantsAllow(grants, serverID, perm)
}

// --- two-factor authentication ----------------------------------------------

// TOTPState is what login needs to check a two-factor code.
type TOTPState struct {
	Secret   string
	Enabled  bool
	LastStep int64
}

func (s *Store) TOTP(ctx context.Context, userID string) (TOTPState, error) {
	var state TOTPState
	var enabled string
	err := s.db.QueryRowContext(ctx, `SELECT totp_secret, totp_enabled, totp_last_step FROM users WHERE id = ?`, userID).Scan(&state.Secret, &enabled, &state.LastStep)
	state.Enabled = enabled == "true"
	return state, err
}

// BeginTOTP stores a new secret that is not yet enforced at login.
func (s *Store) BeginTOTP(ctx context.Context, userID string, secret string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret = ?, totp_enabled = 'false', totp_last_step = 0, totp_recovery = '' WHERE id = ?`, secret, userID)
	return err
}

// EnableTOTP turns two-factor login on and stores the recovery codes (hashed).
func (s *Store) EnableTOTP(ctx context.Context, userID string, step int64, recoveryHashes []string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_enabled = 'true', totp_last_step = ?, totp_recovery = ? WHERE id = ?`, step, strings.Join(recoveryHashes, ","), userID)
	return err
}

func (s *Store) DisableTOTP(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret = '', totp_enabled = 'false', totp_last_step = 0, totp_recovery = '' WHERE id = ?`, userID)
	return err
}

// ClaimTOTPStep records that a code for this step was used. It returns false
// when the step (or a later one) was already used, so a code works only once.
func (s *Store) ClaimTOTPStep(ctx context.Context, userID string, step int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// HashRecoveryCode is how recovery codes are stored.
func HashRecoveryCode(code string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// NewRecoveryCodes makes the one-time codes shown when two-factor is enabled,
// along with their hashes for storage.
func NewRecoveryCodes(count int) (plain []string, hashes []string, err error) {
	for i := 0; i < count; i++ {
		raw, err := randomHex(5)
		if err != nil {
			return nil, nil, err
		}
		code := raw[:5] + "-" + raw[5:]
		plain = append(plain, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return plain, hashes, nil
}

// UseRecoveryCode consumes a recovery code. It reports whether it was valid.
func (s *Store) UseRecoveryCode(ctx context.Context, userID string, code string) (bool, error) {
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT totp_recovery FROM users WHERE id = ?`, userID).Scan(&stored); err != nil {
		return false, err
	}
	hash := HashRecoveryCode(code)
	remaining := []string{}
	found := false
	for _, candidate := range splitEvents(stored) {
		if !found && candidate == hash {
			found = true
			continue
		}
		remaining = append(remaining, candidate)
	}
	if !found {
		return false, nil
	}
	sort.Strings(remaining)
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_recovery = ? WHERE id = ?`, strings.Join(remaining, ","), userID)
	return err == nil, err
}
