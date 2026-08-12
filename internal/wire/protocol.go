// Package wire implements the Firebird wire protocol encoding and operations.
package wire

//lint:file-ignore U1000 Este archivo es el registro completo de constantes del wire
// protocol (espejo de la spec). Se conservan tambien las constantes de areas fuera del
// alcance 1.0 (services, events, batch) como documentacion declarativa y para navegacion
// con la spec al lado.

// --- Operation codes (opcodes) ---

const (
	// Connection operations
	opConnect          int32 = 1
	opAccept           int32 = 3
	opReject           int32 = 4
	opDisconnect       int32 = 6
	opAcceptData       int32 = 94
	opCondAccept       int32 = 98
	opContAuth         int32 = 92
	opTrustedAuth      int32 = 90
	opCrypt            int32 = 96
	opCryptKeyCallback int32 = 97

	// Database operations
	opAttach       int32 = 19
	opCreate       int32 = 20
	opDetach       int32 = 21
	opDropDatabase int32 = 81
	opInfoDatabase int32 = 40
	opCancel       int32 = 91
	opPing         int32 = 93

	// Transaction operations
	opTransaction       int32 = 29
	opCommit            int32 = 30
	opRollback          int32 = 31
	opCommitRetaining   int32 = 50
	opRollbackRetaining int32 = 86
	opPrepare2          int32 = 51
	opReconnect         int32 = 33
	opInfoTransaction   int32 = 42

	// Statement operations
	opAllocateStatement int32 = 62
	opPrepareStatement  int32 = 68
	opExecute           int32 = 63
	opExecute2          int32 = 76
	opExecImmediate     int32 = 64
	opExecImmediate2    int32 = 75
	opFetch             int32 = 65
	opFetchResponse     int32 = 66
	opFetchScroll       int32 = 112
	opFreeStatement     int32 = 67
	opSetCursor         int32 = 69
	opInfoSQL           int32 = 70
	opInfoCursor        int32 = 113
	opSQLResponse       int32 = 78

	// Blob operations
	opCreateBlob    int32 = 34
	opCreateBlob2   int32 = 57
	opOpenBlob      int32 = 35
	opOpenBlob2     int32 = 56
	opGetSegment    int32 = 36
	opPutSegment    int32 = 37
	opBatchSegments int32 = 44
	opSeekBlob      int32 = 61
	opCancelBlob    int32 = 38
	opCloseBlob     int32 = 39
	opInfoBlob      int32 = 43
	opInlineBlob    int32 = 114

	// Array operations
	opGetSlice int32 = 58
	opPutSlice int32 = 59
	opSlice    int32 = 60

	// Batch operations (protocol 16+)
	opBatchCreate     int32 = 99
	opBatchMsg        int32 = 100
	opBatchExec       int32 = 101
	opBatchRls        int32 = 102
	opBatchCS         int32 = 103
	opBatchRegblob    int32 = 104
	opBatchBlobStream int32 = 105
	opBatchSetBpb     int32 = 106
	opBatchCancel     int32 = 109
	opBatchSync       int32 = 110
	opInfoBatch       int32 = 111

	// Service operations
	opServiceAttach int32 = 82
	opServiceDetach int32 = 83
	opServiceInfo   int32 = 84
	opServiceStart  int32 = 85

	// Event operations
	opQueEvents      int32 = 48
	opCancelEvents   int32 = 49
	opEvent          int32 = 52
	opConnectRequest int32 = 53
	opAuxConnect     int32 = 54

	// Responses and utilities
	opResponse           int32 = 9
	opDummy              int32 = 71
	opExit               int32 = 2
	opAbortAuxConnection int32 = 95
)

// --- Protocol versions ---

const (
	ProtocolVersion10 uint32 = 10
	ProtocolVersion11 uint32 = 0x800B
	ProtocolVersion12 uint32 = 0x800C
	ProtocolVersion13 uint32 = 0x800D
	ProtocolVersion14 uint32 = 0x800E
	ProtocolVersion15 uint32 = 0x800F
	ProtocolVersion16 uint32 = 0x8010
	ProtocolVersion17 uint32 = 0x8011
	ProtocolVersion18 uint32 = 0x8012
	ProtocolVersion19 uint32 = 0x8013

	FBProtocolFlag uint32 = 0x8000
	FBProtocolMask uint32 = 0x7FFF
)

// --- Connection constants ---

const (
	ConnectVersion2 uint32 = 2
	ConnectVersion3 uint32 = 3
	ArchGeneric     uint32 = 1
	InvalidObject   uint32 = 0xFFFF
)

// --- Protocol types and flags ---

const (
	PtypeRPC       uint32 = 2
	PtypeBatchSend uint32 = 3
	PtypeOutOfBand uint32 = 4
	PtypeLazySend  uint32 = 5
	PtypeMask      uint32 = 0xFF
)

// --- CNCT parameters ---

const (
	CnctUser             byte = 1
	CnctPasswd           byte = 2
	CnctHost             byte = 4
	CnctGroup            byte = 5
	CnctUserVerification byte = 6
	CnctSpecificData     byte = 7
	CnctPluginName       byte = 8
	CnctLogin            byte = 9
	CnctPluginList       byte = 10
	CnctClientCrypt      byte = 11
)

