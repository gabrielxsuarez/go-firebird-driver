package firebird

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	fbcharset "github.com/gabrielxsuarez/go-firebird-driver/internal/charset"
	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// conn implements driver.Conn and all optional interfaces.
type conn struct {
	wc        *wire.WireConnection
	config    *Config
	closed    bool
	bad       bool
	mu        sync.Mutex
	fetchSize int

	// activeTx is the handle of the explicit transaction started by BeginTx.
	// When zero, ExecContext/QueryContext use auto-commit transactions.
	activeTx int32

	// autoTx is a persistent transaction used for auto-commit operations.
	// It is kept alive via CommitRetaining to avoid creating/destroying
	// a transaction on every single Exec call.
	autoTx int32

	// dirtyAutoTx indicates uncommitted DML was executed on autoTx.
	dirtyAutoTx bool

	// descCache caches ParseSQLDescribeInfo results by SQL text to avoid
	// repeated parsing and string allocations for repeated queries.
	descCache [descCacheSlots]descCacheEntry
}

var (
	_ driver.Conn               = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.Pinger             = (*conn)(nil)
	_ driver.SessionResetter    = (*conn)(nil)
	_ driver.Validator          = (*conn)(nil)
	_ driver.NamedValueChecker  = (*conn)(nil)

	errReadUncommitted = errors.New("firebird: isolation level READ UNCOMMITTED not supported")
	errWriteCommitted  = errors.New("firebird: isolation level WRITE COMMITTED not supported")
	errLinearizable    = errors.New("firebird: isolation level LINEARIZABLE not supported")
)

// gdsIscLogin is the Firebird GDS code for "authentication failed" (isc_login).
// Under concurrent connection bursts, Firebird's SRP auth occasionally returns
// this error even with correct credentials.  A brief retry resolves it.
const gdsIscLogin = int32(335544472)

// maxConnRetries is the number of times newConnection retries on a transient
// auth failure before giving up.
const maxConnRetries = 3

// Descriptor cache: avoids repeated ParseSQLDescribeInfo string allocations
// for queries executed more than once on the same connection.
const (
	descCacheSlots = 32 // must be power of 2
	descCacheMask  = descCacheSlots - 1
)

type descCacheEntry struct {
	query    string
	stmtType int32
	outputs  []wire.ColumnDescriptor
	inputs   []wire.ColumnDescriptor
}

func descCacheIndex(query string) uint32 {
	h := uint32(2166136261) // FNV-1a offset basis
	for i := 0; i < len(query); i++ {
		h ^= uint32(query[i])
		h *= 16777619
	}
	return h & descCacheMask
}

func (c *conn) descCacheLookup(query string) (int32, []wire.ColumnDescriptor, []wire.ColumnDescriptor, bool) {
	e := &c.descCache[descCacheIndex(query)]
	if e.query == query {
		return e.stmtType, e.outputs, e.inputs, true
	}
	return 0, nil, nil, false
}

func (c *conn) descCacheStore(query string, stmtType int32, outputs, inputs []wire.ColumnDescriptor) {
	c.descCache[descCacheIndex(query)] = descCacheEntry{
		query:    query,
		stmtType: stmtType,
		outputs:  outputs,
		inputs:   inputs,
	}
}

// isTransientAuthFailure reports whether err is a transient SRP authentication
// failure that warrants a connection retry.
func isTransientAuthFailure(err error) bool {
	var se *wire.StatusError
	return errors.As(err, &se) && se.GDSCode() == gdsIscLogin
}

// newConnection creates a new Firebird connection.
func newConnection(ctx context.Context, cfg *Config) (*conn, error) {
	wireCfg := &wire.ProtocolConfig{
		Host:           cfg.Host,
		Port:           cfg.Port,
		Database:       cfg.Database,
		User:           cfg.User,
		Password:       cfg.Password,
		Charset:        cfg.Charset,
		Dialect:        cfg.Dialect,
		WireCrypt:      cfg.WireCrypt,
		WireCryptSet:   true,
		Role:           cfg.Role,
		AuthPluginList: wire.DefaultPluginList,
	}

	var wc *wire.WireConnection
	var err error
	for attempt := range maxConnRetries {
		wc, err = wire.Connect(wireCfg)
		if err == nil {
			break
		}
		if !isTransientAuthFailure(err) || attempt == maxConnRetries-1 {
			break
		}
		// Transient SRP auth failure under burst load: wait briefly with jitter
		// to desynchronize concurrent reconnect attempts, then retry.
		jitter := time.Duration(rand.Intn(40)+10) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jitter):
		}
	}
	if err != nil {
		return nil, err
	}

	return &conn{
		wc:        wc,
		config:    cfg,
		fetchSize: cfg.FetchSize,
	}, nil
}

