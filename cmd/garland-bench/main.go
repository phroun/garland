// garland-bench is a benchmark and stress test for the Garland library.
// It creates a 1GB file and measures performance of common operations.
package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/phroun/garland"
)

const (
	fileSize       = 1 << 30 // 1 GB
	chunkSize      = 64 * 1024 * 1024
	smallEditSize  = 100
	mediumEditSize = 10 * 1024
	largeEditSize  = 1024 * 1024
)

type BenchResult struct {
	Name     string
	Duration time.Duration
	Ops      int
	Extra    string
}

func (r BenchResult) String() string {
	if r.Ops > 0 {
		opsPerSec := float64(r.Ops) / r.Duration.Seconds()
		if r.Extra != "" {
			return fmt.Sprintf("%-40s %12v  (%d ops, %.2f ops/sec) %s", r.Name, r.Duration.Round(time.Millisecond), r.Ops, opsPerSec, r.Extra)
		}
		return fmt.Sprintf("%-40s %12v  (%d ops, %.2f ops/sec)", r.Name, r.Duration.Round(time.Millisecond), r.Ops, opsPerSec)
	}
	if r.Extra != "" {
		return fmt.Sprintf("%-40s %12v  %s", r.Name, r.Duration.Round(time.Millisecond), r.Extra)
	}
	return fmt.Sprintf("%-40s %12v", r.Name, r.Duration.Round(time.Millisecond))
}

