// soak: estrés sostenido del driver sobre un pool, con muestreo periódico de
// goroutines/heap y reporte a archivo. Detecta fugas gruesas (goroutines,
// memoria) y verifica recuperación ante caídas del servidor: el server puede
// matarse/reiniciarse a mitad de corrida y el harness lo registra y sigue.
//
//	cd scripts/soak
//	go run . -duration 30m -out soak_report.txt
//
// La base indicada debe ser DEDICADA (hace RECREATE TABLE soak_*).
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/gabrielxsuarez/go-firebird-driver"
)

var (
	flagDSN      = flag.String("dsn", "firebird://sysdba:masterkey@localhost:3063//var/lib/firebird/data/driver.fdb?charset=UTF8", "DSN de una base DEDICADA para el soak")
	flagDuration = flag.Duration("duration", 30*time.Minute, "duración total")
	flagWorkers  = flag.Int("workers", 16, "goroutines de carga")
	flagMaxConns = flag.Int("maxconns", 8, "MaxOpenConns del pool")
	flagOut      = flag.String("out", "soak_report.txt", "archivo de reporte")
	flagCancels  = flag.Bool("cancels", true, "incluir queries canceladas por contexto en el workload")
)

type counters struct {
	ops        atomic.Int64 // operaciones exitosas
	sqlErrs    atomic.Int64 // errores SQL/driver inesperados
	connErrs   atomic.Int64 // errores de conexión/transporte (esperables si matás el server)
	cancels    atomic.Int64 // cancelaciones por contexto (parte del workload)
	rowsRead   atomic.Int64
	lastErrMu  sync.Mutex
	lastErr    string
	lastErrCat string
	errKinds   map[string]int
}

func (c *counters) fail(cat string, err error) {
	if cat == "conn" {
		c.connErrs.Add(1)
	} else {
		c.sqlErrs.Add(1)
	}
	c.lastErrMu.Lock()
	c.lastErr = err.Error()
	if len(c.lastErr) > 200 {
		c.lastErr = c.lastErr[:200]
	}
	c.lastErrCat = cat
	if c.errKinds == nil {
		c.errKinds = make(map[string]int)
	}
	kind := c.lastErr
	if len(kind) > 110 {
		kind = kind[:110]
	}
	c.errKinds[kind]++
	c.lastErrMu.Unlock()
}

