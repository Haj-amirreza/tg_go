package legacy

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type vector struct {
	Key    string `json:"key_hex"`
	Nonce  string `json:"nonce_hex"`
	Plain  string `json:"plaintext"`
	Cipher string `json:"php_ciphertext"`
	HMAC   string `json:"php_hmac"`
}

func fixture(t *testing.T) vector {
	t.Helper()
	raw, err := os.ReadFile("testdata/php-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vector
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestPHPCompatibility(t *testing.T) {
	v := fixture(t)
	c, err := New(v.Key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Decrypt(v.Cipher)
	if err != nil || string(plain) != v.Plain {
		t.Fatal("cannot read PHP envelope")
	}
	nonce, _ := hex.DecodeString(v.Nonce)
	wire, err := c.encrypt([]byte(v.Plain), bytes.NewReader(nonce))
	if err != nil || wire != v.Cipher {
		t.Fatal("Go envelope differs from PHP")
	}
	if SignJSON([]byte(v.Plain), v.Key) != v.HMAC || !VerifyJSON([]byte(v.Plain), v.Key, v.HMAC) {
		t.Fatal("textual hex HMAC differs")
	}
}
func TestRejectsCorruption(t *testing.T) {
	v := fixture(t)
	c, _ := New(v.Key)
	bad, _ := New(strings.Repeat("ff", 32))
	if _, err := bad.Decrypt(v.Cipher); err == nil {
		t.Fatal("wrong key accepted")
	}
	for _, wire := range []string{"", "???", v.Cipher[:20], v.Cipher[:len(v.Cipher)-5] + "AAAA="} {
		if _, err := c.Decrypt(wire); err == nil {
			t.Fatal("corrupt wire accepted")
		}
	}
	if VerifyJSON([]byte(v.Plain+" "), v.Key, v.HMAC) || VerifyJSON([]byte(v.Plain), v.Key, "zz") {
		t.Fatal("changed signed bytes accepted")
	}
	if _, err := New("invalid"); err == nil {
		t.Fatal("bad key accepted")
	}
	if _, err := c.encrypt(nil, bytes.NewReader(nil)); err == nil {
		t.Fatal("randomness failure ignored")
	}
}
func TestRoundTripAndPHPReader(t *testing.T) {
	v := fixture(t)
	c, _ := New(v.Key)
	wire, err := c.Encrypt([]byte(v.Plain))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Decrypt(wire)
	if err != nil || string(plain) != v.Plain {
		t.Fatal("roundtrip failed")
	}
	if file := os.Getenv("TEST_GO_CRYPTO_OUTPUT"); file != "" {
		raw, _ := json.Marshal(map[string]string{"ciphertext": wire})
		if err = os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
