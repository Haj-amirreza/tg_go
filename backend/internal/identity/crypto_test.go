package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"encoding/base64"
	"golang.org/x/crypto/argon2"
)

type phpFixture struct {
	Password  string `json:"password"`
	Bcrypt    string `json:"bcrypt"`
	Secret    string `json:"totp_secret"`
	Counter   int64  `json:"totp_counter"`
	Code      string `json:"totp_code"`
	Encrypted string `json:"totp_encrypted"`
}

func vector(t *testing.T) phpFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/php-identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var f phpFixture
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("invalid synthetic fixture")
	}
	return f
}
func TestPHPCredentials(t *testing.T) {
	f := vector(t)
	if !passwordOK(f.Bcrypt, f.Password) || passwordOK(f.Bcrypt, "wrong") || passwordOK(f.Bcrypt, strings.Repeat("a", 73)) {
		t.Fatal("PHP bcrypt mismatch")
	}
	code, err := totpCode(f.Secret, f.Counter)
	if err != nil || code != f.Code {
		t.Fatal("PHP TOTP mismatch")
	}
	now := time.Unix(f.Counter*30, 0)
	if _, err = verifyTOTP(f.Secret, f.Code, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err = verifyTOTP(f.Secret, f.Code, f.Counter, now); err == nil {
		t.Fatal("TOTP replay accepted")
	}
	if _, err = verifyTOTP(f.Secret, "12345", 0, now); err == nil {
		t.Fatal("short code accepted")
	}
}
func TestArgon2AndInvalidHashes(t *testing.T) {
	salt := []byte("synthetic-salt-16")
	sum := argon2.IDKey([]byte("test"), salt, 1, 8192, 1, 32)
	hash := "$argon2id$v=19$m=8192,t=1,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(sum)
	if !passwordOK(hash, "test") || passwordOK(hash, "wrong") {
		t.Fatal("argon2 mismatch")
	}
	for _, bad := range []string{"invalid", strings.Replace(hash, "m=8192", "m=999999999", 1), strings.Replace(hash, "p=1", "p=0", 1)} {
		if passwordOK(bad, "test") {
			t.Fatal("invalid KDF parameters accepted")
		}
	}
}
func signedLaunch(bot, id string, now time.Time) string {
	v := url.Values{"auth_date": {strconv.FormatInt(now.Unix(), 10)}, "query_id": {"synthetic-launch"}, "user": {"{\"id\":" + id + ",\"first_name\":\"مصنوعی\"}"}}
	parts := []string{}
	for _, k := range []string{"auth_date", "query_id", "user"} {
		parts = append(parts, k+"="+v.Get(k))
	}
	k := hmac.New(sha256.New, []byte("WebAppData"))
	k.Write([]byte(bot))
	mac := hmac.New(sha256.New, k.Sum(nil))
	mac.Write([]byte(strings.Join(parts, "\n")))
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}
func TestTelegramSignature(t *testing.T) {
	now := time.Unix(1770000000, 0)
	data := signedLaunch("synthetic-bot-token", "123456", now)
	if u, err := VerifyTelegram(data, "synthetic-bot-token", now); err != nil || u.ID != "123456" {
		t.Fatal("valid signed launch rejected")
	}
	for _, in := range []struct {
		data, token string
		at          time.Time
	}{{data, "wrong", now}, {data, "synthetic-bot-token", now.Add(301 * time.Second)}, {data, "synthetic-bot-token", now.Add(-31 * time.Second)}, {data + "&user=other", "synthetic-bot-token", now}, {strings.Replace(data, "123456", "999999", 1), "synthetic-bot-token", now}} {
		if _, err := VerifyTelegram(in.data, in.token, in.at); err == nil {
			t.Fatal("invalid launch accepted")
		}
	}
}
func TestDelegationPolicy(t *testing.T) {
	a := Actor{Level: 20, Permissions: []string{"admins", "tickets"}}
	if !a.CanDelegate(30, []string{"tickets"}) || a.CanDelegate(20, []string{"tickets"}) || a.CanDelegate(30, []string{"settings"}) || a.Can("unknown") {
		t.Fatal("delegation boundary failed")
	}
	a.Super = true
	if !a.CanDelegate(1, Permissions) || a.CanDelegate(1, []string{"unknown"}) {
		t.Fatal("super allowlist failed")
	}
}
