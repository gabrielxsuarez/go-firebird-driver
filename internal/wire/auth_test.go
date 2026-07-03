package wire

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/gabrielxsuarez/go-firebird-driver/internal/bignum"
)

// --- SRP round-trip: client and server derive the same session key ---

func TestSRPRoundTrip(t *testing.T) {
	for _, plugin := range []string{PluginSrp, PluginSrp256} {
		t.Run(plugin, func(t *testing.T) {
			user := "SYSDBA"
			password := "masterkey"

			// Client generates ephemeral key pair
			clientPrivate, err := bignum.GeneratePrivateKey()
			if err != nil {
				t.Fatal(err)
			}
			clientPublic := bignum.ClientPublicKey(clientPrivate)

			// Server generates salt and ephemeral key pair
			salt := make([]byte, bignum.SaltSize)
			if _, err := rand.Read(salt); err != nil {
				t.Fatal(err)
			}
			v := verifier(user, password, salt)

			serverPrivate, err := bignum.GeneratePrivateKey()
			if err != nil {
				t.Fatal(err)
			}
			serverPub := serverPublicKey(v, serverPrivate)

			// Both derive session key
			clientK := clientSessionKey(user, password, salt, clientPublic, serverPub, clientPrivate)
			serverK := serverSessionKey(user, password, salt, clientPublic, serverPub, serverPrivate)

			if !bytes.Equal(clientK, serverK) {
				t.Fatalf("session keys differ:\n  client: %x\n  server: %x", clientK, serverK)
			}

			// Verify session key is 20 bytes (SHA-1 output)
			if len(clientK) != 20 {
				t.Fatalf("expected 20-byte session key, got %d", len(clientK))
			}
		})
	}
}

// --- SRP round-trip with full ComputeProof flow ---

func TestSRPClientComputeProof(t *testing.T) {
	for _, plugin := range []string{PluginSrp, PluginSrp256} {
		t.Run(plugin, func(t *testing.T) {
			user := "SYSDBA"
			password := "masterkey"

			// Client side
			client, err := newSRPClient(plugin, user, password)
			if err != nil {
				t.Fatal(err)
			}

			// Server side: generate challenge
			salt := make([]byte, bignum.SaltSize)
			if _, err := rand.Read(salt); err != nil {
				t.Fatal(err)
			}
			v := verifier(strings.ToUpper(user), password, salt)
			serverPriv, err := bignum.GeneratePrivateKey()
			if err != nil {
				t.Fatal(err)
			}
			serverPub := serverPublicKey(v, serverPriv)

			// Build server auth data (as Firebird would send it)
			serverData := buildServerAuthData(salt, serverPub)

			// Client computes proof
			proofHex, err := client.ComputeProof(serverData)
			if err != nil {
				t.Fatal(err)
			}

			// Verify proof is valid hex
			if _, err := hex.DecodeString(string(proofHex)); err != nil {
				t.Fatalf("proof is not valid hex: %s", proofHex)
			}

			// Verify session keys match
			serverK := serverSessionKey(strings.ToUpper(user), password, salt, client.publicKey, serverPub, serverPriv)
			if !bytes.Equal(client.SessionKey(), serverK) {
				t.Fatalf("session keys differ after ComputeProof")
			}
		})
	}
}

// --- Deterministic test with known private key ---

