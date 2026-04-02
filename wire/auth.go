package wire

import (
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"hash"
	"math/big"
	"strings"

	"github.com/gabrielxsuarez/go-firebird-driver/internal/bignum"
)

// SRP plugin names recognized by Firebird 3.0+.
const (
	PluginSrp256 = "Srp256"
	PluginSrp    = "Srp"
)

// DefaultPluginList is the preferred comma-separated list of auth plugins.
// Srp256 is preferred; Srp is the fallback for older Firebird 3.0 configurations.
const DefaultPluginList = PluginSrp256 + "," + PluginSrp

// srpClient holds the ephemeral state for one SRP authentication exchange.
type srpClient struct {
	pluginName string   // active plugin ("Srp256" or "Srp")
	user       string   // uppercase database user
	password   string   // plaintext password
	privateKey *big.Int // a (client ephemeral private key)
	publicKey  *big.Int // A = g^a mod N (client ephemeral public key)
	sessionKey []byte   // K (20 bytes, derived after proof)
	proof      []byte   // M1 (client proof sent to server)
}

// newSRPClient creates a new SRP client for the given plugin, user, and password.
// It generates a random ephemeral private key and computes the public key A.
func newSRPClient(pluginName, user, password string) (*srpClient, error) {
	privKey, err := bignum.GeneratePrivateKey()
	if err != nil {
		return nil, err
	}
	return newSRPClientWithKey(pluginName, user, password, privKey), nil
}

// newSRPClientWithKey creates an SRP client with a specific private key (for testing).
func newSRPClientWithKey(pluginName, user, password string, privateKey *big.Int) *srpClient {
	return &srpClient{
		pluginName: pluginName,
		user:       strings.ToUpper(user),
		password:   password,
		privateKey: privateKey,
		publicKey:  bignum.ClientPublicKey(privateKey),
	}
}

// PublicKeyHex returns the client public key A as an uppercase hex string.
func (c *srpClient) PublicKeyHex() string {
	return toUpperHex(bignum.BigToBytes(c.publicKey))
}

// SessionKey returns the session key K (available after ComputeProof).
func (c *srpClient) SessionKey() []byte {
	return c.sessionKey
}

// SetPlugin changes the active plugin (for server-requested plugin switch).
func (c *srpClient) SetPlugin(pluginName string) {
	c.pluginName = pluginName
}

// ComputeProof computes the SRP client proof M1 from the server's challenge.
// serverData is the raw auth data from op_cond_accept or op_cont_auth:
//
//	[saltLen LE16][salt bytes][keyLen LE16][B as hex string]
//
// Returns M1 as uppercase hex bytes, suitable for sending in op_cont_auth.
func (c *srpClient) ComputeProof(serverData []byte) ([]byte, error) {
	salt, serverPublic, err := parseServerAuthData(serverData)
	if err != nil {
		return nil, err
	}

	// Session key K = SHA1(S)
	c.sessionKey = clientSessionKey(c.user, c.password, salt, c.publicKey, serverPublic, c.privateKey)

	// Client proof M1 = H(H(N) xor H(g), H(user), salt, A, B, K)
	c.proof = clientProof(c.pluginName, c.user, salt, c.publicKey, serverPublic, c.sessionKey)

	// Return M1 as uppercase hex
	hexProof := toUpperHex(c.proof)
	return []byte(hexProof), nil
}

// --- Core SRP computations ---

// scramble computes u = SHA1(Pad(A) || Pad(B)).
// Always uses SHA-1 regardless of plugin variant.
func scramble(keyA, keyB *big.Int) *big.Int {
	h := sha1.New()
	h.Write(bignum.Pad(keyA))
	h.Write(bignum.Pad(keyB))
	return bignum.BytesToBig(h.Sum(nil))
}

// userHash computes x = SHA1(salt || SHA1(user + ":" + password)).
// Always uses SHA-1 regardless of plugin variant.
// Reuses a single hasher via Reset() to avoid a second allocation.
func userHash(salt []byte, user, password string) *big.Int {
	h := sha1.New()
	h.Write([]byte(user))
	h.Write([]byte(":"))
	h.Write([]byte(password))
	inner := h.Sum(nil)

	h.Reset()
	h.Write(salt)
	h.Write(inner)
	return bignum.BytesToBig(h.Sum(nil))
}