// Prepare implements driver.Conn.
func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext implements driver.ConnPrepareContext.
func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.bad {
		return nil, driver.ErrBadConn
	}

	stop := c.withCancel(ctx)
	defer stop()

	// Prepare requires a transaction; use existing or persistent auto-tx
	var txHandle int32
	if c.activeTx != 0 {
		txHandle = c.activeTx
	} else {
		var err error
		txHandle, err = c.getAutoTx()
		if err != nil {
			return nil, c.handleRetryableErrorLocked(err)
		}
	}

	// Allocate + prepare in 1 round-trip
	stmtHandle, infoData, err := c.wc.AllocateAndPrepare(txHandle, query, 65535)
	if err != nil {
		if c.activeTx == 0 {
			c.invalidateAutoTx()
		}
		return nil, c.handleRetryableErrorLocked(err)
	}

	// Parse descriptor info
	stmtType, outputs, inputs := wire.ParseSQLDescribeInfo(infoData)

	return &stmt{
		conn:      c,
		handle:    stmtHandle,
		query:     query,
		stmtType:  stmtType,
		outputs:   outputs,
		inputs:    inputs,
		fetchSize: c.fetchSize,
	}, nil
}

// Close implements driver.Conn.
func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	if c.bad {
		return nil
	}
	if c.autoTx != 0 {
		_ = c.wc.Commit(c.autoTx)
		c.autoTx = 0
		c.dirtyAutoTx = false
	}
	c.wc.DrainStatementPool()
	return c.wc.Detach()
}

// getAutoTx returns the persistent auto-commit transaction, creating one if needed.
func (c *conn) getAutoTx() (int32, error) {
	if c.autoTx != 0 {
		return c.autoTx, nil
	}
	tpb := defaultTPB()
	txHandle, err := c.wc.Transaction(tpb)
	if err != nil {
		return 0, c.handleRetryableErrorLocked(err)
	}
	c.autoTx = txHandle
	return txHandle, nil
}

// invalidateAutoTx marks the auto-commit transaction as invalid (e.g. after error).
func (c *conn) invalidateAutoTx() {
	c.autoTx = 0
	c.dirtyAutoTx = false
}

// Begin implements driver.Conn (deprecated, uses BeginTx).
func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx implements driver.ConnBeginTx.
func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.bad {
		return nil, driver.ErrBadConn
	}

	// Commit any pending auto-commit transaction before starting explicit one
	if c.autoTx != 0 {
		_ = c.wc.Commit(c.autoTx)
		c.autoTx = 0
		c.dirtyAutoTx = false
	}

	tpb, err := buildTPB(opts)
	if err != nil {
		return nil, err
	}

	txHandle, err := c.wc.Transaction(tpb)
	if err != nil {
		return nil, c.handleRetryableErrorLocked(err)
	}

	c.activeTx = txHandle

	return &transaction{
		conn:   c,
		handle: txHandle,
	}, nil
}

