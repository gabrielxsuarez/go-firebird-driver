package wire

// DPB tag constants.
const (
	IscDpbPageSize         byte = 4
	IscDpbNoGarbageCollect byte = 16
	IscDpbSysUserName      byte = 19
	IscDpbForceWrite       byte = 24
	IscDpbUserName         byte = 28
	IscDpbPassword         byte = 29
	IscDpbPasswordEnc      byte = 30
	IscDpbLcCtype          byte = 48
	IscDpbConnectTimeout   byte = 57
	IscDpbDummyPktInterval byte = 58
	IscDpbSQLDialect       byte = 63
	IscDpbSetDBReadOnly    byte = 64
	IscDpbSetDBCharset     byte = 68
	IscDpbUtf8Filename     byte = 77
	IscDpbAuthBlock        byte = 79
	IscDpbClientVersion    byte = 80
	IscDpbRemoteProtocol   byte = 81
	IscDpbHostName         byte = 82
	IscDpbOsUser           byte = 83
	IscDpbSpecificAuthData byte = 84
	IscDpbAuthPluginList   byte = 85
	IscDpbAuthPluginName   byte = 86
	IscDpbConfig           byte = 87
	IscDpbNolinger         byte = 88
	IscDpbProcessID        byte = 89
	IscDpbProcessName      byte = 90
	IscDpbSessionTimeZone  byte = 91
	IscDpbSetDBReplica     byte = 92
	IscDpbSetBind          byte = 93
	IscDpbSetBindOfRPT     byte = IscDpbSetBind
	IscDpbDecfloatRound    byte = 94
	IscDpbDecfloatTraps    byte = 95
	IscDpbParallelWorkers  byte = 100
)

// DPBBuilder constructs a Database Parameter Buffer.
// Uses a reusable internal buffer.
type DPBBuilder struct {
	buf []byte
}

// NewDPBBuilder creates a DPB builder with DPB version 2 (protocol 13+).
func NewDPBBuilder() *DPBBuilder {
	b := &DPBBuilder{buf: make([]byte, 0, 256)}
	b.buf = append(b.buf, IscDpbVersion2)
	return b
}

// Reset clears the buffer and writes the version byte.
func (b *DPBBuilder) Reset() {
	b.buf = b.buf[:1]
	b.buf[0] = IscDpbVersion2
}

// WriteString appends a string tag with 4-byte LE length (DPB v2 format).
func (b *DPBBuilder) WriteString(tag byte, value string) {
	n := len(value)
	b.buf = append(b.buf, tag, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
	b.buf = append(b.buf, value...)
}

// WriteMarker appends a marker tag with zero length.
func (b *DPBBuilder) WriteMarker(tag byte) {
	b.buf = append(b.buf, tag, 0, 0, 0, 0)
}

// WriteByteTag appends a single-byte value tag.
func (b *DPBBuilder) WriteByteTag(tag byte, value byte) {
	b.buf = append(b.buf, tag, 1, 0, 0, 0, value)
}

// WriteBytes appends a raw byte value tag with 4-byte LE length.
func (b *DPBBuilder) WriteBytes(tag byte, value []byte) {
	n := len(value)
	b.buf = append(b.buf, tag, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
	b.buf = append(b.buf, value...)
}

// Bytes returns the built DPB.
func (b *DPBBuilder) Bytes() []byte {
	return b.buf
}