func main() {
	fmt.Println("Garland Benchmark and Stress Test")
	fmt.Println("==================================")
	fmt.Printf("File size: %d MB\n", fileSize/(1024*1024))
	fmt.Printf("Go version: %s\n", runtime.Version())
	fmt.Printf("GOMAXPROCS: %d\n", runtime.GOMAXPROCS(0))
	fmt.Println()

	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "garland-bench-*")
	if err != nil {
		fmt.Printf("Failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test-1gb.txt")
	coldStorage := filepath.Join(tmpDir, "cold")

	var results []BenchResult

	// Generate test file
	fmt.Println("Generating 1GB test file...")
	result := generateTestFile(testFile)
	results = append(results, result)
	fmt.Println(result)
	fmt.Println()

	// Initialize library with generous memory for benchmark
	// (we want to test operations, not memory pressure)
	lib, err := garland.Init(garland.LibraryOptions{
		ColdStoragePath: coldStorage,
		MemorySoftLimit: 2 * 1024 * 1024 * 1024,  // 2 GB
		MemoryHardLimit: 4 * 1024 * 1024 * 1024,  // 4 GB
	})
	if err != nil {
		fmt.Printf("Failed to init library: %v\n", err)
		os.Exit(1)
	}

	// Helper to run and print each benchmark
	runBench := func(name string, fn func() BenchResult) {
		fmt.Printf("  %-40s ", name+"...")
		result := fn()
		fmt.Printf("%v\n", result.Duration.Round(time.Millisecond))
		results = append(results, result)
	}

	// Run benchmarks
	fmt.Println("Running benchmarks...")
	fmt.Println()

	// Open file benchmark (skip memory-only for large files - would thrash)
	fmt.Println("File opening:")
	runBench("Open file (all storage tiers)", func() BenchResult {
		return benchOpenFile(lib, testFile, garland.AllStorage, "Open file (all storage tiers)")
	})

	// Open file for remaining operations
	fmt.Println("\nOpening file for operation benchmarks...")
	g, err := lib.Open(garland.FileOptions{
		FilePath:     testFile,
		LoadingStyle: garland.AllStorage,
	})
	if err != nil {
		fmt.Printf("Failed to open file: %v\n", err)
		os.Exit(1)
	}

	// Wait for file to be ready
	for !g.ByteCount().Complete {
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("File ready: %d bytes, %d lines\n\n", g.ByteCount().Value, g.LineCount().Value)

	// Cursor operations
	fmt.Println("Cursor operations:")
	runBench("Seek operations (byte)", func() BenchResult { return benchSeekOperations(g) })
	runBench("Read operations (64KB chunks)", func() BenchResult { return benchReadOperations(g) })

	// Edit operations
	fmt.Println("\nEdit operations:")
	runBench("Small inserts (100 bytes x 1000)", func() BenchResult { return benchSmallInserts(g) })
	runBench("Small deletes (100 bytes x 1000)", func() BenchResult { return benchSmallDeletes(g) })
	runBench("Medium inserts (10KB x 100)", func() BenchResult { return benchMediumInserts(g) })
	runBench("Large inserts (1MB x 10)", func() BenchResult { return benchLargeInserts(g) })

	// Transaction operations
	fmt.Println("\nTransaction operations:")
	runBench("Transaction cycles", func() BenchResult { return benchTransactions(g) })

	// Search operations
	fmt.Println("\nSearch operations:")
	runBench("Search (find first)", func() BenchResult { return benchSearch(g) })
	runBench("Search all occurrences", func() BenchResult { return benchSearchAll(g) })

	// Undo operations
	fmt.Println("\nUndo/redo operations:")
	runBench("Undo/redo cycles", func() BenchResult { return benchUndoRedo(g) })

	// Decoration operations
	fmt.Println("\nDecoration operations:")
	runBench("Decoration add/query/remove", func() BenchResult { return benchDecorations(g) })

	// Memory management - use a separate library with lower limits
	fmt.Println("\nMemory management:")
	g.Close()

	// Re-init with lower memory to test chilling
	lib2, _ := garland.Init(garland.LibraryOptions{
		ColdStoragePath: coldStorage,
		MemorySoftLimit: 256 * 1024 * 1024, // 256 MB
		MemoryHardLimit: 512 * 1024 * 1024, // 512 MB
	})
	g2, _ := lib2.Open(garland.FileOptions{
		FilePath:     testFile,
		LoadingStyle: garland.AllStorage,
	})
	if g2 != nil {
		for !g2.ByteCount().Complete {
			time.Sleep(100 * time.Millisecond)
		}
		runBench("Chill unused data", func() BenchResult { return benchChill(g2) })
		g2.Close()
	}

	// Print summary
	fmt.Println("\n" + "=")
	fmt.Println("SUMMARY")
	fmt.Println("=")
	for _, r := range results {
		fmt.Println(r)
	}

	// Memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Println()
	fmt.Printf("Peak heap allocation: %d MB\n", m.HeapSys/(1024*1024))
	fmt.Printf("Total allocations: %d MB\n", m.TotalAlloc/(1024*1024))
}

func generateTestFile(path string) BenchResult {
	start := time.Now()

	f, err := os.Create(path)
	if err != nil {
		return BenchResult{Name: "Generate test file", Duration: 0, Extra: fmt.Sprintf("ERROR: %v", err)}
	}
	defer f.Close()

	// Generate realistic text content with lines
	lineNum := 1
	written := int64(0)
	buf := make([]byte, chunkSize)

	for written < fileSize {
		// Fill buffer with text lines
		pos := 0
		for pos < len(buf)-200 && written+int64(pos) < fileSize {
			// Create a line with line number and some content
			line := fmt.Sprintf("%08d: ", lineNum)
			copy(buf[pos:], line)
			pos += len(line)

			// Add random printable content
			contentLen := 60 + int(buf[pos%len(buf)])%40 // 60-100 chars
			if pos+contentLen+1 > len(buf) {
				contentLen = len(buf) - pos - 1
			}
			for i := 0; i < contentLen; i++ {
				buf[pos+i] = 'a' + byte((lineNum+i)%26)
			}
			pos += contentLen
			buf[pos] = '\n'
			pos++
			lineNum++
		}

		toWrite := pos
		if written+int64(toWrite) > fileSize {
			toWrite = int(fileSize - written)
		}

		n, err := f.Write(buf[:toWrite])
		if err != nil {
			return BenchResult{Name: "Generate test file", Duration: time.Since(start), Extra: fmt.Sprintf("ERROR: %v", err)}
		}
		written += int64(n)
	}

	return BenchResult{
		Name:     "Generate test file",
		Duration: time.Since(start),
		Extra:    fmt.Sprintf("%d lines", lineNum-1),
	}
}

func benchOpenFile(lib *garland.Library, path string, style garland.LoadingStyle, name string) BenchResult {
	start := time.Now()

	g, err := lib.Open(garland.FileOptions{
		FilePath:     path,
		LoadingStyle: style,
	})
	if err != nil {
		return BenchResult{Name: name, Duration: 0, Extra: fmt.Sprintf("ERROR: %v", err)}
	}

	// Wait for ready
	byteCount := g.ByteCount()
	for !byteCount.Complete {
		time.Sleep(10 * time.Millisecond)
		byteCount = g.ByteCount()
	}

	duration := time.Since(start)
	g.Close()

	return BenchResult{
		Name:     name,
		Duration: duration,
		Extra:    fmt.Sprintf("%d bytes", byteCount.Value),
	}
}

func benchSeekOperations(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	byteCount := g.ByteCount().Value
	ops := 0
	start := time.Now()

	// Random seeks across the file
	positions := []int64{0, byteCount / 4, byteCount / 2, byteCount * 3 / 4, byteCount - 1}
	for i := 0; i < 1000; i++ {
		for _, pos := range positions {
			cursor.SeekByte(pos)
			ops++
		}
	}

	return BenchResult{
		Name:     "Seek operations (byte)",
		Duration: time.Since(start),
		Ops:      ops,
	}
}

func benchReadOperations(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	byteCount := g.ByteCount().Value
	ops := 0
	bytesRead := int64(0)
	start := time.Now()

	// Read chunks from various positions
	positions := []int64{0, byteCount / 4, byteCount / 2, byteCount * 3 / 4}
	for i := 0; i < 100; i++ {
		for _, pos := range positions {
			cursor.SeekByte(pos)
			data, err := cursor.ReadBytes(64 * 1024) // 64KB reads
			if err == nil {
				bytesRead += int64(len(data))
				ops++
			}
		}
	}

	return BenchResult{
		Name:     "Read operations (64KB chunks)",
		Duration: time.Since(start),
		Ops:      ops,
		Extra:    fmt.Sprintf("%d MB read", bytesRead/(1024*1024)),
	}
}

func benchSmallInserts(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	ops := 0
	smallText := make([]byte, smallEditSize)
	for i := range smallText {
		smallText[i] = 'x'
	}

	start := time.Now()

	// Insert small chunks at various positions
	g.TransactionStart("small inserts")
	for i := 0; i < 1000; i++ {
		pos := int64(i * 1000)
		cursor.SeekByte(pos)
		cursor.InsertBytes(smallText, nil, true)
		ops++
	}
	g.TransactionCommit()

	duration := time.Since(start)

	// Rollback to clean up
	g.UndoSeek(g.CurrentRevision() - 1)

	return BenchResult{
		Name:     "Small inserts (100 bytes x 1000)",
		Duration: duration,
		Ops:      ops,
	}
}

func benchSmallDeletes(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	ops := 0
	start := time.Now()

	g.TransactionStart("small deletes")
	for i := 0; i < 1000; i++ {
		pos := int64(i * 1000)
		cursor.SeekByte(pos)
		cursor.DeleteBytes(smallEditSize, false)
		ops++
	}
	g.TransactionCommit()

	duration := time.Since(start)

	// Rollback to clean up
	g.UndoSeek(g.CurrentRevision() - 1)

	return BenchResult{
		Name:     "Small deletes (100 bytes x 1000)",
		Duration: duration,
		Ops:      ops,
	}
}

func benchMediumInserts(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	ops := 0
	mediumText := make([]byte, mediumEditSize)
	for i := range mediumText {
		mediumText[i] = 'y'
	}

	start := time.Now()

	g.TransactionStart("medium inserts")
	for i := 0; i < 100; i++ {
		pos := int64(i * 10000)
		cursor.SeekByte(pos)
		cursor.InsertBytes(mediumText, nil, true)
		ops++
	}
	g.TransactionCommit()

	duration := time.Since(start)

	// Rollback to clean up
	g.UndoSeek(g.CurrentRevision() - 1)

	return BenchResult{
		Name:     "Medium inserts (10KB x 100)",
		Duration: duration,
		Ops:      ops,
	}
}

func benchLargeInserts(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	ops := 0
	largeText := make([]byte, largeEditSize)
	for i := range largeText {
		largeText[i] = 'z'
	}

	start := time.Now()

	g.TransactionStart("large inserts")
	for i := 0; i < 10; i++ {
		pos := int64(i * 100000)
		cursor.SeekByte(pos)
		cursor.InsertBytes(largeText, nil, true)
		ops++
	}
	g.TransactionCommit()

	duration := time.Since(start)

	// Rollback to clean up
	g.UndoSeek(g.CurrentRevision() - 1)

	return BenchResult{
		Name:     "Large inserts (1MB x 10)",
		Duration: duration,
		Ops:      ops,
	}
}

func benchTransactions(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	ops := 0
	text := []byte("transaction test data")
	startRev := g.CurrentRevision()

	start := time.Now()

	for i := 0; i < 100; i++ {
		g.TransactionStart(fmt.Sprintf("tx-%d", i))
		cursor.SeekByte(0)
		cursor.InsertBytes(text, nil, true)
		g.TransactionCommit()
		ops++
	}

	duration := time.Since(start)

	// Rollback all
	g.UndoSeek(startRev)

	return BenchResult{
		Name:     "Transaction cycles (start/edit/commit)",
		Duration: duration,
		Ops:      ops,
	}
}

func benchSearch(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	ops := 0
	start := time.Now()

	// Search for line number patterns
	patterns := []string{"00001000:", "00010000:", "00100000:", "01000000:"}
	for i := 0; i < 25; i++ {
		for _, pattern := range patterns {
			cursor.SeekByte(0)
			_, err := cursor.FindString(pattern, garland.SearchOptions{CaseSensitive: true})
			if err == nil {
				ops++
			}
		}
	}

	return BenchResult{
		Name:     "Search (find first)",
		Duration: time.Since(start),
		Ops:      ops,
	}
}

func benchSearchAll(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	ops := 0
	totalMatches := 0
	start := time.Now()

	// Search for common patterns
	cursor.SeekByte(0)
	results, err := cursor.FindStringAll("abcdefghij", garland.SearchOptions{CaseSensitive: true})
	if err == nil {
		totalMatches += len(results)
		ops++
	}

	return BenchResult{
		Name:     "Search all occurrences",
		Duration: time.Since(start),
		Ops:      ops,
		Extra:    fmt.Sprintf("%d matches", totalMatches),
	}
}

func benchUndoRedo(g *garland.Garland) BenchResult {
	cursor := g.NewCursor()
	defer g.RemoveCursor(cursor)

	// Create some revisions first
	startRev := g.CurrentRevision()
	text := []byte("undo test")

	for i := 0; i < 50; i++ {
		g.TransactionStart("")
		cursor.SeekByte(0)
		cursor.InsertBytes(text, nil, true)
		g.TransactionCommit()
	}

	endRev := g.CurrentRevision()
	ops := 0

	start := time.Now()

	// Undo/redo cycles
	for i := 0; i < 10; i++ {
		// Undo all
		for rev := endRev; rev > startRev; rev-- {
			g.UndoSeek(rev - 1)
			ops++
		}
		// Redo all
		for rev := startRev; rev < endRev; rev++ {
			g.UndoSeek(rev + 1)
			ops++
		}
	}

	duration := time.Since(start)

	// Clean up
	g.UndoSeek(startRev)

	return BenchResult{
		Name:     "Undo/redo operations",
		Duration: duration,
		Ops:      ops,
	}
}

func benchDecorations(g *garland.Garland) BenchResult {
	ops := 0
	byteCount := g.ByteCount().Value

	start := time.Now()

	// Add decorations
	for i := 0; i < 1000; i++ {
		pos := int64(i) * (byteCount / 1000)
		addr := garland.AbsoluteAddress{
			Mode: garland.ByteMode,
			Byte: pos,
		}
		g.Decorate([]garland.DecorationEntry{{
			Key:     fmt.Sprintf("mark-%d", i),
			Address: &addr,
		}})
		ops++
	}

	// Query decorations
	for i := 0; i < 1000; i++ {
		_, err := g.GetDecorationPosition(fmt.Sprintf("mark-%d", i))
		if err == nil {
			ops++
		}
	}

	// Query range
	decorations, _ := g.GetDecorationsInByteRange(0, byteCount/2)
	ops += len(decorations)

	// Remove decorations
	for i := 0; i < 1000; i++ {
		g.Decorate([]garland.DecorationEntry{{
			Key:     fmt.Sprintf("mark-%d", i),
			Address: nil, // nil to delete
		}})
		ops++
	}

	return BenchResult{
		Name:     "Decoration operations",
		Duration: time.Since(start),
		Ops:      ops,
	}
}

func benchChill(g *garland.Garland) BenchResult {
	start := time.Now()

	// Get initial memory stats
	initialStats := g.MemoryUsage()

	// Chill inactive data
	g.Chill(garland.ChillUnusedData)

	finalStats := g.MemoryUsage()

	return BenchResult{
		Name:     "Chill unused data",
		Duration: time.Since(start),
		Extra:    fmt.Sprintf("before: %d MB, after: %d MB", initialStats.MemoryBytes/(1024*1024), finalStats.MemoryBytes/(1024*1024)),
	}
}

// randomBytes generates n random bytes
func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