// ExecContext implements driver.ExecerContext (fast path without prepare).
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.bad {
		return nil, driver.ErrBadConn
	}

	stop := c.withCancel(ctx)
	defer stop()

	// Use active transaction or get persistent auto-commit one
	autoCommit := c.activeTx == 0
	txHandle := c.activeTx
	if autoCommit {
		var err error
		txHandle, err = c.getAutoTx()
		if err != nil {
			return nil, c.handleRetryableErrorLocked(err)
		}
	}

	// Allocate + prepare
	stmtHandle, infoData, err := c.wc.AllocateAndPrepareWithItems(txHandle, query, 65535, wire.PrepareExecInfoItems())
	if err != nil {
		if autoCommit {
			c.invalidateAutoTx()
		}
		return nil, c.handleRetryableErrorLocked(err)
	}

	stmtType, _, inputs := wire.ParseSQLDescribeInfo(infoData)

	// Encode params and execute
	var blr, paramData []byte
	if len(inputs) > 0 && len(args) > 0 {
		if err := c.materializeNamedBlobs(txHandle, inputs, args); err != nil {
			if autoCommit {
				c.invalidateAutoTx()
			}
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, c.handleFatalErrorLocked(err)
		}
		blr = wire.BuildParamBLR(inputs)
		var sw wire.StackWriter
		paramData, err = wire.EncodeParamsOptimalErr(&sw, inputs, args)
		if err != nil {
			if autoCommit {
				c.invalidateAutoTx()
			}
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, err
		}
	}

	// Auto-commit fast path: batch execute+commit_retaining in one flush
	if autoCommit && stmtType != wire.StmtExecProcedure {
		err = c.wc.ExecuteAndCommitRetaining(stmtHandle, txHandle, blr, paramData)
		if err != nil {
			c.invalidateAutoTx()
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, c.handleFatalErrorLocked(err)
		}

		rowsAffected := getRowsAffected(c.wc, stmtHandle, stmtType)
		_ = c.wc.RecycleStatement(stmtHandle, false)

		return &result{
			wc:         c.wc,
			stmtHandle: stmtHandle,
			stmtType:   stmtType,
			cached:     rowsAffected,
			computed:   true,
		}, nil
	}

	if stmtType == wire.StmtExecProcedure {
		outBLR := []byte{}
		_, _, err = c.wc.Execute2(stmtHandle, txHandle, blr, paramData, outBLR, nil)
	} else {
		err = c.wc.Execute(stmtHandle, txHandle, blr, paramData)
	}

	if err != nil {
		if autoCommit {
			c.invalidateAutoTx()
		}
		_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
		return nil, c.handleFatalErrorLocked(err)
	}

	if autoCommit {
		if err := c.wc.CommitRetaining(txHandle); err != nil {
			c.invalidateAutoTx()
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, c.handleFatalErrorLocked(err)
		}
	}

	rowsAffected := getRowsAffected(c.wc, stmtHandle, stmtType)
	_ = c.wc.RecycleStatement(stmtHandle, false)

	return &result{
		wc:         c.wc,
		stmtHandle: stmtHandle,
		stmtType:   stmtType,
		cached:     rowsAffected,
		computed:   true,
	}, nil
}

// QueryContext implements driver.QueryerContext (fast path without prepare).
func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.bad {
		return nil, driver.ErrBadConn
	}

	stop := c.withCancel(ctx)
	defer stop()

	// Use active transaction or get persistent auto-commit one
	autoCommit := c.activeTx == 0
	txHandle := c.activeTx
	if autoCommit {
		var err error
		txHandle, err = c.getAutoTx()
		if err != nil {
			return nil, c.handleRetryableErrorLocked(err)
		}
	}

	// Allocate + prepare
	stmtHandle, infoData, err := c.wc.AllocateAndPrepare(txHandle, query, 65535)
	if err != nil {
		if autoCommit {
			c.invalidateAutoTx()
		}
		return nil, c.handleRetryableErrorLocked(err)
	}

	// Use cached descriptors if available, otherwise parse and cache
	stmtType, outputs, inputs, cached := c.descCacheLookup(query)
	if !cached {
		stmtType, outputs, inputs = wire.ParseSQLDescribeInfo(infoData)
		c.descCacheStore(query, stmtType, outputs, inputs)
	}

	// Encode params and execute
	var blr, paramData []byte
	if len(inputs) > 0 && len(args) > 0 {
		if err := c.materializeNamedBlobs(txHandle, inputs, args); err != nil {
			if autoCommit {
				c.invalidateAutoTx()
			}
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, c.handleFatalErrorLocked(err)
		}
		blr = wire.BuildParamBLR(inputs)
		var sw wire.StackWriter
		paramData, err = wire.EncodeParamsOptimalErr(&sw, inputs, args)
		if err != nil {
			if autoCommit {
				c.invalidateAutoTx()
			}
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, err
		}
	}

	eof := false
	hasCursor := true
	var initialRows [][]any
	if stmtType == wire.StmtExecProcedure {
		outBLR := wire.BuildBLR(outputs)
		msgs, row, execErr := c.wc.Execute2(stmtHandle, txHandle, blr, paramData, outBLR, outputs)
		if execErr != nil {
			if autoCommit {
				c.invalidateAutoTx()
			}
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, c.handleFatalErrorLocked(execErr)
		}
		if msgs > 0 {
			initialRows = [][]any{row}
		}
		eof = true
		hasCursor = false
	} else {
		err = c.wc.Execute(stmtHandle, txHandle, blr, paramData)
		if err != nil {
			if autoCommit {
				c.invalidateAutoTx()
			}
			_ = c.wc.FreeStatement(stmtHandle, wire.DSQLDrop)
			return nil, c.handleFatalErrorLocked(err)
		}
	}

	return &rows{
		conn:         c,
		ctx:          ctx,
		stmtHandle:   stmtHandle,
		txHandle:     txHandle,
		outputs:      outputs,
		fetchSize:    c.fetchSize,
		autoFreeStmt: autoCommit,
		autoCommitTx: autoCommit,
		hasCursor:    hasCursor,
		buf:          initialRows,
		eof:          eof,
		hasBlobs:     hasBlobs(outputs),
	}, nil
}

