// Package totp implements time-based one-time passwords (RFC 6238) as used by
// authenticator apps: HMAC-SHA1, 30-second steps, 6 digits.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	stepSeconds = 30
	digits      = 6
	// skew is how many steps either side of now are accepted, to forgive a
	// slightly wrong clock on the phone.
	skew = 1
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new random secret, base32 encoded.
func GenerateSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return encoding.EncodeToString(raw), nil
}

// URI builds the otpauth:// link authenticator apps read from a QR code.
func URI(secret string, issuer string, account string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(digits))
	query.Set("period", fmt.Sprint(stepSeconds))
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// Code returns the code for the secret at the given time step.
func Code(secret string, step int64) (string, error) {
	key, err := encoding.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	modulus := uint32(1)
	for i := 0; i < digits; i++ {
		modulus *= 10
	}
	return fmt.Sprintf("%0*d", digits, value%modulus), nil
}

// Step is the 30-second step number for a time.
func Step(at time.Time) int64 {
	return at.Unix() / stepSeconds
}

// Verify checks a code against the secret near the given time. It returns the
// step the code matched, so the caller can refuse to accept the same step
// twice, and whether the code was valid.
func Verify(secret string, code string, at time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != digits {
		return 0, false
	}
	current := Step(at)
	for offset := int64(-skew); offset <= skew; offset++ {
		expected, err := Code(secret, current+offset)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return current + offset, true
		}
	}
	return 0, false
}
