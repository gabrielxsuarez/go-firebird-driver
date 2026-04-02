package wire

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/user"
	"strings"
	"time"
)

// ProtocolConfig holds the parameters for the wire protocol handshake.
type ProtocolConfig struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string

	// Charset for the connection. Default: "UTF8".
	Charset string
	// SQL dialect. Default: 3.
	Dialect uint32
	// Wire encryption preference. Default: WireCryptEnabled.
	WireCrypt    uint32
	WireCryptSet bool // true if WireCrypt was explicitly set
	// Auth plugin list. Default: "Srp256,Srp".
	AuthPluginList string
	// Role for the connection.
	Role string
}

// HandshakeResult holds the outcome of a successful protocol handshake.
type HandshakeResult struct {
	ProtocolVersion uint32
	DBHandle        int32
}

// protocolDescriptor describes one offered protocol version.
type protocolDescriptor struct {
	Version uint32
	Arch    uint32
	MinType uint32
	MaxType uint32
	Weight  uint32
}

// supportedProtocols returns the protocol versions we offer, from lowest
// to highest weight.
func supportedProtocols() []protocolDescriptor {
	maxType := PtypeLazySend
	return []protocolDescriptor{
		{ProtocolVersion13, ArchGeneric, PtypeBatchSend, maxType, 2},
		{ProtocolVersion15, ArchGeneric, PtypeBatchSend, maxType, 4},
		{ProtocolVersion16, ArchGeneric, PtypeBatchSend, maxType, 6},
		{ProtocolVersion18, ArchGeneric, PtypeBatchSend, maxType, 8},
	}
}