// Ping implements driver.Pinger.
func (c *conn) Ping(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.bad {
		return driver.ErrBadConn
	}
	return c.pingLocked(ctx)
}

var pingInfoItems = []byte{wire.IscInfoBaseLevel}

// ResetSession implements driver.SessionResetter.
func (c *conn) ResetSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.bad {
		return driver.ErrBadConn
	}
	return c.pingLocked(ctx)
}

// IsValid implements driver.Validator.
func (c *conn) IsValid() bool {
	return !c.closed && !c.bad
}

// CheckNamedValue implements driver.NamedValueChecker.
func (c *conn) CheckNamedValue(nv *driver.NamedValue) error {
	switch v := nv.Value.(type) {
	case nil, int64, float64, bool, string, []byte, time.Time:
		return nil
	case int:
		nv.Value = int64(v)
	case int8:
		nv.Value = int64(v)
	case int16:
		nv.Value = int64(v)
	case int32:
		nv.Value = int64(v)
	case uint:
		nv.Value = int64(v)
	case uint8:
		nv.Value = int64(v)
	case uint16:
		nv.Value = int64(v)
	case uint32:
		nv.Value = int64(v)
	case uint64:
		nv.Value = int64(v)
	case float32:
		nv.Value = float64(v)
	default:
		return driver.ErrSkip
	}
	return nil
}

// --- Helpers ---

func (c *conn) materializeNamedBlobs(txHandle int32, cols []wire.ColumnDescriptor, values []driver.NamedValue) error {
	for i, col := range cols {
		if col.SQLType&^int32(1) != wire.SQLBlob || i >= len(values) || values[i].Value == nil {
			continue
		}
		var blobData []byte
		switch v := values[i].Value.(type) {
		case []byte:
			blobData = v
		case string:
			if col.SubType == 1 {
				s, err := fbcharset.Encode(fbcharset.CharsetID(c.config.Charset), v)
				if err != nil {
					return fmt.Errorf("encode text blob param %d: %w", i, err)
				}
				blobData = []byte(s)
			} else {
				blobData = []byte(v)
			}
		default:
			continue
		}
		blobID, err := c.wc.WriteBlobData(txHandle, blobData)
		if err != nil {
			return fmt.Errorf("create blob param %d: %w", i, c.handleFatalErrorLocked(err))
		}
		values[i].Value = blobID
	}
	return nil
}

func (c *conn) pingLocked(ctx context.Context) error {
	if c.closed || c.bad {
		return driver.ErrBadConn
	}

	stop := c.withCancel(ctx)
	defer stop()

	_, err := c.wc.InfoDatabase(pingInfoItems, 256)
	return c.handleRetryableErrorLocked(err)
}

func (c *conn) handleRetryableErrorLocked(err error) error {
	return c.handleErrorLocked(err, true)
}

func (c *conn) handleFatalErrorLocked(err error) error {
	return c.handleErrorLocked(err, false)
}

func (c *conn) handleErrorLocked(err error, retryable bool) error {
	if err == nil {
		return nil
	}
	if !isTransportError(err) {
		return err
	}
	c.markBadLocked()
	if retryable {
		return wrapBadConn(err)
	}
	return err
}

