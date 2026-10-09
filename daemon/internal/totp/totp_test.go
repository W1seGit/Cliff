package totp

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors use the ASCII secret "12345678901234567890"
// (base32 GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ); the 6-digit codes are the last six
// digits of the published 8-digit values.
func TestCodeMatchesRFC6238Vectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := map[int64]string{
		59:         "287082",
		1111111109: "081804",
		1234567890: "005924",
		2000000000: "279037",
	}
	for seconds, want := range cases {
		got, err := Code(secret, Step(time.Unix(seconds, 0)))
		if err != nil || got != want {
			t.Errorf("t=%d: got %q (%v), want %q", seconds, got, err, want)
		}
	}
}

func TestVerifyAcceptsNearbyStepsOnly(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	code, _ := Code(secret, Step(now))

	if step, ok := Verify(secret, code, now); !ok || step != Step(now) {
		t.Fatalf("the current code should verify (step %d, ok=%v)", step, ok)
	}
	if _, ok := Verify(secret, code[:3]+" "+code[3:], now); !ok {
		t.Fatal("a code typed with a space should verify")
	}
	if _, ok := Verify(secret, code, now.Add(30*time.Second)); !ok {
		t.Fatal("one step of clock skew is allowed")
	}
	if _, ok := Verify(secret, code, now.Add(5*time.Minute)); ok {
		t.Fatal("an old code must be refused")
	}
	if _, ok := Verify(secret, "12345", now); ok {
		t.Fatal("a short code must be refused")
	}
}

func TestURI(t *testing.T) {
	uri := URI("ABC234", "Cliff", "alex")
	for _, part := range []string{"otpauth://totp/Cliff:alex?", "secret=ABC234", "issuer=Cliff"} {
		if !strings.Contains(uri, part) {
			t.Errorf("%q is missing %q", uri, part)
		}
	}
}
