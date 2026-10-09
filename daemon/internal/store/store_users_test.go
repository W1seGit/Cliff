package store

import (
	"context"
	"errors"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := hashPassword("correct-horse-battery", "")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("correct-horse-battery", hash) {
		t.Fatal("valid password rejected")
	}
	if verifyPassword("wrong-password-here", hash) {
		t.Fatal("wrong password accepted")
	}
	if verifyPassword("anything", "not-a-hash") {
		t.Fatal("malformed hash accepted")
	}
}

// Hashes created before the switch to x/crypto/pbkdf2 must keep verifying.
// This vector came from the previous hand-written PBKDF2-HMAC-SHA256
// (password "legacy-password-1", salt "0123456789abcdef", 210000 iterations).
func TestPasswordHashBackwardCompatible(t *testing.T) {
	const legacy = "0123456789abcdef:805ff0af4dfd7ea845ff3fe6ebb550c7dba9c22361712b4d18fc386a8dfe2c86"
	if !verifyPassword("legacy-password-1", legacy) {
		t.Fatal("legacy hash no longer verifies")
	}
}

func TestAuthenticateErrors(t *testing.T) {
	db := openTestStore(t)
	if _, err := db.CreateUser(context.Background(), "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Authenticate(context.Background(), "admin", "correct-horse-battery"); err != nil {
		t.Fatalf("valid login failed: %v", err)
	}
	for _, c := range [][2]string{{"admin", "nope-nope-nope"}, {"ghost", "correct-horse-battery"}} {
		if _, err := db.Authenticate(context.Background(), c[0], c[1]); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Authenticate(%q) err = %v, want ErrInvalidCredentials", c[0], err)
		}
	}
}
