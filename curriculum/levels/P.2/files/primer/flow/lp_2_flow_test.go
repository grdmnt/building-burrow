package flow_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"burrow/primer/flow"
)

func TestLP_2_Sqrt(t *testing.T) {
	cases := []struct {
		x     float64
		steps int
	}{
		{1, 1}, {2, 5}, {4, 6}, {9, 7}, {2.25, 5}, {0.25, 6},
		{100, 9}, {12345, 12}, {1e6, 15}, {1e-6, 15}, {1e10, 22},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.x), func(t *testing.T) {
			z, steps := flow.Sqrt(tc.x)
			want := math.Sqrt(tc.x)
			if math.Abs(z-want) > 1e-9*math.Max(1, want) {
				t.Errorf("Sqrt(%v) = %v, want %v (math.Sqrt's answer)",
					tc.x, z, want)
			}
			if steps != tc.steps {
				t.Errorf("Sqrt(%v) reported %d steps, want %d. Start "+
					"from z = 1.0, count every new guess you compute, "+
					"and stop as soon as a guess moves by less than "+
					"1e-10", tc.x, steps, tc.steps)
			}
		})
	}
}

func TestLP_2_SqrtEdges(t *testing.T) {
	for _, x := range []float64{-1, -0.5, -1e6} {
		z, steps := flow.Sqrt(x)
		if !math.IsNaN(z) || steps != 0 {
			t.Errorf("Sqrt(%v) = (%v, %d), want (NaN, 0): a negative "+
				"number has no real square root, so check before looping",
				x, z, steps)
		}
	}
	if z, steps := flow.Sqrt(0); z != 0 || steps != 0 {
		t.Errorf("Sqrt(0) = (%v, %d), want (0, 0) without looping",
			z, steps)
	}
}

func TestLP_2_FizzBuzz(t *testing.T) {
	want := []string{"1", "2", "Fizz", "4", "Buzz", "Fizz", "7", "8",
		"Fizz", "Buzz", "11", "Fizz", "13", "14", "FizzBuzz"}
	for i, w := range want {
		if got := flow.FizzBuzz(i + 1); got != w {
			t.Errorf("FizzBuzz(%d) = %q, want %q", i+1, got, w)
		}
	}
	more := map[int]string{30: "FizzBuzz", 45: "FizzBuzz", 98: "98",
		99: "Fizz", 100: "Buzz", 101: "101"}
	for n, w := range more {
		if got := flow.FizzBuzz(n); got != w {
			hint := ""
			if w == "FizzBuzz" {
				hint = ". A switch runs the first case that matches, " +
					"so the both-at-once case has to come first"
			}
			t.Errorf("FizzBuzz(%d) = %q, want %q%s", n, got, w, hint)
		}
	}
}

func TestLP_2_Countdown(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{3, "3 2 1 liftoff"},
		{1, "1 liftoff"},
		{0, "liftoff"},
		{10, "10 9 8 7 6 5 4 3 2 1 liftoff"},
	}
	for _, tc := range cases {
		var b strings.Builder
		flow.Countdown(&b, tc.n)
		if got := b.String(); got != tc.want {
			t.Errorf("Countdown(&b, %d) wrote %q, want %q. Deferred "+
				"calls run last-in, first-out when the function returns",
				tc.n, got, tc.want)
		}
	}
	var b strings.Builder
	b.WriteString("T-minus ")
	flow.Countdown(&b, 2)
	if got := b.String(); got != "T-minus 2 1 liftoff" {
		t.Errorf("Countdown should add to what's already in the builder: "+
			"got %q, want %q", got, "T-minus 2 1 liftoff")
	}
}

// TestLP_2_CountdownUsesDefer reads your source, because any loop that
// counts down would print the same thing. The point is to see defer
// reverse the order for you.
func TestLP_2_CountdownUsesDefer(t *testing.T) {
	fn := findFunc(t, "Countdown")
	if fn == nil {
		t.Fatal("no func Countdown found in primer/flow")
	}
	inLoop := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch l := n.(type) {
		case *ast.ForStmt:
			body = l.Body
		case *ast.RangeStmt:
			body = l.Body
		default:
			return true
		}
		ast.Inspect(body, func(m ast.Node) bool {
			if _, ok := m.(*ast.DeferStmt); ok {
				inLoop = true
			}
			return true
		})
		return true
	})
	if !inLoop {
		t.Error("Countdown should count up from 1 in a for loop and " +
			"defer each write, letting defer reverse the order. No " +
			"defer statement inside a loop was found")
	}
}

func TestLP_2_Receipt(t *testing.T) {
	cases := []struct {
		cents, people int
		want          string
	}{
		{1000, 3, "person 1: 334\nperson 2: 333\nperson 3: 333\n"},
		{1000, 4, "person 1: 250\nperson 2: 250\nperson 3: 250\n" +
			"person 4: 250\n"},
		{5, 3, "person 1: 2\nperson 2: 2\nperson 3: 1\n"},
		{0, 2, "person 1: 0\nperson 2: 0\n"},
		{7, 1, "person 1: 7\n"},
		{100, 0, "nobody to split with\n"},
		{100, -2, "nobody to split with\n"},
	}
	for _, tc := range cases {
		if got := flow.Receipt(tc.cents, tc.people); got != tc.want {
			t.Errorf("Receipt(%d, %d) =\n%s\nwant\n%s",
				tc.cents, tc.people, got, tc.want)
		}
	}
}

// TestLP_2_ReceiptAddsUp checks many splits: every cent is paid, and
// nobody pays more than one cent more than anyone else.
func TestLP_2_ReceiptAddsUp(t *testing.T) {
	for cents := 0; cents <= 60; cents++ {
		for people := 1; people <= 7; people++ {
			lines := strings.Split(
				strings.TrimSuffix(flow.Receipt(cents, people), "\n"), "\n")
			if len(lines) != people {
				t.Fatalf("Receipt(%d, %d) should have %d lines, got %d",
					cents, people, people, len(lines))
			}
			sum, lo, hi := 0, math.MaxInt, math.MinInt
			for i, line := range lines {
				prefix := "person " + strconv.Itoa(i+1) + ": "
				n, err := strconv.Atoi(strings.TrimPrefix(line, prefix))
				if !strings.HasPrefix(line, prefix) || err != nil {
					t.Fatalf("Receipt(%d, %d) line %d is %q, want %q "+
						"and a number", cents, people, i+1, line, prefix)
				}
				sum += n
				lo, hi = min(lo, n), max(hi, n)
			}
			if sum != cents || hi-lo > 1 {
				t.Fatalf("Receipt(%d, %d) shares add up to %d (want %d), "+
					"smallest %d, largest %d", cents, people, sum, cents,
					lo, hi)
			}
		}
	}
}

func TestLP_2_UsesBasicsKit(t *testing.T) {
	if !imports(t, "burrow/primer/basics") {
		t.Error("Receipt should reuse basics.SplitBill from P.1 instead of " +
			"dividing again: add \"burrow/primer/basics\" to the imports")
	}
}

// pkgFiles parses the non-test .go files of the package under test.
func pkgFiles(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") ||
			strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), n, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", n, err)
		}
		files = append(files, f)
	}
	return files
}

func findFunc(t *testing.T, name string) *ast.FuncDecl {
	t.Helper()
	for _, f := range pkgFiles(t) {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil &&
				fd.Name.Name == name {
				return fd
			}
		}
	}
	return nil
}

func imports(t *testing.T, path string) bool {
	t.Helper()
	for _, f := range pkgFiles(t) {
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == path {
				return true
			}
		}
	}
	return false
}
