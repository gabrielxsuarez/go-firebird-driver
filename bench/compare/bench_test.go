package compare

// Harness head-to-head go-firebird-driver vs nakagami/firebirdsql (Fase 3.2/6.3).
//
// Ambos drivers registran el nombre "firebirdsql" en database/sql, así que no
// pueden convivir en un proceso: la variante se elige con build tags.
//
//	go test -bench . -benchmem -count=N .          > ours.txt
//	go test -bench . -benchmem -count=N -tags nak . > nak.txt
//	benchstat ours.txt nak.txt
//
// El servidor y la base se configuran con FBBENCH_TARGET (default: FB3 nativo
// local). La base debe existir; ver README.md. Los escenarios recrean sus
// tablas en el primer uso del binario (RECREATE TABLE), así ambas variantes
// miden sobre datos idénticos.

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func target() string {
	if t := os.Getenv("FBBENCH_TARGET"); t != "" {
		return t
	}
	return "127.0.0.1:3050/C:/AlfaBeta/firebird/tmp/bench.fdb"
}

func openDB(b *testing.B, maxConns int) *sql.DB {
	b.Helper()
	db, err := sql.Open(driverName, dsn(target()))
	if err != nil {
		b.Fatalf("open (%s): %v", driverLabel, err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	if err := db.Ping(); err != nil {
		db.Close()
		b.Fatalf("ping (%s): %v", driverLabel, err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

// --- setup de datos (una vez por proceso) ---

var setupOnce sync.Once

func setupData(b *testing.B) {
	b.Helper()
	setupOnce.Do(func() {
		db, err := sql.Open(driverName, dsn(target()))
		if err != nil {
			b.Fatalf("setup open: %v", err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)

		mustExec := func(q string, args ...any) {
			if _, err := db.Exec(q, args...); err != nil {
				b.Fatalf("setup %q: %v", q[:min(40, len(q))], err)
			}
		}

		// Tabla de fetch masivo: 10 columnas mixtas, 10k filas.
		mustExec(`RECREATE TABLE bench_rows (
			id INTEGER NOT NULL,
			name VARCHAR(30),
			val BIGINT,
			price DOUBLE PRECISION,
			created TIMESTAMP,
			qty INTEGER,
			code VARCHAR(10),
			amount NUMERIC(12,2),
			flag SMALLINT,
			note VARCHAR(50))`)
		tx, err := db.Begin()
		if err != nil {
			b.Fatalf("setup begin: %v", err)
		}
		ins, err := tx.Prepare("INSERT INTO bench_rows VALUES (?,?,?,?,?,?,?,?,?,?)")
		if err != nil {
			b.Fatalf("setup prepare: %v", err)
		}
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 10000; i++ {
			_, err := ins.Exec(i, fmt.Sprintf("name-%d", i), int64(i)*1000003,
				float64(i)*1.5, base.Add(time.Duration(i)*time.Second), i%100,
				fmt.Sprintf("C%05d", i%1000), fmt.Sprintf("%d.%02d", i, i%100),
				i%2, strings.Repeat("x", 20+i%30))
			if err != nil {
				b.Fatalf("setup insert %d: %v", i, err)
			}
		}
		ins.Close()
		if err := tx.Commit(); err != nil {
			b.Fatalf("setup commit: %v", err)
		}

		// Blobs de 1KB y 1MB.
		mustExec("RECREATE TABLE bench_blob (id INTEGER NOT NULL, b BLOB SUB_TYPE 0)")
		blob1k := make([]byte, 1024)
		blob1m := make([]byte, 1024*1024)
		_, _ = rand.Read(blob1k)
		_, _ = rand.Read(blob1m)
		mustExec("INSERT INTO bench_blob VALUES (1, ?)", blob1k)
		mustExec("INSERT INTO bench_blob VALUES (2, ?)", blob1m)

		// Tabla destino de inserts.
		mustExec("RECREATE TABLE bench_ins (id INTEGER, name VARCHAR(30), val BIGINT, price DOUBLE PRECISION)")
	})
}

// --- Escenarios ---

// Connect+auth+detach: costo del handshake completo (SRP + attach).
func BenchmarkConnect(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		db, err := sql.Open(driverName, dsn(target()))
		if err != nil {
			b.Fatal(err)
		}
		if err := db.Ping(); err != nil {
			b.Fatal(err)
		}
		db.Close()
	}
}

// SELECT 1 preparado en loop: overhead fijo por roundtrip.
func BenchmarkSelect1Prepared(b *testing.B) {
	db := openDB(b, 1)
	stmt, err := db.Prepare("SELECT 1 FROM RDB$DATABASE")
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n int
		if err := stmt.QueryRow().Scan(&n); err != nil {
			b.Fatal(err)
		}
	}
}

// SELECT 10k filas × 10 columnas mixtas: throughput de fetch + decode.
func BenchmarkFetch10kx10(b *testing.B) {
	setupData(b)
	db := openDB(b, 1)
	dest := make([]any, 10)
	ptrs := make([]any, 10)
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	b.ReportAllocs()
	b.ResetTimer()
	totalRows := 0
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT * FROM bench_rows")
		if err != nil {
			b.Fatal(err)
		}
		n := 0
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				b.Fatal(err)
			}
			n++
		}
		if err := rows.Err(); err != nil {
			b.Fatal(err)
		}
		rows.Close()
		if n != 10000 {
			b.Fatalf("esperaba 10000 filas, leí %d", n)
		}
		totalRows += n
	}
	b.ReportMetric(float64(totalRows)/b.Elapsed().Seconds(), "rows/s")
}

// INSERT preparado en loop, transacción por lote de 100.
func BenchmarkInsertPrepared(b *testing.B) {
	setupData(b)
	db := openDB(b, 1)
	if _, err := db.Exec("DELETE FROM bench_ins"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	const batch = 100
	done := 0
	for done < b.N {
		n := min(batch, b.N-done)
		tx, err := db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		stmt, err := tx.Prepare("INSERT INTO bench_ins VALUES (?,?,?,?)")
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, err := stmt.Exec(done+i, "insert-bench-name", int64(done+i)*7919, float64(i)*2.25); err != nil {
				b.Fatal(err)
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
		done += n
	}
}

func benchBlob(b *testing.B, id, wantLen int) {
	setupData(b)
	db := openDB(b, 1)
	stmt, err := db.Prepare("SELECT b FROM bench_blob WHERE id = ?")
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var data []byte
		if err := stmt.QueryRow(id).Scan(&data); err != nil {
			b.Fatal(err)
		}
		if len(data) != wantLen {
			b.Fatalf("blob de %d bytes, esperaba %d", len(data), wantLen)
		}
	}
	b.SetBytes(int64(wantLen))
}

// SELECT de blob de 1KB / 1MB: camino de blob.
func BenchmarkBlob1KB(b *testing.B) { benchBlob(b, 1, 1024) }
func BenchmarkBlob1MB(b *testing.B) { benchBlob(b, 2, 1024*1024) }

// Pool con ~20 goroutines concurrentes: contención y escalabilidad.
func BenchmarkPool20(b *testing.B) {
	db := openDB(b, 20)
	// warm-up del pool
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			_ = db.QueryRow("SELECT 1 FROM RDB$DATABASE").Scan(&n)
		}()
	}
	wg.Wait()
	b.ReportAllocs()
	b.SetParallelism(3) // ~3×GOMAXPROCS goroutines ≈ 20-24 sobre pool de 20
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var n int
			if err := db.QueryRow("SELECT 1 FROM RDB$DATABASE").Scan(&n); err != nil {
				b.Fatal(err)
			}
		}
	})
}
