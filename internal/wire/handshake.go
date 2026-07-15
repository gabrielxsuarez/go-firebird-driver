package wire

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/user"
	"strings"
	"time"

	fbcharset "github.com/gabrielxsuarez/go-firebird-driver/internal/charset"
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
	// NoneCharset is the character set assumed for text columns declared
	// CHARACTER SET NONE. Empty (or "NONE") keeps them as raw bytes.
	NoneCharset string
	// SQL dialect. Default: 3.
	Dialect uint32
	// Wire encryption preference. Default: WireCryptEnabled.
	WireCrypt    uint32
	WireCryptSet bool // true if WireCrypt was explicitly set
	// Auth plugin list. Default: "Srp256,Srp".
	AuthPluginList string
	// Role for the connection.
	Role string
	// DataTypeBind maps to isc_dpb_set_bind (Firebird 4+).
	DataTypeBind string
	// SessionTimeZone maps to isc_dpb_session_time_zone (Firebird 4+).
	SessionTimeZone string
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

// ConnectContext establishes a connection honoring ctx for the TCP dial and
// the whole handshake (auth + crypt negotiation). Without a deadline here, a
// server that accepts TCP but never answers would hang Connect forever and
// database/sql connection timeouts would not be respected.
func ConnectContext(ctx context.Context, cfg *ProtocolConfig) (*WireConnection, error) {
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
	var dialer net.Dialer
	tcpConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("op_connect: dial %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = tcpConn.SetDeadline(deadline)
		defer func() { _ = tcpConn.SetDeadline(time.Time{}) }()
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
	w.WriteInt32(0)                     // p_cnct_operation
	w.WriteUInt32(ConnectVersion3)      // p_cnct_cversion
	w.WriteUInt32(ArchGeneric)          // p_cnct_client
	w.WriteString(cfg.Database)         // p_cnct_file
	w.WriteInt32(int32(len(protocols))) // p_cnct_count
	w.WriteBuffer(userIdent)            // p_cnct_user_id

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
		// Only switch between plugins we actually implement (the SRP family).
		// Renaming blindly would send SRP data under an arbitrary plugin name
		// (e.g. Legacy_Auth) and fail later with a misleading isc_login error.
		if serverPlugin != PluginSrp && serverPlugin != PluginSrp256 {
			conn.Close()
			return nil, fmt.Errorf("op_connect: server requires unsupported auth plugin %q (supported: %s)", serverPlugin, cfg.AuthPluginList)
		}
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
		// La respuesta final de autenticación trae las keys de wire crypt del
		// servidor (plugins ofrecidos + IV de ChaCha) en resp.Data.
		if len(resp.Data) > 0 {
			serverKeys = copyBytes(resp.Data)
		}
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
	if cfg.WireCrypt == WireCryptRequired && len(sessionKey) == 0 {
		conn.Close()
		return nil, fmt.Errorf("op_crypt: wire_crypt=required but the negotiated auth produced no session key (server may not support wire encryption)")
	}
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
	w.WriteBuffer(dpb)          // p_atch_dpb

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
		noneCharsetID:   fbcharset.CharsetID(cfg.NoneCharset),
		lazySend:        lazySend,
	}

	return wc, nil
}

// selectCipher parses server keys and creates appropriate cipher instances.
// Preference order: ChaCha (if the server offers it with an IV), then Arc4.
func selectCipher(serverKeys []byte, sessionKey []byte) (string, streamCipher, streamCipher, error) {
	plugins, chachaIV := parseServerKeys(serverKeys)

	if pluginListed(plugins, "ChaCha") && chachaIV != nil {
		// La clave es SHA-256(sessionKey) — un solo hash (newChaCha20Cipher lo aplica).
		rc, rerr := newChaCha20Cipher(sessionKey, chachaIV)
		wc, werr := newChaCha20Cipher(sessionKey, chachaIV)
		if rerr == nil && werr == nil {
			return "ChaCha", rc, wc, nil
		}
	}

	if len(plugins) > 0 && !pluginListed(plugins, "Arc4") {
		return "", nil, nil, fmt.Errorf("no supported wire crypt plugin offered by server (offered: %v)", plugins)
	}

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

func pluginListed(plugins []string, name string) bool {
	for _, p := range plugins {
		if p == name {
			return true
		}
	}
	return false
}

// parseServerKeys parses the p_acpt_keys buffer: an untagged clumplet
// sequence of [tag(1)][len(1)][data(len)] entries (see jaybird
// WireConnection.addServerKeys):
//
//	TAG_KEY_TYPE       (0): key type name, e.g. "Symmetric"
//	TAG_KEY_PLUGINS    (1): space-separated plugin list, e.g. "ChaCha Arc4"
//	TAG_PLUGIN_SPECIFIC(3): plugin name + NUL + data (ChaCha: IV of 12/16 bytes)
//
// Returns the offered plugin names and the ChaCha IV if present.
func parseServerKeys(keys []byte) (plugins []string, chachaIV []byte) {
	pos := 0
	for pos+2 <= len(keys) {
		tag := keys[pos]
		length := int(keys[pos+1])
		pos += 2
		if pos+length > len(keys) {
			break
		}
		data := keys[pos : pos+length]
		pos += length

		switch tag {
		case 1: // TAG_KEY_PLUGINS
			for _, name := range strings.Fields(string(data)) {
				plugins = append(plugins, name)
			}
		case 3: // TAG_PLUGIN_SPECIFIC: plugin name + NUL + data
			if sep := bytes.IndexByte(data, 0); sep > 0 {
				name := string(data[:sep])
				specific := data[sep+1:]
				if name == "ChaCha" && (len(specific) == 12 || len(specific) == 16) {
					chachaIV = append([]byte(nil), specific...)
				}
			}
		}
	}
	return plugins, chachaIV
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
	if cfg.DataTypeBind != "" {
		dpb.WriteString(IscDpbSetBind, cfg.DataTypeBind)
	}
	if cfg.SessionTimeZone != "" && !strings.EqualFold(cfg.SessionTimeZone, "server") {
		dpb.WriteString(IscDpbSessionTimeZone, cfg.SessionTimeZone)
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
