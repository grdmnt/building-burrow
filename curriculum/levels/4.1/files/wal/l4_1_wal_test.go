package wal_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/wal"
)

func logPath(t *testing.T) string { return filepath.Join(t.TempDir(), "wal.log") }

func open(t *testing.T, path string, opts ...wal.Option) *wal.Log {
	t.Helper()
	l, err := wal.Open(path, opts...)
	if err != nil {
		t.Fatalf("wal.Open(%s): %v", path, err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func appendAll(t *testing.T, l *wal.Log, recs ...[]byte) {
	t.Helper()
	for i, r := range recs {
		if err := l.Append(r); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}
}

// replay collects copies of every record. The yielded slice is only valid
// until the next step, so it must be copied to be kept.
func replay(t *testing.T, l *wal.Log) [][]byte {
	t.Helper()
	var out [][]byte
	for rec, err := range l.Records() {
		if err != nil {
			t.Fatalf("Records: %v", err)
		}
		out = append(out, append([]byte(nil), rec...))
	}
	return out
}

func sameRecords(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Records gave %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("record %d = %.40q, want %.40q", i, got[i], want[i])
		}
	}
}

func TestL4_1_AppendThenIterateInOrder(t *testing.T) {
	l := open(t, logPath(t))
	want := [][]byte{
		[]byte("first"),
		{},
		[]byte("nul\x00and\nnewline"),
		bytes.Repeat([]byte{0xab}, 1<<20),
	}
	for i := range 100 {
		want = append(want, []byte(fmt.Sprintf("record-%03d", i)))
	}
	appendAll(t, l, want...)
	sameRecords(t, replay(t, l), want)
}

func TestL4_1_EmptyLog(t *testing.T) {
	l := open(t, logPath(t))
	n := 0
	for _, err := range l.Records() {
		if err != nil {
			t.Fatalf("Records on a new log: %v", err)
		}
		n++
	}
	if n != 0 {
		t.Errorf("a new log has no records, but Records yielded %d", n)
	}
}

func TestL4_1_ReopenKeepsRecords(t *testing.T) {
	path := logPath(t)
	l, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	appendAll(t, l, []byte("a"), []byte("b"), []byte("c"))
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	l2 := open(t, path)
	appendAll(t, l2, []byte("d"), []byte("e"))
	sameRecords(t, replay(t, l2), [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")})
}

func TestL4_1_IterationIsRepeatable(t *testing.T) {
	l := open(t, logPath(t))
	appendAll(t, l, []byte("1"), []byte("2"))
	sameRecords(t, replay(t, l), [][]byte{[]byte("1"), []byte("2")})
	appendAll(t, l, []byte("3"))
	sameRecords(t, replay(t, l), [][]byte{[]byte("1"), []byte("2"), []byte("3")})
	appendAll(t, l, []byte("4"))
	if got := replay(t, l); len(got) != 4 {
		t.Errorf("Records must always read from the start of the log and see every append; got %d records", len(got))
	}
}

func TestL4_1_BreakStopsIteration(t *testing.T) {
	l := open(t, logPath(t))
	for i := range 1000 {
		appendAll(t, l, []byte(fmt.Sprintf("r%d", i)))
	}
	seen := 0
	// If Records keeps calling yield after the loop body breaks, Go panics with
	// "range function continued iteration after function for loop body returned false".
	for rec, err := range l.Records() {
		if err != nil {
			t.Fatal(err)
		}
		seen++
		if string(rec) == "r1" {
			break
		}
	}
	if seen != 2 {
		t.Errorf("breaking after the second record should stop the loop, saw %d", seen)
	}
}

func TestL4_1_ScribblingCallerDoesNotHurt(t *testing.T) {
	l := open(t, logPath(t))
	appendAll(t, l, []byte("aaaa"), []byte("bbbb"))
	for rec := range l.Records() {
		for i := range rec {
			rec[i] = 'X'
		}
	}
	sameRecords(t, replay(t, l), [][]byte{[]byte("aaaa"), []byte("bbbb")})
}

func TestL4_1_AppendCopiesNothingItShouldnt(t *testing.T) {
	l := open(t, logPath(t))
	buf := []byte("original")
	appendAll(t, l, buf)
	copy(buf, "MUTATED!")
	sameRecords(t, replay(t, l), [][]byte{[]byte("original")})
}

func TestL4_1_RecordTooLarge(t *testing.T) {
	l := open(t, logPath(t))
	if err := l.Append(make([]byte, wal.MaxRecordSize+1)); !errors.Is(err, wal.ErrRecordTooLarge) {
		t.Errorf("a record over MaxRecordSize should be ErrRecordTooLarge, got %v", err)
	}
	if got := replay(t, l); len(got) != 0 {
		t.Errorf("a rejected record must not be written")
	}
}

func TestL4_1_Closed(t *testing.T) {
	l, err := wal.Open(logPath(t))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err := l.Append([]byte("x")); !errors.Is(err, wal.ErrClosed) {
		t.Errorf("Append after Close should be wal.ErrClosed, got %v", err)
	}
	var errs []error
	for _, err := range l.Records() {
		errs = append(errs, err)
	}
	if len(errs) != 1 || !errors.Is(errs[0], wal.ErrClosed) {
		t.Errorf("Records after Close should yield (nil, wal.ErrClosed) exactly once, got %v", errs)
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing twice should return nil, got %v", err)
	}
}

func TestL4_1_OpenErrors(t *testing.T) {
	if _, err := wal.Open(filepath.Join(t.TempDir(), "no", "such", "dir", "wal.log")); err == nil {
		t.Errorf("opening a log in a directory that doesn't exist should fail")
	}
	dir := t.TempDir()
	if _, err := wal.Open(dir); err == nil {
		t.Errorf("opening a directory as a log file should fail")
	}
}

func TestL4_1_ConcurrentAppends(t *testing.T) {
	l := open(t, logPath(t))
	const workers, each = 20, 50
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				body := bytes.Repeat([]byte{byte(w)}, 100+i)
				sum := sha256.Sum256(body)
				rec := append(sum[:], body...)
				if err := l.Append(rec); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got := replay(t, l)
	if len(got) != workers*each {
		t.Fatalf("%d concurrent appends produced %d records", workers*each, len(got))
	}
	for i, rec := range got {
		if len(rec) < 32 {
			t.Fatalf("record %d is only %d bytes: appends interleaved", i, len(rec))
		}
		if sum := sha256.Sum256(rec[32:]); !bytes.Equal(sum[:], rec[:32]) {
			t.Fatalf("record %d is corrupt: two appends were interleaved in the file. Serialise writes.", i)
		}
	}
}

func TestL4_1_FileIsOnDisk(t *testing.T) {
	path := logPath(t)
	l := open(t, path)
	appendAll(t, l, []byte("hello"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 5 {
		t.Errorf("after Append returns, the record must already be in the file (size is %d)", info.Size())
	}
}

// TestL4_1_KillNine runs an appender in a child process, kills it with SIGKILL
// mid-stream, and checks every record the child saw acknowledged is in the log.
func TestL4_1_KillNine(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process")
	}
	path := logPath(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestL4_1_zHelperAppender$")
	cmd.Env = append(os.Environ(), "BURROW_HELPER_WAL="+path)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	last := -1
	sc := bufio.NewScanner(out)
	deadline := time.Now().Add(20 * time.Second)
	for last < 200 && sc.Scan() {
		if n, ok := strings.CutPrefix(sc.Text(), "acked "); ok {
			last, _ = strconv.Atoi(n)
		}
		if time.Now().After(deadline) {
			break
		}
	}
	cmd.Process.Kill()
	cmd.Wait()
	if last < 0 {
		t.Fatal("the helper acknowledged nothing")
	}
	l := open(t, path)
	got := replay(t, l)
	if len(got) < last+1 {
		t.Fatalf("%d records were acknowledged before the crash but only %d are in the log. Append must not return before fsync.", last+1, len(got))
	}
	for i := 0; i <= last; i++ {
		if string(got[i]) != fmt.Sprintf("record-%05d", i) {
			t.Fatalf("record %d = %q", i, got[i])
		}
	}
}

func TestL4_1_zHelperAppender(t *testing.T) {
	path := os.Getenv("BURROW_HELPER_WAL")
	if path == "" {
		t.Skip("helper process for TestL4_1_KillNine")
	}
	l, err := wal.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper open:", err)
		os.Exit(3)
	}
	for i := 0; ; i++ {
		if err := l.Append([]byte(fmt.Sprintf("record-%05d", i))); err != nil {
			fmt.Fprintln(os.Stderr, "helper append:", err)
			os.Exit(3)
		}
		fmt.Printf("acked %d\n", i)
	}
}
