// Package legacy reads the existing PHP wire formats without silently canonicalizing signed JSON.
package legacy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
)

type Crypto struct{ aead cipher.AEAD }

func New(keyHex string) (*Crypto, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid application key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid application key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypto{aead}, nil
}
func (c *Crypto) Encrypt(plain []byte) (string, error) { return c.encrypt(plain, rand.Reader) }
func (c *Crypto) encrypt(plain []byte, random io.Reader) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return "", errors.New("secure randomness unavailable")
	}
	sealed := c.aead.Seal(nil, nonce, plain, nil)
	tagAt := len(sealed) - c.aead.Overhead()
	wire := append([]byte{}, nonce...)
	wire = append(wire, sealed[tagAt:]...)
	wire = append(wire, sealed[:tagAt]...)
	return base64.StdEncoding.EncodeToString(wire), nil
}
func (c *Crypto) Decrypt(value string) ([]byte, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(value)
	n := c.aead.NonceSize()
	t := c.aead.Overhead()
	if err != nil || len(raw) < n+t {
		return nil, errors.New("invalid encrypted data")
	}
	sealed := append([]byte{}, raw[n+t:]...)
	sealed = append(sealed, raw[n:n+t]...)
	plain, err := c.aead.Open(nil, raw[:n], sealed, nil)
	if err != nil {
		return nil, errors.New("invalid encrypted data")
	}
	return plain, nil
}

// SignJSON uses the textual hex key and PHP's ORIGINAL encoded bytes. Do not marshal a Go map here.
func SignJSON(raw []byte, keyHex string) string {
	mac := hmac.New(sha256.New, []byte(keyHex))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
func VerifyJSON(raw []byte, keyHex, signature string) bool {
	expected, err := hex.DecodeString(SignJSON(raw, keyHex))
	if err != nil {
		return false
	}
	actual, err := hex.DecodeString(signature)
	return err == nil && hmac.Equal(expected, actual)
}