// --- Wire crypt preferences ---

const (
	WireCryptDisabled uint32 = 0
	WireCryptEnabled  uint32 = 1
	WireCryptRequired uint32 = 2
)

// --- DSQL options ---

const (
	DSQLClose     uint32 = 1
	DSQLDrop      uint32 = 2
	DSQLUnprepare uint32 = 4
)

// --- Statement types ---

const (
	StmtSelect        int32 = 1
	StmtInsert        int32 = 2
	StmtUpdate        int32 = 3
	StmtDelete        int32 = 4
	StmtDDL           int32 = 5
	StmtGetSegment    int32 = 6
	StmtPutSegment    int32 = 7
	StmtExecProcedure int32 = 8
	StmtStartTrans    int32 = 9
	StmtCommit        int32 = 10
	StmtRollback      int32 = 11
	StmtSelectForUpd  int32 = 12
	StmtSetGenerator  int32 = 13
	StmtSavepoint     int32 = 14
)

// --- SQL dialect ---

const (
	SQLDialectV5      uint32 = 1
	SQLDialectV6Trans uint32 = 2
	SQLDialectV6      uint32 = 3
	SQLDialectCurrent uint32 = 3
)

// --- Cancellation kinds ---

const (
	CancelDisable uint32 = 1
	CancelEnable  uint32 = 2
	CancelRaise   uint32 = 3
	CancelAbort   uint32 = 4
)

// --- Status vector tags ---

const (
	IscArgEnd         int32 = 0
	IscArgGds         int32 = 1
	IscArgString      int32 = 2
	IscArgCstring     int32 = 3
	IscArgNumber      int32 = 4
	IscArgInterpreted int32 = 5
	IscArgWarning     int32 = 18
	IscArgSQLState    int32 = 19
)

// --- Info structural items ---

const (
	IscInfoEnd          byte = 1
	IscInfoTruncated    byte = 2
	IscInfoError        byte = 3
	IscInfoDataNotReady byte = 4
	IscInfoLength       byte = 126
	IscInfoFlagEnd      byte = 127
)

// --- DPB/TPB/SPB version tags ---

const (
	IscDpbVersion1 byte = 1
	IscDpbVersion2 byte = 2
	IscTpbVersion1 byte = 1
	IscTpbVersion3 byte = 3
	IscSpbVersion1 byte = 1
	IscSpbVersion2 byte = 2
	IscSpbVersion3 byte = 3
)

// --- SQL type codes ---

const (
	SQLText          int32 = 452
	SQLVarying       int32 = 448
	SQLShort         int32 = 500
	SQLLong          int32 = 496
	SQLFloat         int32 = 482
	SQLDouble        int32 = 480
	SQLDFloat        int32 = 530
	SQLTimestamp     int32 = 510
	SQLBlob          int32 = 520
	SQLArray         int32 = 540
	SQLQuad          int32 = 550
	SQLTypeDate      int32 = 570
	SQLTypeTime      int32 = 560
	SQLInt64         int32 = 580
	SQLInt128        int32 = 32752
	SQLTimestampTZ   int32 = 32754
	SQLTimeTZ        int32 = 32756
	SQLTimeTZEx      int32 = 32750
	SQLTimestampTZEx int32 = 32748
	SQLDec16         int32 = 32760
	SQLDec34         int32 = 32762
	SQLBoolean       int32 = 32764
	SQLNull          int32 = 32766
)

// --- BLR type codes ---

const (
	BlrShort         byte = 7
	BlrLong          byte = 8
	BlrQuad          byte = 9
	BlrFloat         byte = 10
	BlrDFloat        byte = 11
	BlrSQLDate       byte = 12
	BlrSQLTime       byte = 13
	BlrText          byte = 14
	BlrText2         byte = 15
	BlrInt64         byte = 16
	BlrBlob2         byte = 17
	BlrBool          byte = 23
	BlrDec64         byte = 24
	BlrDec128        byte = 25
	BlrInt128        byte = 26
	BlrDouble        byte = 27
	BlrSQLTimeTZ     byte = 28
	BlrTimestampTZ   byte = 29
	BlrExTimeTZ      byte = 30
	BlrExTimestampTZ byte = 31
	BlrTimestamp     byte = 35
	BlrVarying       byte = 37
	BlrVarying2      byte = 38
)

// --- BLR structural tokens ---

const (
	BlrVersion4 byte = 4
	BlrVersion5 byte = 5
	BlrBegin    byte = 2
	BlrMessage  byte = 4
	BlrEnd      byte = 255
	BlrEOC      byte = 76
)

// --- Fetch scroll directions (protocol 18+) ---

const (
	FetchNext     int32 = 0
	FetchPrior    int32 = 1
	FetchFirst    int32 = 2
	FetchLast     int32 = 3
	FetchAbsolute int32 = 4
	FetchRelative int32 = 5
)

// --- Blob seek modes ---

const (
	BlobSeekFromHead int32 = 0
	BlobSeekRelative int32 = 1
	BlobSeekFromTail int32 = 2
)