// Connect performs the full Firebird wire protocol handshake:
// TCP connect → op_connect → auth loop → op_crypt → op_attach.
// Returns a WireConnection ready for use.
func Connect(cfg *ProtocolConfig) (*WireConnection, error) {
	if cfg.Charset == "" {
		cfg.Charset = "UTF8"
	}
	if cfg.Dialect == 0 {
		cfg.Dialect = SQLDialectCurrent
	}
	if cfg.AuthPluginList == "" {
		cfg.AuthPluginList = DefaultPluginList
	}
	if !cfg.WireCryptSet {
		cfg.WireCrypt = WireCryptEnabled
	}

	// Determine preferred plugin
	pluginName := PluginSrp256
	if idx := strings.Index(cfg.AuthPluginList, ","); idx > 0 {
		pluginName = cfg.AuthPluginList[:idx]
	}

	// Create SRP client
	srp, err := newSRPClient(pluginName, cfg.User, cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("op_connect: srp init: %w", err)
	}

	// TCP connect
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	tcpConn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("op_connect: dial %s: %w", addr, err)
	}
	if tcp, ok := tcpConn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetReadBuffer(32768)
		_ = tcp.SetWriteBuffer(32768)
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(5 * time.Minute)
	}

	conn := NewConn(tcpConn)
	w := NewWriter()
	r := NewReader(conn)

	// Build user identification block
	osUser := getOSUser()
	hostname := getHostname()
	publicKeyHex := srp.PublicKeyHex()
	userIdent := buildUserIdentBlock(osUser, hostname, strings.ToUpper(cfg.User),
		pluginName, publicKeyHex, cfg.AuthPluginList, cfg.WireCrypt)

	// Write op_connect
	protocols := supportedProtocols()
	w.WriteInt32(opConnect)
	w.WriteInt32(0)                   // p_cnct_operation
	w.WriteUInt32(ConnectVersion3)    // p_cnct_cversion
	w.WriteUInt32(ArchGeneric)        // p_cnct_client
	w.WriteString(cfg.Database)       // p_cnct_file
	w.WriteInt32(int32(len(protocols))) // p_cnct_count
	w.WriteBuffer(userIdent)          // p_cnct_user_id

	// Protocol descriptors
	for _, p := range protocols {
		w.WriteUInt32(p.Version)
		w.WriteUInt32(p.Arch)
		w.WriteUInt32(p.MinType)
		w.WriteUInt32(p.MaxType)
		w.WriteUInt32(p.Weight)
	}

	if err := w.Flush(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("op_connect: flush: %w", err)
	}

	// Read server response
	op := r.ReadOpcode()
	if r.Err() != nil {
		conn.Close()
		return nil, fmt.Errorf("op_connect: read response: %w", r.Err())
	}

	var acceptedVersion uint32
	var acceptedType uint32
	var serverAuthData []byte
	var serverPlugin string
	var authenticated int32
	var serverKeys []byte

	switch op {
	case opAcceptData, opCondAccept:
		acceptedVersion = r.ReadUInt32()
		_ = r.ReadUInt32() // architecture
		acceptedType = r.ReadUInt32()
		serverAuthData = copyBytes(r.ReadBuffer())
		serverPlugin = r.ReadString()
		authenticated = r.ReadInt32()
		serverKeys = copyBytes(r.ReadBuffer())
		if r.Err() != nil {
			conn.Close()
			return nil, fmt.Errorf("op_connect: read accept: %w", r.Err())
		}

	case opReject:
		conn.Close()
		return nil, fmt.Errorf("op_connect: connection rejected by server")

	default:
		conn.Close()
		return nil, fmt.Errorf("op_connect: unexpected opcode %d", op)
	}

	protocolVersion := acceptedVersion & FBProtocolMask

	// Handle authentication
	if serverPlugin != "" && serverPlugin != srp.pluginName {
		srp.SetPlugin(serverPlugin)
	}

	if authenticated == 0 && len(serverAuthData) > 0 {
		// Server sent challenge data directly: compute proof
		proof, err := srp.ComputeProof(serverAuthData)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("op_connect: srp proof: %w", err)
		}

		// Send op_cont_auth with proof
		w.WriteInt32(opContAuth)
		w.WriteBuffer(proof)
		w.WriteString(srp.pluginName)
		w.WriteString(cfg.AuthPluginList)
		w.WriteBuffer(serverKeys)
		if err := w.Flush(conn); err != nil {
			conn.Close()
			return nil, fmt.Errorf("op_cont_auth: flush: %w", err)
		}

		resp, err := r.ReadResponse()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("op_cont_auth: %w", err)
		}
		_ = resp
	} else if authenticated == 0 {
		// No auth data: server switched plugins or needs another round.
		// Re-send the client public key with the (possibly new) plugin name.
		publicKeyHex := srp.PublicKeyHex()

		w.WriteInt32(opContAuth)
		w.WriteBuffer([]byte(publicKeyHex))
		w.WriteString(srp.pluginName)
		w.WriteString(cfg.AuthPluginList)
		w.WriteBuffer(serverKeys)
		if err := w.Flush(conn); err != nil {
			conn.Close()
			return nil, fmt.Errorf("op_cont_auth: flush: %w", err)
		}

		// Server responds with challenge data (salt+B) in op_cont_auth
		contOp := r.ReadOpcode()
		if r.Err() != nil {
			conn.Close()
			return nil, fmt.Errorf("op_cont_auth: read opcode: %w", r.Err())
		}

		switch contOp {
		case opContAuth:
			// Read op_cont_auth fields: data, plugin, list, keys
			contAuthData := copyBytes(r.ReadBuffer())
			_ = r.ReadString() // plugin name
			_ = r.ReadString() // plugin list
			contKeys := copyBytes(r.ReadBuffer())
			if r.Err() != nil {
				conn.Close()
				return nil, fmt.Errorf("op_cont_auth: read: %w", r.Err())
			}
			if len(contKeys) > 0 {
				serverKeys = contKeys
			}

			// Compute proof from server challenge
			proof, err := srp.ComputeProof(contAuthData)
			if err != nil {
				conn.Close()
				return nil, fmt.Errorf("op_cont_auth: srp proof: %w", err)
			}

			// Send proof
			w.WriteInt32(opContAuth)
			w.WriteBuffer(proof)
			w.WriteString(srp.pluginName)
			w.WriteString(cfg.AuthPluginList)
			w.WriteBuffer(serverKeys)
			if err := w.Flush(conn); err != nil {
				conn.Close()
				return nil, fmt.Errorf("op_cont_auth: flush proof: %w", err)
			}

			resp, err := r.ReadResponse()
			if err != nil {
				conn.Close()
				return nil, fmt.Errorf("op_cont_auth: %w", err)
			}
			_ = resp

		case opResponse:
			// Server already authenticated
			resp := r.readGenericResponse()
			if r.Err() != nil {
				conn.Close()
				return nil, fmt.Errorf("op_cont_auth: read response: %w", r.Err())
			}
			if resp.Status.HasError() {
				conn.Close()
				return nil, fmt.Errorf("op_cont_auth: %w", &StatusError{SV: resp.Status})
			}

		default:
			conn.Close()
			return nil, fmt.Errorf("op_cont_auth: unexpected opcode %d", contOp)
		}
	}

	// Activate wire encryption
	sessionKey := srp.SessionKey()
	if cfg.WireCrypt != WireCryptDisabled && len(sessionKey) > 0 {
		cipherName, readCipher, writeCipher, err := selectCipher(serverKeys, sessionKey)
		if err != nil && cfg.WireCrypt == WireCryptRequired {
			conn.Close()
			return nil, fmt.Errorf("op_crypt: %w", err)
		}
		if err == nil {
			// Send op_crypt in plaintext
			w.WriteInt32(opCrypt)
			w.WriteString(cipherName)
			w.WriteString("Symmetric")
			if err := w.Flush(conn); err != nil {
				conn.Close()
				return nil, fmt.Errorf("op_crypt: flush: %w", err)
			}

			// Activate encryption immediately
			conn.enableEncryption(readCipher, writeCipher)
			// Reset reader for encrypted stream
			r.Reset(conn)

			// Read op_crypt response (now encrypted)
			_, err = r.ReadResponse()
			if err != nil {
				conn.Close()
				return nil, fmt.Errorf("op_crypt: %w", err)
			}
		}
	}

	// op_attach
	dpb := buildConnectDPB(cfg, srp)

	w.WriteInt32(opAttach)
	w.WriteInt32(0)             // p_atch_database
	w.WriteString(cfg.Database) // p_atch_file
	w.WriteBuffer(dpb)         // p_atch_dpb

	if err := w.Flush(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("op_attach: flush: %w", err)
	}

	resp, err := r.ReadResponse()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("op_attach: %w", err)
	}

	lazySend := (acceptedType & PtypeMask) == PtypeLazySend

	wc := &WireConnection{
		conn:            conn,
		reader:          r,
		writer:          w,
		dbHandle:        resp.Handle,
		protocolVersion: protocolVersion,
		charset:         cfg.Charset,
		lazySend:        lazySend,
	}

	return wc, nil
}