func TestSRPDeterministic(t *testing.T) {
	user := "SYSDBA"
	password := "masterkey"

	// Use fixed private keys for reproducibility
	clientPriv := big.NewInt(0).SetBytes([]byte{
		0x60, 0x97, 0x55, 0x27, 0x03, 0x5C, 0xF2, 0xAD,
		0x19, 0x89, 0x80, 0x6F, 0x04, 0x07, 0x21, 0x0B,
	})
	serverPriv := big.NewInt(0).SetBytes([]byte{
		0xC8, 0x1E, 0xDC, 0x04, 0xE2, 0x76, 0x2A, 0x56,
		0xAF, 0xD5, 0x29, 0xDD, 0xDA, 0x2D, 0x43, 0x93,
	})
	salt := bytes.Repeat([]byte{0xAB}, bignum.SaltSize)

	clientPub := bignum.ClientPublicKey(clientPriv)
	v := verifier(user, password, salt)
	serverPub := serverPublicKey(v, serverPriv)

	clientK := clientSessionKey(user, password, salt, clientPub, serverPub, clientPriv)
	serverK := serverSessionKey(user, password, salt, clientPub, serverPub, serverPriv)

	if !bytes.Equal(clientK, serverK) {
		t.Fatalf("deterministic session keys differ:\n  client: %x\n  server: %x", clientK, serverK)
	}

	// Compute proof for both plugins — session key is the same
	m1Srp := clientProof(PluginSrp, user, salt, clientPub, serverPub, clientK)
	m1Srp256 := clientProof(PluginSrp256, user, salt, clientPub, serverPub, clientK)

	// Srp proof is 20 bytes (SHA-1), Srp256 proof is 32 bytes (SHA-256)
	if len(m1Srp) != 20 {
		t.Fatalf("Srp proof: expected 20 bytes, got %d", len(m1Srp))
	}
	if len(m1Srp256) != 32 {
		t.Fatalf("Srp256 proof: expected 32 bytes, got %d", len(m1Srp256))
	}

	// Proofs should be different (different hash algorithms)
	if bytes.Equal(m1Srp, m1Srp256[:20]) {
		t.Fatal("Srp and Srp256 proofs should differ")
	}

	// Run again — deterministic result
	clientK2 := clientSessionKey(user, password, salt, clientPub, serverPub, clientPriv)
	if !bytes.Equal(clientK, clientK2) {
		t.Fatal("session key not deterministic")
	}
}

// --- Server auth data parsing ---

func TestParseServerAuthData(t *testing.T) {
	// Build known auth data
	salt := bytes.Repeat([]byte{0xDE}, 32)
	serverPub := big.NewInt(0).SetBytes(bytes.Repeat([]byte{0xFF}, 64))
	data := buildServerAuthData(salt, serverPub)

	parsedSalt, parsedPub, err := parseServerAuthData(data)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(parsedSalt, salt) {
		t.Fatalf("salt mismatch:\n  want: %x\n  got:  %x", salt, parsedSalt)
	}

	if serverPub.Cmp(parsedPub) != 0 {
		t.Fatalf("server public key mismatch:\n  want: %x\n  got:  %x", serverPub, parsedPub)
	}
}