func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, m := range []string{"bad connection", "connection refused", "forcibly closed",
		"broken pipe", "reset by peer", "i/o timeout", "unavailable", "shutdown", "eof",
		"connectex", "wsarecv", "wsasend"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func main() {
	flag.Parse()
	rep, err := os.Create(*flagOut)
	if err != nil {
		fmt.Println("no se pudo crear el reporte:", err)
		os.Exit(1)
	}
	defer rep.Close()
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		fmt.Println(line)
		fmt.Fprintln(rep, line)
	}

	logf("# soak go-firebird-driver")
	logf("# inicio=%s duracion=%s workers=%d maxconns=%d", time.Now().Format(time.RFC3339), *flagDuration, *flagWorkers, *flagMaxConns)
	logf("# dsn=%s", redact(*flagDSN))
	logf("# go=%s GOMAXPROCS=%d", runtime.Version(), runtime.GOMAXPROCS(0))

	db, err := sql.Open("firebird", *flagDSN)
	if err != nil {
		logf("FATAL open: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(*flagMaxConns)
	db.SetMaxIdleConns(*flagMaxConns)
	db.SetConnMaxLifetime(2 * time.Minute) // fuerza churn de conexiones

	if err := setup(db); err != nil {
		logf("FATAL setup: %v", err)
		os.Exit(1)
	}
	logf("# setup OK (soak_rows=2000 filas, soak_blob=64KB)")

	// Baseline tras warm-up.
	var cnt counters
	warm(db)
	runtime.GC()
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	baseGoroutines := runtime.NumGoroutine()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	baseHeap := ms.HeapAlloc
	logf("# baseline: goroutines=%d heapAlloc=%s", baseGoroutines, mb(baseHeap))
	logf("#")
	logf("# elapsed goroutines heapAlloc heapSys ops ops/s rowsRead sqlErrs connErrs cancels")

	ctx, cancel := context.WithTimeout(context.Background(), *flagDuration)
	defer cancel()

	var wg sync.WaitGroup
	for w := range *flagWorkers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(ctx, db, &cnt, id)
		}(w)
	}

	// Sampler cada 10s.
	start := time.Now()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	var prevOps int64
	outage := false
	var outageStart time.Time
sample:
	for {
		select {
		case <-ctx.Done():
			break sample
		case <-tick.C:
			runtime.ReadMemStats(&ms)
			ops := cnt.ops.Load()
			rate := float64(ops-prevOps) / 10
			prevOps = ops
			logf("%7s %6d %9s %8s %9d %6.0f %9d %5d %5d %5d",
				time.Since(start).Round(time.Second), runtime.NumGoroutine(),
				mb(ms.HeapAlloc), mb(ms.HeapSys), ops, rate, cnt.rowsRead.Load(),
				cnt.sqlErrs.Load(), cnt.connErrs.Load(), cnt.cancels.Load())
			// detección de outage: tasa 0 + errores de conexión creciendo
			if rate == 0 && cnt.connErrs.Load() > 0 && !outage {
				outage = true
				outageStart = time.Now()
				logf("! OUTAGE detectado (ops/s=0, connErrs creciendo) — si mataste el server, reinicialo")
			}
			if outage && rate > 0 {
				logf("! RECUPERADO tras %s de outage", time.Since(outageStart).Round(time.Second))
				outage = false
			}
		}
	}
	wg.Wait()

	// Veredicto: cerrar pool, GC doble, comparar contra baseline.
	db.Close()
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()
	runtime.ReadMemStats(&ms)
	logf("#")
	logf("# FIN: ops=%d rows=%d sqlErrs=%d connErrs=%d cancels=%d",
		cnt.ops.Load(), cnt.rowsRead.Load(), cnt.sqlErrs.Load(), cnt.connErrs.Load(), cnt.cancels.Load())
	cnt.lastErrMu.Lock()
	for kind, n := range cnt.errKinds {
		logf("# error x%d: %s", n, kind)
	}
	cnt.lastErrMu.Unlock()
	logf("# goroutines: baseline=%d final=%d (tras Close+GC)", baseGoroutines, finalGoroutines)
	logf("# heapAlloc:  baseline=%s final=%s (tras GC)", mb(baseHeap), mb(ms.HeapAlloc))

	verdict := "OK"
	if finalGoroutines > baseGoroutines+5 {
		verdict = "SOSPECHA DE FUGA DE GOROUTINES"
	}
	if ms.HeapAlloc > baseHeap*3 && ms.HeapAlloc > 64<<20 {
		verdict = "SOSPECHA DE FUGA DE MEMORIA"
	}
	if cnt.sqlErrs.Load() > 0 {
		verdict += " (con errores SQL inesperados: revisar)"
	}
	logf("# VEREDICTO: %s", verdict)
}

func redact(dsn string) string {
	if i := strings.Index(dsn, "://"); i >= 0 {
		if j := strings.Index(dsn[i+3:], "@"); j >= 0 {
			return dsn[:i+3] + "***@" + dsn[i+3+j+1:]
		}
	}
	return dsn
}

func mb(b uint64) string { return fmt.Sprintf("%.1fMB", float64(b)/(1<<20)) }