// selectCipher parses server keys and creates appropriate cipher instances.
func selectCipher(serverKeys []byte, sessionKey []byte) (string, streamCipher, streamCipher, error) {
	// Parse server key types from the accept response.
	// The keys buffer is a serialized structure; for simplicity, try ChaCha first, then Arc4.

	// Try ChaCha20 (needs a nonce from server keys)
	if nonce := extractChaChaData(serverKeys); nonce != nil {
		keyHash := sha256.Sum256(sessionKey)
		rc, err := newChaCha20Cipher(keyHash[:], nonce)
		if err == nil {
			wc, err := newChaCha20Cipher(keyHash[:], nonce)
			if err == nil {
				return "ChaCha", rc, wc, nil
			}
		}
	}

	// Fall back to Arc4
	rc, err := newArc4Cipher(sessionKey)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	wc, err := newArc4Cipher(sessionKey)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	return "Arc4", rc, wc, nil
}

// extractChaChaData attempts to extract a ChaCha nonce from server keys buffer.
// Server keys format: repeated [type_tag(1)][key_tag(1)][len(2 LE)][data]
func extractChaChaData(keys []byte) []byte {
	pos := 0
	for pos < len(keys) {
		if pos+4 > len(keys) {
			break
		}
		typeTag := keys[pos]
		pos++
		_ = keys[pos] // key tag
		pos++
		length := int(keys[pos]) | int(keys[pos+1])<<8
		pos += 2
		if pos+length > len(keys) {
			break
		}
		data := keys[pos : pos+length]
		pos += length

		// Type 0 = "Symmetric", look for "ChaCha" name + nonce
		if typeTag == 0 {
			// Parse inner structure: plugin_name + specific_data
			inner := data
			ipos := 0
			for ipos < len(inner) {
				if ipos+3 > len(inner) {
					break
				}
				itag := inner[ipos]
				ipos++
				ilen := int(inner[ipos]) | int(inner[ipos+1])<<8
				ipos += 2
				if ipos+ilen > len(inner) {
					break
				}
				idata := inner[ipos : ipos+ilen]
				ipos += ilen

				if itag == 0 && string(idata) == "ChaCha" {
					// Next item should be the nonce
					continue
				}
				if itag == 5 && len(idata) >= 12 {
					return idata[:12]
				}
			}
		}
	}
	return nil
}

// buildConnectDPB builds the DPB for op_attach.
func buildConnectDPB(cfg *ProtocolConfig, srp *srpClient) []byte {
	dpb := NewDPBBuilder()
	dpb.WriteString(IscDpbLcCtype, cfg.Charset)
	dpb.WriteByteTag(IscDpbSQLDialect, byte(cfg.Dialect))
	dpb.WriteString(IscDpbUserName, strings.ToUpper(cfg.User))
	dpb.WriteString(IscDpbAuthPluginName, srp.pluginName)
	dpb.WriteString(IscDpbAuthPluginList, cfg.AuthPluginList)
	dpb.WriteMarker(IscDpbUtf8Filename)
	dpb.WriteString(IscDpbClientVersion, "go-firebird-driver/1.0")
	dpb.WriteString(IscDpbRemoteProtocol, "TCP")
	dpb.WriteString(IscDpbHostName, getHostname())
	dpb.WriteString(IscDpbOsUser, getOSUser())
	if cfg.Role != "" {
		dpb.WriteString(63, cfg.Role) // isc_dpb_sql_role_name
	}
	return dpb.Bytes()
}

// getOSUser returns the current OS username.
func getOSUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}

// getHostname returns the current hostname.
func getHostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// copyBytes returns a copy of the given byte slice.
func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
