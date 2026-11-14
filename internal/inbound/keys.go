// Package inbound imports expenses from forwarded emails (plan 3.5). Each
// user has one address u_<random>@<INBOUND_DOMAIN>; a Cloudflare Email
// Worker (workers/inbound-email) posts every mail sent to it, signed, to
// POST /inbound/email. The mail is read in memory, never stored: fixed rules
// for the banks and wallets it knows, the AI for the rest. What was read
// waits as an open message until the person confirms it in Cargar gasto or
// dismisses it.
package inbound

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// keyID names the encryption key in the rows it sealed, for a future
// rotation (plan §8b).
const keyID = 1

// Keys derives three keys from the two secrets: one signs the Worker's
// requests, one hashes addresses and senders for lookups, one encrypts what
// has to be shown back (AES-256-GCM).
type Keys struct {
	webhook []byte
	index   []byte
	aead    cipher.AEAD
}

// NewKeys takes INBOUND_HMAC_KEY (at least 32 bytes) and INBOUND_ENC_KEY
// (exactly 32 bytes).
func NewKeys(hmacKey, encKey []byte) (*Keys, error) {
	if len(hmacKey) < 32 {
		return nil, errors.New("inbound: INBOUND_HMAC_KEY needs at least 32 bytes")
	}
	if len(encKey) != 32 {
		return nil, errors.New("inbound: INBOUND_ENC_KEY needs exactly 32 bytes")
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keys{webhook: derive(hmacKey, "webhook"), index: derive(hmacKey, "index"), aead: aead}, nil
}

func derive(key []byte, label string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("inbound:" + label))
	return m.Sum(nil)
}

// Hash is the lookup value of an address or a sender (already normalized).
func (k *Keys) Hash(s string) []byte {
	m := hmac.New(sha256.New, k.index)
	m.Write([]byte(s))
	return m.Sum(nil)
}

// Seal encrypts s as nonce||ciphertext.
func (k *Keys) Seal(s string) []byte {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(fmt.Sprintf("inbound: no randomness: %v", err))
	}
	return k.aead.Seal(nonce, nonce, []byte(s), []byte("inbound"))
}

// Open reverses Seal.
func (k *Keys) Open(b []byte) (string, error) {
	n := k.aead.NonceSize()
	if len(b) < n {
		return "", errors.New("inbound: sealed value too short")
	}
	out, err := k.aead.Open(nil, b[:n], b[n:], []byte("inbound"))
	if err != nil {
		return "", fmt.Errorf("inbound: open: %w", err)
	}
	return string(out), nil
}

// Sign is the signature the Worker sends for one mail: HMAC-SHA256 over the
// timestamp, the envelope recipient and sender, and the raw mail.
func (k *Keys) Sign(timestamp, to, from string, raw []byte) []byte {
	m := hmac.New(sha256.New, k.webhook)
	m.Write([]byte(timestamp + "\n" + to + "\n" + from + "\n"))
	m.Write(raw)
	return m.Sum(nil)
}
