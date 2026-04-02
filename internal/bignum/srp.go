// Package bignum provides big number arithmetic for SRP authentication.
//
// All constants and operations follow the SRP-6a specification used by
// Firebird 3.0+. The 1024-bit prime N, generator g=2, and multiplier k
// are identical across all Firebird SRP variants (Srp, Srp256).
package bignum

import (
	"crypto/rand"
	"math/big"
)

// SRP key and salt sizes in bytes.
const (
	KeySize  = 128 // 1024-bit keys
	SaltSize = 32  // 256-bit salt
)

var (
	// N is the 1024-bit safe prime used by Firebird SRP.
	N = mustParseBigInt(
		"E67D2E994B2F900C3F41F08F5BB2627ED0D49EE1FE767A52EFCD565CD6E768812C3E1E9CE8F0A8BEA6CB13CD29DDEBF7A96D4A93B55D488DF099A15C89DCB0640738EB2CBDD9A8F7BAB561AB1B0DC1C6CDABF303264A08D1BCA932D1F1EE428B619D970F342ABA9A65793B8B2F041AE5364350C16F735F56ECBCA87BD57B29E7",
		16,
	)

	// G is the generator (g=2).
	G = big.NewInt(2)

	// K is the SRP multiplier: H(N) xor H(g) as defined by Firebird.
	K = mustParseBigInt("1277432915985975349439481660349303019122249719989", 10)
)

func mustParseBigInt(s string, base int) *big.Int {
	n, ok := new(big.Int).SetString(s, base)
	if !ok {
		panic("bignum: invalid constant: " + s)
	}
	return n
}

// Pad zero-pads a big.Int to KeySize bytes (big-endian) using FillBytes.
// The result is always exactly KeySize bytes, with leading zeros as needed.
// Uses a stack-allocated buffer to avoid heap allocation.
func Pad(v *big.Int) []byte {
	var buf [KeySize]byte
	v.FillBytes(buf[:])
	return buf[:]
}

// BigToBytes returns the big-endian representation of v with leading zeros stripped.
func BigToBytes(v *big.Int) []byte {
	return v.Bytes()
}

// BytesToBig interprets b as a big-endian unsigned integer.
func BytesToBig(b []byte) *big.Int {
	return new(big.Int).SetBytes(b)
}

// GeneratePrivateKey generates a cryptographically random 128-bit private key.
func GeneratePrivateKey() (*big.Int, error) {
	b := make([]byte, 16) // 128 bits
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

// ClientPublicKey computes A = g^a mod N.
func ClientPublicKey(privateKey *big.Int) *big.Int {
	return new(big.Int).Exp(G, privateKey, N)
}
