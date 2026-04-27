package firebird

import (
	"context"
	"database/sql/driver"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// stmt implements driver.Stmt and optional context interfaces.
type stmt struct {
	conn      *conn
	handle    int32
	query     string
	stmtType  int32
	outputs   []wire.ColumnDescriptor
	inputs    []wire.ColumnDescriptor
	fetchSize int
	closed    bool
	inputBLR  []byte
	outputBLR []byte
}

var (
	_ driver.Stmt             = (*stmt)(nil)
	_ driver.StmtExecContext  = (*stmt)(nil)
	_ driver.StmtQueryContext = (*stmt)(nil)
)

// Close implements driver.Stmt.
func (s *stmt) Close() error {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	if s.conn.closed || s.conn.bad {
		return nil
	}

	err := s.conn.handleFatalErrorLocked(s.conn.wc.RecycleStatement(s.handle, false))

	// Commit pending auto-commit changes (like nakagami's freeStatement pattern)
	if s.conn.dirtyAutoTx && s.conn.activeTx == 0 && s.conn.autoTx != 0 {
		if cerr := s.conn.wc.CommitRetaining(s.conn.autoTx); cerr != nil {
			s.conn.invalidateAutoTx()
			cerr = s.conn.handleFatalErrorLocked(cerr)
			if err == nil {
				err = cerr
			}
		}
		s.conn.dirtyAutoTx = false
	}

	return err
}

// NumInput implements driver.Stmt.
func (s *stmt) NumInput() int {
	return len(s.inputs)
}

// Exec implements driver.Stmt (deprecated; uses ExecContext).
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	named := valuesToNamed(args)
	return s.ExecContext(context.Background(), named)
}

// Query implements driver.Stmt (deprecated; uses QueryContext).
func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	named := valuesToNamed(args)
	return s.QueryContext(context.Background(), named)
}

// ExecContext implements driver.StmtExecContext.
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()

	if s.closed || s.conn.closed || s.conn.bad {
		return nil, driver.ErrBadConn
	}

	stop := s.conn.withCancel(ctx)
	defer stop()

	autoCommit := s.conn.activeTx == 0
	txHandle := s.conn.activeTx

	var blr, paramData []byte
	if len(s.inputs) > 0 && len(args) > 0 {
		if autoCommit {
			// For blobs we need the auto-tx first to materialize them
			hasBlobParams := false
			for i, col := range s.inputs {
				if col.SQLType&^int32(1) == wire.SQLBlob && i < len(args) && args[i].Value != nil {
					hasBlobParams = true
					break
				}
			}
			if hasBlobParams {
				var err error
				txHandle, err = s.conn.getAutoTx()
				if err != nil {
					return nil, s.conn.handleRetryableErrorLocked(err)
				}
				if err := s.conn.materializeNamedBlobs(txHandle, s.inputs, args); err != nil {
					s.conn.invalidateAutoTx()
					return nil, s.conn.handleFatalErrorLocked(err)
				}
			}
		} else {
			if err := s.conn.materializeNamedBlobs(txHandle, s.inputs, args); err != nil {
				return nil, s.conn.handleFatalErrorLocked(err)
			}
		}

		blr = s.paramBLR()
		var sw wire.StackWriter
		var err error
		paramData, err = wire.EncodeParamsOptimalErrWithCodec(&sw, s.inputs, args, s.conn.wc.TextCodec())
		if err != nil {
			if autoCommit {
				s.conn.invalidateAutoTx()
			}
			return nil, err
		}
	}

	// Get auto-commit tx if needed
	if autoCommit && txHandle == 0 {
		var err error
		txHandle, err = s.conn.getAutoTx()
		if err != nil {
			return nil, s.conn.handleRetryableErrorLocked(err)
		}
	}

	// Execute (no per-exec commit — commit is deferred to stmt.Close)
	var err error
	if s.stmtType == wire.StmtExecProcedure {
		outBLR := []byte{}
		_, _, err = s.conn.wc.Execute2(s.handle, txHandle, blr, paramData, outBLR)
	} else {
		err = s.conn.wc.Execute(s.handle, txHandle, blr, paramData)
	}

	if err != nil {
		if autoCommit {
			s.conn.invalidateAutoTx()
		}
		return nil, s.conn.handleFatalErrorLocked(err)
	}

	if autoCommit {
		s.conn.dirtyAutoTx = true
	}

	return &result{
		wc:         s.conn.wc,
		stmtHandle: s.handle,
		stmtType:   s.stmtType,
	}, nil
}

// QueryContext implements driver.StmtQueryContext.
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()

	if s.closed || s.conn.closed || s.conn.bad {
		return nil, driver.ErrBadConn
	}

	stop := s.conn.withCancel(ctx)
	defer stop()

	autoCommit := s.conn.activeTx == 0
	txHandle := s.conn.activeTx
	if autoCommit {
		var err error
		txHandle, err = s.conn.getAutoTx()
		if err != nil {
			return nil, s.conn.handleRetryableErrorLocked(err)
		}
	}

	var blr, paramData []byte
	if len(s.inputs) > 0 && len(args) > 0 {
		if err := s.conn.materializeNamedBlobs(txHandle, s.inputs, args); err != nil {
			if autoCommit {
				s.conn.invalidateAutoTx()
			}
			return nil, s.conn.handleFatalErrorLocked(err)
		}
		blr = s.paramBLR()
		var sw wire.StackWriter
		var err error
		paramData, err = wire.EncodeParamsOptimalErrWithCodec(&sw, s.inputs, args, s.conn.wc.TextCodec())
		if err != nil {
			if autoCommit {
				s.conn.invalidateAutoTx()
			}
			return nil, err
		}
	}

	outBLR := s.resultBLR()
	var err error
	if s.stmtType == wire.StmtExecProcedure {
		_, _, err = s.conn.wc.Execute2(s.handle, txHandle, blr, paramData, outBLR)
	} else {
		err = s.conn.wc.Execute(s.handle, txHandle, blr, paramData)
	}
	if err != nil {
		if autoCommit {
			s.conn.invalidateAutoTx()
		}
		return nil, s.conn.handleFatalErrorLocked(err)
	}

	return &rows{
		conn:         s.conn,
		stmtHandle:   s.handle,
		txHandle:     txHandle,
		outputs:      s.outputs,
		fetchSize:    s.fetchSize,
		autoFreeStmt: false, // prepared stmt manages its own lifetime
		autoCommitTx: autoCommit,
		blr:          outBLR,
		hasBlobs:     hasBlobs(s.outputs),
	}, nil
}

func (s *stmt) paramBLR() []byte {
	if len(s.inputs) == 0 {
		return nil
	}
	if s.inputBLR == nil {
		s.inputBLR = wire.BuildParamBLR(s.inputs)
	}
	return s.inputBLR
}

func (s *stmt) resultBLR() []byte {
	if len(s.outputs) == 0 {
		return nil
	}
	if s.outputBLR == nil {
		s.outputBLR = wire.BuildBLR(s.outputs)
	}
	return s.outputBLR
}

func valuesToNamed(args []driver.Value) []driver.NamedValue {
	// Fast path: for small arg counts, use stack allocation
	if len(args) <= 16 {
		var stackNamed [16]driver.NamedValue
		for i, v := range args {
			stackNamed[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
		}
		return stackNamed[:len(args)]
	}
	// Slow path: heap allocation for larger arg counts
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return named
}
