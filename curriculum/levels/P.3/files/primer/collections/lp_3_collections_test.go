package collections_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"burrow/primer/collections"
)

func TestLP_3_Move(t *testing.T) {
	p := collections.Point{X: 1, Y: 2}
	collections.Move(&p, 3, -4)
	if p != (collections.Point{X: 4, Y: -2}) {
		t.Fatalf("after Move(&p, 3, -4) from {1 2}, p = %v, want {4 -2}. "+
			"Move gets a pointer, so p.X += dx changes the caller's Point",
			p)
	}
	q := &p
	collections.Move(q, 0.5, 0.5)
	collections.Move(q, 0.5, 0.5)
	if p != (collections.Point{X: 5, Y: -1}) {
		t.Errorf("two more moves by {0.5 0.5} through the pointer q should "+
			"leave p at {5 -1}, got %v", p)
	}
}

func TestLP_3_Dist(t *testing.T) {
	cases := []struct {
		a, b collections.Point
		want float64
	}{
		{collections.Point{}, collections.Point{X: 3, Y: 4}, 5},
		{collections.Point{X: -1, Y: -1}, collections.Point{X: 2, Y: 3}, 5},
		{collections.Point{X: 1, Y: 1}, collections.Point{X: 2, Y: 2},
			math.Sqrt2},
		{collections.Point{X: 7, Y: 7}, collections.Point{X: 7, Y: 7}, 0},
		{collections.Point{X: 0, Y: 10}, collections.Point{}, 10},
	}
	for _, tc := range cases {
		if got := collections.Dist(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("Dist(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLP_3_UsesFlowKit(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") ||
			strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), n, nil,
			parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", n, err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "burrow/primer/flow" {
				return
			}
		}
	}
	t.Error("Dist should take its square root with flow.Sqrt from P.2: " +
		"add \"burrow/primer/flow\" to the imports")
}

func TestLP_3_Reverse(t *testing.T) {
	cases := []struct{ in, want []int }{
		{nil, nil},
		{[]int{}, []int{}},
		{[]int{1}, []int{1}},
		{[]int{1, 2}, []int{2, 1}},
		{[]int{1, 2, 3}, []int{3, 2, 1}},
		{[]int{5, 4, 3, 2, 1, 0}, []int{0, 1, 2, 3, 4, 5}},
	}
	for _, tc := range cases {
		xs := slices.Clone(tc.in)
		collections.Reverse(xs)
		if !slices.Equal(xs, tc.want) {
			t.Errorf("after Reverse(%v) the slice is %v, want %v",
				tc.in, xs, tc.want)
		}
	}
	xs := []int{1, 2, 3, 4, 5}
	collections.Reverse(xs[1:4])
	if !slices.Equal(xs, []int{1, 4, 3, 2, 5}) {
		t.Errorf("Reverse(xs[1:4]) on [1 2 3 4 5] should leave xs as "+
			"[1 4 3 2 5], got %v. Swap elements in place: a slice is a "+
			"window onto an array, so the caller sees the swaps", xs)
	}
}

func TestLP_3_Chunk(t *testing.T) {
	cases := []struct {
		in   []int
		size int
		want [][]int
	}{
		{[]int{1, 2, 3, 4, 5, 6, 7}, 3, [][]int{{1, 2, 3}, {4, 5, 6}, {7}}},
		{[]int{1, 2, 3, 4}, 2, [][]int{{1, 2}, {3, 4}}},
		{[]int{1, 2, 3}, 1, [][]int{{1}, {2}, {3}}},
		{[]int{1, 2, 3}, 5, [][]int{{1, 2, 3}}},
		{[]int{1, 2, 3}, 3, [][]int{{1, 2, 3}}},
		{nil, 3, nil},
		{[]int{}, 3, nil},
		{[]int{1, 2, 3}, 0, nil},
		{[]int{1, 2, 3}, -1, nil},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%v by %d", tc.in, tc.size), func(t *testing.T) {
			got := collections.Chunk(tc.in, tc.size)
			if !slices.EqualFunc(got, tc.want, slices.Equal) {
				t.Errorf("Chunk(%v, %d) = %v, want %v", tc.in, tc.size,
					got, tc.want)
			}
		})
	}
}

func TestLP_3_WordCount(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]int
	}{
		{"", map[string]int{}},
		{"   \t\n", map[string]int{}},
		{"burrow", map[string]int{"burrow": 1}},
		{"Hello hello HELLO", map[string]int{"hello": 3}},
		{"the cat\tand\nthe  hat", map[string]int{
			"the": 2, "cat": 1, "and": 1, "hat": 1}},
		{"Go, go go!", map[string]int{"go,": 1, "go": 1, "go!": 1}},
	}
	for _, tc := range cases {
		got := collections.WordCount(tc.in)
		if !maps.Equal(got, tc.want) {
			hint := ""
			if strings.ToLower(tc.in) != tc.in {
				hint = ". WordCount should lowercase words"
			}
			t.Errorf("WordCount(%q) = %v, want %v%s", tc.in, got,
				tc.want, hint)
		}
	}
	m := collections.WordCount("")
	if m == nil {
		t.Fatal("WordCount(\"\") returned a nil map. Return an empty map " +
			"from make instead: writing to a nil map panics, and callers " +
			"may want to add to the result")
	}
	m["extra"]++
}

func TestLP_3_Fibonacci(t *testing.T) {
	want := []int{0, 1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89}
	f := collections.Fibonacci()
	for i, w := range want {
		if got := f(); got != w {
			t.Fatalf("call %d of the function from Fibonacci() returned "+
				"%d, want %d (the sequence is %v)", i+1, got, w, want)
		}
	}
	g := collections.Fibonacci()
	h := collections.Fibonacci()
	g()
	g()
	g()
	if got := h(); got != 0 {
		t.Errorf("each call to Fibonacci() should start a new sequence, "+
			"but a fresh one began at %d after another was used. Keep the "+
			"state in variables inside Fibonacci, not at package level", got)
	}
}
