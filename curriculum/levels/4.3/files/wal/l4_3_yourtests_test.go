package wal_test

import (
	"testing"

	"burrow/internal/testkit"
)

// mutantScan is shared by the mutants below. It reads the log the way 4.3
// specifies (wire records whose payload is crc32 | rec) and reports where
// each good record starts and where the first bad one starts (-1 if none).
const mutantScan = `
func mutantScan(path string) (good []int64, bad int64, tailOnly bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, -1, false
	}
	r := bytes.NewReader(b)
	for {
		start := int64(len(b) - r.Len())
		p, err := wire.ReadRecord(r)
		if err == io.EOF {
			return good, -1, false
		}
		if err != nil || len(p) < 4 || binary.BigEndian.Uint32(p) != crc32.ChecksumIEEE(p[4:]) {
			return good, start, err != nil || r.Len() == 0
		}
		good = append(good, start)
	}
}
`

const mutantImports = `import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"burrow/wire"
)

var _ = errors.Is
`

// Your own tests in wal/ are graded here. Each mutant is your log with one bug
// planted in it; at least one of your tests must fail for each.
func TestL4_3_YourTestsCatchBugs(t *testing.T) {
	testkit.CheckYourTests(t, testkit.MutationCheck{
		Dir:            "wal",
		RequireInTests: "TempDir()",
		Mutants: []testkit.Mutant{
			{
				Bug:    "Open silently truncates a log with a corrupt record in the middle instead of returning ErrCorrupt",
				Target: "Open",
				Source: mutantImports + `
func Open(path string, opts ...Option) (*Log, error) {
	l, err := OrigOpen(path, opts...)
	if errors.Is(err, ErrCorrupt) {
		if _, bad, _ := mutantScan(path); bad >= 0 && os.Truncate(path, bad) == nil {
			return OrigOpen(path, opts...)
		}
	}
	return l, err
}
` + mutantScan,
			},
			{
				Bug:    "repairing a torn tail also drops the last good record",
				Target: "Open",
				Source: mutantImports + `
func Open(path string, opts ...Option) (*Log, error) {
	if good, bad, tailOnly := mutantScan(path); bad >= 0 && tailOnly && len(good) > 0 {
		os.Truncate(path, good[len(good)-1])
	}
	return OrigOpen(path, opts...)
}
` + mutantScan,
			},
			{
				Bug:    "Truncate(n) keeps n+1 records",
				Target: "Log.Truncate",
				Source: `
func (l *Log) Truncate(n int) error {
	if n >= 0 && n < l.Len() {
		return l.OrigTruncate(n + 1)
	}
	return l.OrigTruncate(n)
}
`,
			},
		},
	})
}