// clientSessionKey computes K = SHA1(S), where S is the shared session secret.
// S = (B - k*g^x)^(a + u*x) mod N
// Reuses big.Int temporaries to reduce allocations.
func clientSessionKey(user, password string, salt []byte, keyA, keyB, privA *big.Int) []byte {
	u := scramble(keyA, keyB)
	x := userHash(salt, user, password)

	// t1 = g^x mod N
	var t1, t2 big.Int
	t1.Exp(bignum.G, x, bignum.N)

	// t1 = (k * t1) % N
	t1.Mul(bignum.K, &t1)
	t1.Mod(&t1, bignum.N)

	// t1 = (B - t1) % N  (diff)
	t1.Sub(keyB, &t1)
	t1.Mod(&t1, bignum.N)

	// t2 = (u * x) % N
	t2.Mul(u, x)
	t2.Mod(&t2, bignum.N)

	// t2 = (a + t2) % N  (aux)
	t2.Add(privA, &t2)
	t2.Mod(&t2, bignum.N)

	// S = t1^t2 mod N
	t1.Exp(&t1, &t2, bignum.N)

	// K = SHA1(S)
	h := sha1.New()
	h.Write(t1.Bytes())
	return h.Sum(nil)
}

// clientProof computes M1 = H(H(N) xor H(g), H(user), salt, A, B, K).
// The hash H depends on the plugin: SHA-1 for Srp, SHA-256 for Srp256.
func clientProof(pluginName, user string, salt []byte, keyA, keyB *big.Int, sessionKey []byte) []byte {
	// n1 = H(N) as big.Int
	hn := sha1.Sum(bignum.N.Bytes())
	n1 := bignum.BytesToBig(hn[:])

	// n2 = H(g) as big.Int
	hg := sha1.Sum(bignum.G.Bytes())
	n2 := bignum.BytesToBig(hg[:])

	// n3 = n1 ^ n2 mod N  (xor via ModPow in reference, but actually it's XOR)
	// Note: Firebird implementations use ModPow(n1, n2, N), NOT bitwise XOR.
	n3 := new(big.Int).Exp(n1, n2, bignum.N)

	// n4 = H(user) as big.Int
	hu := sha1.Sum([]byte(user))
	n4 := bignum.BytesToBig(hu[:])

	// SRP proof: M1 = H(n3 || n4 || salt || A || B || K)
	var digest hash.Hash
	switch pluginName {
	case PluginSrp256:
		digest = sha256.New()
	default:
		digest = sha1.New()
	}
	digest.Write(n3.Bytes())
	digest.Write(n4.Bytes())
	digest.Write(salt)
	digest.Write(keyA.Bytes())
	digest.Write(keyB.Bytes())
	digest.Write(sessionKey)
	return digest.Sum(nil)
}

// --- Server response parsing ---

// parseServerAuthData extracts salt and server public key B from the server's
// auth data. Format: [saltLen LE16][salt][keyLen LE16][B as hex string]
func parseServerAuthData(data []byte) (salt []byte, serverPublic *big.Int, err error) {
	if len(data) < 4 {
		return nil, nil, errors.New("srp: server auth data too short")
	}

	// Salt length (2-byte little-endian)
	saltLen := int(data[0]) | int(data[1])<<8
	if len(data) < 2+saltLen+2 {
		return nil, nil, errors.New("srp: server auth data truncated at salt")
	}
	salt = data[2 : 2+saltLen]

	// Server key length (2-byte little-endian)
	keyStart := 2 + saltLen
	_ = int(data[keyStart]) | int(data[keyStart+1])<<8 // keyLen (informational)
	hexStart := keyStart + 2

	if hexStart >= len(data) {
		return nil, nil, errors.New("srp: server auth data missing public key")
	}

	// B is a hex string encoded as ISO-8859-1 bytes
	hexB := string(data[hexStart:])
	serverPublic, ok := new(big.Int).SetString(hexB, 16)
	if !ok {
		return nil, nil, errors.New("srp: invalid hex in server public key")
	}

	return salt, serverPublic, nil
}

