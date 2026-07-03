package firebird

import (
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// transaction implements driver.Tx.
type transaction struct {
	conn   *conn
	handle int32
	done   bool
}

var _ driver.Tx = (*transaction)(nil)

// Commit implements driver.Tx.
func (tx *transaction) Commit() error {
	tx.conn.mu.Lock()

	if tx.done {
		tx.conn.mu.Unlock()
		return nil
	}
	tx.done = true
	tx.conn.activeTx = 0
	if tx.conn.closed || tx.conn.bad {
		tx.conn.mu.Unlock()
		return driver.ErrBadConn
	}
	err := tx.conn.handleFatalErrorLocked(tx.conn.wc.Commit(tx.handle))
	tx.conn.mu.Unlock()
	return err
}

// Rollback implements driver.Tx.
func (tx *transaction) Rollback() error {
	tx.conn.mu.Lock()

	if tx.done {
		tx.conn.mu.Unlock()
		return nil
	}
	tx.done = true
	tx.conn.activeTx = 0
	if tx.conn.closed || tx.conn.bad {
		tx.conn.mu.Unlock()
		return driver.ErrBadConn
	}
	err := tx.conn.handleFatalErrorLocked(tx.conn.wc.Rollback(tx.handle))
	tx.conn.mu.Unlock()
	return err
}

// Errores de niveles de aislamiento de database/sql sin equivalente en Firebird.
var (
	errReadUncommitted = errors.New("firebird: isolation level READ UNCOMMITTED not supported")
	errWriteCommitted  = errors.New("firebird: isolation level WRITE COMMITTED not supported")
	errLinearizable    = errors.New("firebird: isolation level LINEARIZABLE not supported")
)

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
		// Firebird no tiene REPEATABLE READ; se mapea a SNAPSHOT, que es
		// estrictamente mas fuerte (mismo criterio que jaybird).
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
		return nil, fmt.Errorf("firebird: isolation level %d not supported", opts.Isolation)
	}
}