func (c *conn) markBadLocked() {
	if c.bad {
		return
	}
	c.bad = true
	c.activeTx = 0
	c.autoTx = 0
	c.dirtyAutoTx = false
	if c.wc != nil {
		_ = c.wc.CloseTransport()
	}
}

// withCancel starts a goroutine that monitors ctx and cancels the wire
// connection if ctx is done. Returns a stop function that must be called
// when the operation completes. stop() blocks until the goroutine exits,
// guaranteeing Cancel() won't fire after stop() returns.
func (c *conn) withCancel(ctx context.Context) (stop func()) {
	if ctx == nil || ctx.Done() == nil {
		return func() {}
	}

	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			_ = c.wc.Cancel(wire.CancelRaise)
		case <-done:
		}
	}()

	return func() {
		close(done)
		<-finished
	}
}

// cachedDefaultTPB is the pre-built TPB for default auto-commit transactions.
var cachedDefaultTPB = []byte{
	wire.IscTpbVersion3,
	wire.IscTpbReadCommitted,
	wire.IscTpbRecVersion,
	wire.IscTpbWrite,
	wire.IscTpbWait,
}

func defaultTPB() []byte {
	return cachedDefaultTPB
}

// Pre-built TPBs for common isolation levels (avoids TPBBuilder allocation).
var (
	tpbDefaultWrite = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbReadCommitted,
		wire.IscTpbRecVersion,
		wire.IscTpbWait,
		wire.IscTpbWrite,
	}
	tpbDefaultRead = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbReadCommitted,
		wire.IscTpbRecVersion,
		wire.IscTpbWait,
		wire.IscTpbRead,
	}
	tpbReadCommittedWrite = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbReadCommitted,
		wire.IscTpbRecVersion,
		wire.IscTpbWrite,
	}
	tpbReadCommittedRead = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbReadCommitted,
		wire.IscTpbRecVersion,
		wire.IscTpbRead,
	}
	tpbSnapshotWrite = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbConcurrency,
		wire.IscTpbWrite,
	}
	tpbSnapshotRead = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbConcurrency,
		wire.IscTpbRead,
	}
	tpbSerializableWrite = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbConsistency,
		wire.IscTpbWrite,
	}
	tpbSerializableRead = []byte{
		wire.IscTpbVersion3,
		wire.IscTpbConsistency,
		wire.IscTpbRead,
	}
)

func buildTPB(opts driver.TxOptions) ([]byte, error) {
	// Fast path: use pre-built TPBs for common combinations (zero allocs).
	switch opts.Isolation {
	case 0: // LevelDefault
		if opts.ReadOnly {
			return tpbDefaultRead, nil
		}
		return tpbDefaultWrite, nil
	case 2: // LevelReadCommitted
		if opts.ReadOnly {
			return tpbReadCommittedRead, nil
		}
		return tpbReadCommittedWrite, nil
	case 4, 5: // LevelRepeatableRead, LevelSnapshot
		if opts.ReadOnly {
			return tpbSnapshotRead, nil
		}
		return tpbSnapshotWrite, nil
	case 6: // LevelSerializable
		if opts.ReadOnly {
			return tpbSerializableRead, nil
		}
		return tpbSerializableWrite, nil
	case 1: // LevelReadUncommitted
		return nil, errReadUncommitted
	case 3: // LevelWriteCommitted
		return nil, errWriteCommitted
	case 7: // LevelLinearizable
		return nil, errLinearizable
	default:
		if opts.ReadOnly {
			return tpbDefaultRead, nil
		}
		return tpbDefaultWrite, nil
	}
}

var rowsAffectedInfoItems = []byte{wire.IscInfoSQLRecords}

func getRowsAffected(wc *wire.WireConnection, stmtHandle int32, stmtType int32) int64 {
	data, err := wc.InfoSQL(stmtHandle, rowsAffectedInfoItems, 256)
	if err != nil {
		return 0
	}

	_, insertCount, updateCount, deleteCount := wire.ParseRecordCounts(data)

	switch stmtType {
	case wire.StmtInsert:
		return insertCount
	case wire.StmtUpdate:
		return updateCount
	case wire.StmtDelete:
		return deleteCount
	default:
		return insertCount + updateCount + deleteCount
	}
}