// --- CNCT user identification block ---

// buildUserIdentBlock assembles the TLV-encoded user identification block for
// op_connect. It includes OS user, hostname, database login, auth plugin info,
// the client public key A, the plugin list, and wire crypt preference.
func buildUserIdentBlock(osUser, hostname, dbUser, pluginName string, publicKeyHex string, pluginList string, wireCrypt uint32) []byte {
	// Pre-compute total size to avoid reallocation.
	// Each tag entry: 1 (tag) + 1 (len) + len(value)
	// CNCT_specific_data may be multi-part.
	size := 0
	size += 2 + len(osUser)        // CNCT_user
	size += 2 + len(hostname)      // CNCT_host
	size += 2                      // CNCT_user_verification (empty)
	size += 2 + len(dbUser)        // CNCT_login
	size += 2 + len(pluginName)    // CNCT_plugin_name
	size += specificDataSize(publicKeyHex) // CNCT_specific_data (multi-part)
	size += 2 + len(pluginList)    // CNCT_plugin_list
	size += 2 + 4                  // CNCT_client_crypt (4-byte value)

	buf := make([]byte, 0, size)

	buf = appendTag(buf, CnctUser, []byte(osUser))
	buf = appendTag(buf, CnctHost, []byte(hostname))
	buf = appendTag(buf, CnctUserVerification, nil)
	buf = appendTag(buf, CnctLogin, []byte(dbUser))
	buf = appendTag(buf, CnctPluginName, []byte(pluginName))
	buf = appendSpecificData(buf, publicKeyHex)
	buf = appendTag(buf, CnctPluginList, []byte(pluginList))
	buf = appendCryptPreference(buf, wireCrypt)

	return buf
}

// appendTag appends a single TLV entry: [tag][len][value].
func appendTag(buf []byte, tag byte, value []byte) []byte {
	buf = append(buf, tag, byte(len(value)))
	return append(buf, value...)
}

// specificDataSize returns the byte size of the multi-part CNCT_specific_data
// encoding for a hex string of the given length.
func specificDataSize(hexKey string) int {
	n := len(hexKey)
	if n <= 254 {
		return 3 + n // tag + (len+1) + seq + data
	}
	// First part: 3 + 254, remaining parts: 3 + remaining each (max 254 per part)
	size := 0
	for n > 0 {
		chunk := n
		if chunk > 254 {
			chunk = 254
		}
		size += 3 + chunk // tag + (chunk+1) + seq + data
		n -= chunk
	}
	return size
}

// appendSpecificData appends CNCT_specific_data with multi-part encoding.
// Each part: [0x07][len+1][sequence][data] where data is at most 254 bytes.
func appendSpecificData(buf []byte, hexKey string) []byte {
	data := []byte(hexKey)
	seq := byte(0)
	for len(data) > 0 {
		chunk := len(data)
		if chunk > 254 {
			chunk = 254
		}
		buf = append(buf, CnctSpecificData, byte(chunk+1), seq)
		buf = append(buf, data[:chunk]...)
		data = data[chunk:]
		seq++
	}
	return buf
}

// appendCryptPreference appends CNCT_client_crypt with a 4-byte LE value.
func appendCryptPreference(buf []byte, wireCrypt uint32) []byte {
	buf = append(buf, CnctClientCrypt, 4, byte(wireCrypt), byte(wireCrypt>>8), byte(wireCrypt>>16), byte(wireCrypt>>24))
	return buf
}

// toUpperHex encodes src as uppercase hexadecimal in a single allocation.
// Replaces the two-allocation pattern: strings.ToUpper(hex.EncodeToString(src)).
func toUpperHex(src []byte) string {
	const hexDigits = "0123456789ABCDEF"
	dst := make([]byte, len(src)*2)
	for i, b := range src {
		dst[i*2] = hexDigits[b>>4]
		dst[i*2+1] = hexDigits[b&0x0f]
	}
	return string(dst)
}

// --- Server-side helpers (for testing only) ---

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

// verifier computes v = g^x mod N.
func verifier(user, password string, salt []byte) *big.Int {
	x := userHash(salt, user, password)
	return new(big.Int).Exp(bignum.G, x, bignum.N)
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