func setup(db *sql.DB) error {
	db.Exec("DROP TABLE soak_rows")
	db.Exec("DROP TABLE soak_blob")
	db.Exec("DROP TABLE soak_ins")
	stmts := []string{
		"CREATE TABLE soak_rows (id INTEGER NOT NULL PRIMARY KEY, name VARCHAR(40), val BIGINT, price DOUBLE PRECISION, created TIMESTAMP)",
		"CREATE TABLE soak_blob (id INTEGER NOT NULL PRIMARY KEY, b BLOB SUB_TYPE 0)",
		"CREATE TABLE soak_ins (id INTEGER, name VARCHAR(40), val BIGINT)",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", s[:30], err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	ins, err := tx.Prepare("INSERT INTO soak_rows VALUES (?,?,?,?,?)")
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range 2000 {
		if _, err := ins.Exec(i, fmt.Sprintf("row-%d", i), int64(i)*7919, float64(i)*1.25, now); err != nil {
			return err
		}
	}
	ins.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	blob := make([]byte, 64*1024)
	_, _ = rand.Read(blob)
	if _, err := db.Exec("INSERT INTO soak_blob VALUES (1, ?)", blob); err != nil {
		return err
	}
	return nil
}

func warm(db *sql.DB) {
	var n int
	for range 8 {
		_ = db.QueryRow("SELECT COUNT(*) FROM soak_rows").Scan(&n)
	}
}

func worker(ctx context.Context, db *sql.DB, cnt *counters, id int) {
	i := 0
	for ctx.Err() == nil {
		i++
		var err error
		var op string
		switch {
		case i%20 == 19 && *flagCancels: // 5%: query cancelada por contexto (workload esperado)
			op = "cancelled"
			err = doCancelled(ctx, db)
			if err == nil {
				cnt.cancels.Add(1)
				continue
			}
		case i%10 == 9: // 10%: blob 64KB
			op = "blob"
			err = doBlob(ctx, db)
		case i%7 == 6: // ~14%: tx con inserts preparados
			op = "insertTx"
			err = doInsertTx(ctx, db, id, i)
		case i%4 == 3: // ~19%: scan de 2000 filas
			op = "scan"
			err = doScan(ctx, db, cnt)
		default: // ~52%: point select
			op = "point"
			err = doPoint(ctx, db, i)
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", op, err)
		}
		if err != nil {
			if ctx.Err() != nil {
				return // fin de la corrida, no contar
			}
			if isConnErr(err) {
				cnt.fail("conn", err)
				time.Sleep(250 * time.Millisecond) // no martillar durante un outage
			} else {
				cnt.fail("sql", err)
			}
			continue
		}
		cnt.ops.Add(1)
	}
}

func doPoint(ctx context.Context, db *sql.DB, i int) error {
	var name string
	var val int64
	return db.QueryRowContext(ctx, "SELECT name, val FROM soak_rows WHERE id = ?", i%2000).Scan(&name, &val)
}

func doScan(ctx context.Context, db *sql.DB, cnt *counters) error {
	rows, err := db.QueryContext(ctx, "SELECT id, name, val, price, created FROM soak_rows")
	if err != nil {
		return err
	}
	defer rows.Close()
	var id int
	var name string
	var val int64
	var price float64
	var created time.Time
	n := 0
	for rows.Next() {
		if err := rows.Scan(&id, &name, &val, &price, &created); err != nil {
			return err
		}
		n++
	}
	cnt.rowsRead.Add(int64(n))
	return rows.Err()
}

func doInsertTx(ctx context.Context, db *sql.DB, id, i int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO soak_ins VALUES (?,?,?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	for k := range 10 {
		if _, err := stmt.Exec(i+k, fmt.Sprintf("w%d-%d", id, k), int64(k)); err != nil {
			stmt.Close()
			tx.Rollback()
			return err
		}
	}
	stmt.Close()
	if i%2 == 0 {
		return tx.Commit()
	}
	return tx.Rollback() // mitad rollback: ejercita ambos caminos
}

func doBlob(ctx context.Context, db *sql.DB) error {
	var b []byte
	if err := db.QueryRowContext(ctx, "SELECT b FROM soak_blob WHERE id = 1").Scan(&b); err != nil {
		return err
	}
	if len(b) != 64*1024 {
		return fmt.Errorf("blob corrupto: %d bytes", len(b))
	}
	return nil
}

func doCancelled(ctx context.Context, db *sql.DB) error {
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	var n int64
	// Query pesada NO recursiva: un CTE recursivo profundo revienta por sí solo
	// contra el límite de clones de recursión de Firebird (GDS 335544663).
	err := db.QueryRowContext(cctx, `
		SELECT COUNT(*) FROM soak_rows a, soak_rows b, soak_rows c`).Scan(&n)
	if err == nil {
		return nil // terminó antes del timeout: cuenta como op normal igual
	}
	if cctx.Err() != nil && ctx.Err() == nil {
		return nil // cancelación esperada
	}
	return err
}