func TestParseServerAuthDataTooShort(t *testing.T) {
	_, _, err := parseServerAuthData([]byte{0, 0})
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestParseServerAuthDataTruncated(t *testing.T) {
	// Salt length says 32 but only 10 bytes of data
	data := []byte{32, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	_, _, err := parseServerAuthData(data)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestParseServerAuthDataInvalidHex(t *testing.T) {
	// Valid structure but garbage hex for B
	data := make([]byte, 0, 40)
	data = append(data, 2, 0, 0xAB, 0xCD, 4, 0) // salt: 2 bytes + key len: 4
	data = append(data, "ZZZZ"...)              // invalid hex
	_, _, err := parseServerAuthData(data)
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

// --- Multi-part CNCT_specific_data encoding ---

func TestSpecificDataShort(t *testing.T) {
	// Key shorter than 254 bytes
	hexKey := strings.Repeat("A", 100)
	buf := appendSpecificData(nil, hexKey)

	// Expected: [0x07][101][0][data]
	if len(buf) != 3+100 {
		t.Fatalf("expected %d bytes, got %d", 3+100, len(buf))
	}
	if buf[0] != CnctSpecificData {
		t.Fatalf("expected tag 0x07, got 0x%02x", buf[0])
	}
	if buf[1] != 101 { // len + 1
		t.Fatalf("expected length 101, got %d", buf[1])
	}
	if buf[2] != 0 { // sequence 0
		t.Fatalf("expected sequence 0, got %d", buf[2])
	}
	if string(buf[3:]) != hexKey {
		t.Fatal("data mismatch")
	}
}

func TestSpecificDataExact254(t *testing.T) {
	hexKey := strings.Repeat("B", 254)
	buf := appendSpecificData(nil, hexKey)

	// Single part: [0x07][255][0][254 bytes]
	if len(buf) != 3+254 {
		t.Fatalf("expected %d bytes, got %d", 3+254, len(buf))
	}
	if buf[1] != 255 { // 254 + 1
		t.Fatalf("expected length 255, got %d", buf[1])
	}
}

func TestSpecificDataMultiPart(t *testing.T) {
	// 329 bytes → part 0 (254) + part 1 (75)
	hexKey := strings.Repeat("C", 329)
	buf := appendSpecificData(nil, hexKey)

	// Part 0: [0x07][255][0][254 bytes]
	expectedSize := (3 + 254) + (3 + 75)
	if len(buf) != expectedSize {
		t.Fatalf("expected %d bytes, got %d", expectedSize, len(buf))
	}

	// Verify part 0
	if buf[0] != CnctSpecificData || buf[1] != 255 || buf[2] != 0 {
		t.Fatalf("part 0 header: %02x %02x %02x", buf[0], buf[1], buf[2])
	}
	part0Data := string(buf[3 : 3+254])
	if part0Data != strings.Repeat("C", 254) {
		t.Fatal("part 0 data mismatch")
	}

	// Verify part 1
	off := 3 + 254
	if buf[off] != CnctSpecificData || buf[off+1] != 76 || buf[off+2] != 1 {
		t.Fatalf("part 1 header: %02x %02x %02x", buf[off], buf[off+1], buf[off+2])
	}
	part1Data := string(buf[off+3:])
	if part1Data != strings.Repeat("C", 75) {
		t.Fatal("part 1 data mismatch")
	}
}

func TestSpecificDataThreeParts(t *testing.T) {
	// 600 bytes → 254 + 254 + 92
	hexKey := strings.Repeat("D", 600)
	buf := appendSpecificData(nil, hexKey)

	expectedSize := (3 + 254) + (3 + 254) + (3 + 92)
	if len(buf) != expectedSize {
		t.Fatalf("expected %d bytes, got %d", expectedSize, len(buf))
	}

	// Verify sequence numbers
	off := 0
	for seq := byte(0); seq < 3; seq++ {
		if buf[off+2] != seq {
			t.Fatalf("expected sequence %d at offset %d, got %d", seq, off, buf[off+2])
		}
		chunkSize := int(buf[off+1]) - 1
		off += 3 + chunkSize
	}
}

// --- User identification block ---

func TestBuildUserIdentBlock(t *testing.T) {
	hexKey := strings.Repeat("AB", 50) // 100-char hex key
	buf := buildUserIdentBlock("gabriel", "myhost", "SYSDBA", PluginSrp256, hexKey, DefaultPluginList, WireCryptEnabled)

	// Verify all tags are present in order
	tags := []byte{}
	i := 0
	for i < len(buf) {
		tag := buf[i]
		tags = append(tags, tag)
		length := int(buf[i+1])
		i += 2 + length
	}

	expectedTags := []byte{CnctUser, CnctHost, CnctUserVerification, CnctLogin, CnctPluginName, CnctSpecificData, CnctPluginList, CnctClientCrypt}
	if !bytes.Equal(tags, expectedTags) {
		t.Fatalf("tag order:\n  want: %v\n  got:  %v", expectedTags, tags)
	}
}

// --- PublicKeyHex ---

func TestPublicKeyHex(t *testing.T) {
	privKey := big.NewInt(42)
	client := newSRPClientWithKey(PluginSrp256, "SYSDBA", "masterkey", privKey)

	hexA := client.PublicKeyHex()

	// Verify it's valid uppercase hex
	if hexA != strings.ToUpper(hexA) {
		t.Fatal("PublicKeyHex should be uppercase")
	}
	decoded, err := hex.DecodeString(hexA)
	if err != nil {
		t.Fatalf("invalid hex: %v", err)
	}

	// Verify it corresponds to g^42 mod N
	expected := bignum.ClientPublicKey(privKey)
	actual := new(big.Int).SetBytes(decoded)
	if expected.Cmp(actual) != 0 {
		t.Fatal("PublicKeyHex does not match expected value")
	}
}

// --- Plugin switch ---

func TestSetPlugin(t *testing.T) {
	client := newSRPClientWithKey(PluginSrp256, "SYSDBA", "masterkey", big.NewInt(1))
	if client.pluginName != PluginSrp256 {
		t.Fatalf("expected %s, got %s", PluginSrp256, client.pluginName)
	}
	client.SetPlugin(PluginSrp)
	if client.pluginName != PluginSrp {
		t.Fatalf("expected %s, got %s", PluginSrp, client.pluginName)
	}
}

// --- User is uppercased ---

func TestUserUppercased(t *testing.T) {
	client := newSRPClientWithKey(PluginSrp256, "sysdba", "masterkey", big.NewInt(1))
	if client.user != "SYSDBA" {
		t.Fatalf("expected SYSDBA, got %s", client.user)
	}
}

// --- Crypt preference encoding ---

func TestCryptPreference(t *testing.T) {
	buf := appendCryptPreference(nil, WireCryptRequired)
	// [0x0B][4][2, 0, 0, 0]
	if len(buf) != 6 {
		t.Fatalf("expected 6 bytes, got %d", len(buf))
	}
	if buf[0] != CnctClientCrypt {
		t.Fatalf("expected tag 0x0B, got 0x%02x", buf[0])
	}
	if buf[1] != 4 {
		t.Fatalf("expected length 4, got %d", buf[1])
	}
	if buf[2] != 2 || buf[3] != 0 || buf[4] != 0 || buf[5] != 0 {
		t.Fatalf("expected LE value [2,0,0,0], got %v", buf[2:6])
	}
}

// --- buildServerAuthData round-trip ---

func TestBuildServerAuthDataRoundTrip(t *testing.T) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}

	// Use a realistic-sized server public key
	serverPriv, err := bignum.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	v := verifier("SYSDBA", "masterkey", salt)
	serverPub := serverPublicKey(v, serverPriv)

	data := buildServerAuthData(salt, serverPub)
	parsedSalt, parsedPub, err := parseServerAuthData(data)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(parsedSalt, salt) {
		t.Fatal("salt round-trip failed")
	}
	if serverPub.Cmp(parsedPub) != 0 {
		t.Fatal("server public key round-trip failed")
	}
}

// Simulación del lado servidor de SRP: solo la usan los tests para verificar
// el protocolo offline (el driver real nunca computa el lado servidor).

// serverPublicKey computes B = (k*v + g^b) mod N, where v is the stored verifier.
func serverPublicKey(verifier, serverPrivate *big.Int) *big.Int {
	// gb = g^b mod N
	gb := new(big.Int).Exp(bignum.G, serverPrivate, bignum.N)
	// SRP formula: kv = (k * v) % N
	kv := new(big.Int).Mul(bignum.K, verifier)
	kv.Mod(kv, bignum.N)
	// SRP formula: B = (kv + gb) % N
	B := new(big.Int).Add(kv, gb)
	B.Mod(B, bignum.N)
	return B
}

// serverSessionKey computes the server-side session key for verification.
// K = SHA1((A * v^u)^b mod N)
func serverSessionKey(user, password string, salt []byte, keyA, keyB, serverPrivate *big.Int) []byte {
	u := scramble(keyA, keyB)
	v := verifier(user, password, salt)
	// vu = v^u mod N
	vu := new(big.Int).Exp(v, u, bignum.N)
	// SRP formula: avu = (A * vu) % N
	avu := new(big.Int).Mul(keyA, vu)
	avu.Mod(avu, bignum.N)
	// S = avu^b mod N
	sessionSecret := new(big.Int).Exp(avu, serverPrivate, bignum.N)
	// K = SHA1(S)
	h := sha1.New()
	h.Write(sessionSecret.Bytes())
	return h.Sum(nil)
}

// buildServerAuthData constructs the server auth challenge response:
//
//	[saltLen LE16][salt][keyLen LE16][B as hex string]
func buildServerAuthData(salt []byte, serverPublic *big.Int) []byte {
	hexB := toUpperHex(bignum.BigToBytes(serverPublic))
	data := make([]byte, 0, 2+len(salt)+2+len(hexB))
	// Salt length (LE16)
	data = append(data, byte(len(salt)), byte(len(salt)>>8))
	data = append(data, salt...)
	// Key length (LE16)
	data = append(data, byte(len(hexB)), byte(len(hexB)>>8))
	data = append(data, []byte(hexB)...)
	return data
}
