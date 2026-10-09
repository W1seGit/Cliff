package store

import (
	"context"
	"errors"
	"testing"
)

func firstAdmin(t *testing.T, db *Store) User {
	t.Helper()
	user, err := db.CreateUser(context.Background(), "owner", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != RoleAdmin {
		t.Fatalf("the first user must be an admin, got %q", user.Role)
	}
	return user
}

func TestAccountsAndLastAdminGuard(t *testing.T) {
	db := openTestStore(t)
	ctx := context.Background()
	owner := firstAdmin(t, db)

	member, err := db.CreateAccount(ctx, "friend", "another long password", RoleMember)
	if err != nil || member.Role != RoleMember {
		t.Fatalf("could not add a member: %+v (%v)", member, err)
	}
	if _, err := db.CreateAccount(ctx, "friend", "another long password", RoleMember); err == nil {
		t.Fatal("duplicate usernames must be refused")
	}
	if _, err := db.CreateAccount(ctx, "x", "another long password", RoleMember); err == nil {
		t.Fatal("short usernames must be refused")
	}

	if err := db.SetAccountRole(ctx, owner.ID, RoleMember); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the only admin should fail, got %v", err)
	}
	if err := db.DeleteAccount(ctx, owner.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("deleting the only admin should fail, got %v", err)
	}
	if err := db.SetAccountRole(ctx, member.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAccount(ctx, owner.ID); err != nil {
		t.Fatalf("with a second admin the first may go: %v", err)
	}
	accounts, err := db.ListAccounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].Username != "friend" {
		t.Fatalf("unexpected accounts %+v (%v)", accounts, err)
	}
}

func TestPermissionsGrantAndAllow(t *testing.T) {
	db := openTestStore(t)
	ctx := context.Background()
	admin := firstAdmin(t, db)
	member, err := db.CreateAccount(ctx, "friend", "another long password", RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	if ok, _ := db.Allowed(ctx, admin, "srv_a", PermFiles); !ok {
		t.Fatal("admins may do anything")
	}
	if ok, _ := db.Allowed(ctx, member, "srv_a", PermView); ok {
		t.Fatal("a member has no access until granted")
	}

	grants := map[string][]string{
		"srv_a":    {PermConsole, "not-a-permission"},
		AllServers: {PermBackups},
		"srv_b":    {},
	}
	if err := db.SetPermissions(ctx, member.ID, grants); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		server, perm string
		want         bool
	}{
		{"srv_a", PermConsole, true},
		{"srv_a", PermView, true}, // implied by any grant
		{"srv_a", PermFiles, false},
		{"srv_a", PermBackups, true}, // from the all-servers grant
		{"srv_z", PermBackups, true},
		{"srv_z", PermConsole, false},
		{"srv_b", PermFiles, false}, // an empty grant adds nothing; only the all-servers grant applies
	}
	for _, tc := range cases {
		if got, _ := db.Allowed(ctx, member, tc.server, tc.perm); got != tc.want {
			t.Errorf("%s/%s: got %v, want %v", tc.server, tc.perm, got, tc.want)
		}
	}

	if err := db.DeleteAccount(ctx, member.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := db.Permissions(ctx, member.ID); len(left) != 0 {
		t.Fatalf("deleting an account should remove its grants: %v", left)
	}
}

func TestTOTPStateAndRecoveryCodes(t *testing.T) {
	db := openTestStore(t)
	ctx := context.Background()
	user := firstAdmin(t, db)

	if err := db.BeginTOTP(ctx, user.ID, "SECRET"); err != nil {
		t.Fatal(err)
	}
	if state, _ := db.TOTP(ctx, user.ID); state.Enabled || state.Secret != "SECRET" {
		t.Fatalf("a pending secret must not be enforced yet: %+v", state)
	}
	plain, hashes, err := NewRecoveryCodes(3)
	if err != nil || len(plain) != 3 {
		t.Fatal(err)
	}
	if err := db.EnableTOTP(ctx, user.ID, 100, hashes); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := db.GetUser(ctx, user.ID); !got.TotpEnabled {
		t.Fatal("the user should now require two-factor")
	}

	if ok, _ := db.ClaimTOTPStep(ctx, user.ID, 100); ok {
		t.Fatal("the step used to enable two-factor cannot be used again")
	}
	if ok, _ := db.ClaimTOTPStep(ctx, user.ID, 101); !ok {
		t.Fatal("a later step is fine")
	}
	if ok, _ := db.ClaimTOTPStep(ctx, user.ID, 101); ok {
		t.Fatal("a step can only be claimed once")
	}

	if ok, _ := db.UseRecoveryCode(ctx, user.ID, "wrong-code"); ok {
		t.Fatal("a wrong recovery code was accepted")
	}
	if ok, _ := db.UseRecoveryCode(ctx, user.ID, plain[0]); !ok {
		t.Fatal("a valid recovery code was refused")
	}
	if ok, _ := db.UseRecoveryCode(ctx, user.ID, plain[0]); ok {
		t.Fatal("a recovery code works only once")
	}

	if err := db.DisableTOTP(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if state, _ := db.TOTP(ctx, user.ID); state.Enabled || state.Secret != "" {
		t.Fatalf("two-factor was not cleared: %+v", state)
	}
}
