package identity

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

var errCredentials = errors.New("invalid credentials")
var telegramID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,80}$`)

func passwordOK(hash, password string) bool {
	if len(password) == 0 || len(password) > 72 {
		return false
	}
	if strings.HasPrefix(hash, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	// PHP PASSWORD_ARGON2I/ID hashes; cap resource use before invoking the KDF.
	p := strings.Split(hash, "$")
	if len(p) != 6 || (p[1] != "argon2id" && p[1] != "argon2i") || p[2] != "v=19" {
		return false
	}
	var m, t uint32
	var threads uint8
	if n, err := fmt.Sscanf(p[3], "m=%d,t=%d,p=%d", &m, &t, &threads); err != nil || n != 3 || m < 8 || m > 262144 || t < 1 || t > 10 || threads < 1 || threads > 16 {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(p[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(p[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false
	}
	var actual []byte
	if p[1] == "argon2id" {
		actual = argon2.IDKey([]byte(password), salt, t, m, threads, uint32(len(expected)))
	} else {
		actual = argon2.Key([]byte(password), salt, t, m, threads, uint32(len(expected)))
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func totpCode(secret string, counter int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) < 10 {
		return "", errCredentials
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(counter))
	h := hmac.New(sha1.New, key)
	h.Write(b[:])
	sum := h.Sum(nil)
	o := sum[len(sum)-1] & 15
	n := binary.BigEndian.Uint32(sum[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000), nil
}
func verifyTOTP(secret, code string, last int64, now time.Time) (int64, error) {
	if len(code) != 6 {
		return 0, errCredentials
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, errCredentials
		}
	}
	for _, counter := range []int64{now.Unix()/30 - 1, now.Unix() / 30, now.Unix()/30 + 1} {
		if counter <= last {
			continue
		}
		v, err := totpCode(secret, counter)
		if err == nil && hmac.Equal([]byte(v), []byte(code)) {
			return counter, nil
		}
	}
	return 0, errCredentials
}

type TelegramIdentity struct{ ID, Digest string }

func VerifyTelegram(data, botToken string, now time.Time) (TelegramIdentity, error) {
	fail := func() (TelegramIdentity, error) { return TelegramIdentity{}, errCredentials }
	if data == "" || len(data) > 16384 || botToken == "" {
		return fail()
	}
	fields, err := url.ParseQuery(data)
	if err != nil {
		return fail()
	}
	lines := []string{}
	for k, v := range fields {
		if len(v) != 1 || !regexp.MustCompile(`^[a-z_]+$`).MatchString(k) {
			return fail()
		}
		if k != "hash" {
			lines = append(lines, k+"="+v[0])
		}
	}
	sig, err := hex.DecodeString(fields.Get("hash"))
	if err != nil || len(sig) != 32 {
		return fail()
	}
	sort.Strings(lines)
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return fail()
	}
	stamp, err := strconv.ParseInt(fields.Get("auth_date"), 10, 64)
	if err != nil || stamp < now.Unix()-300 || stamp > now.Unix()+30 {
		return fail()
	}
	var u struct {
		ID    json.Number `json:"id"`
		IsBot bool        `json:"is_bot"`
	}
	if json.Unmarshal([]byte(fields.Get("user")), &u) != nil || !telegramID.MatchString(u.ID.String()) || u.IsBot {
		return fail()
	}
	return TelegramIdentity{u.ID.String(), hex.EncodeToString(sig)}, nil
}
