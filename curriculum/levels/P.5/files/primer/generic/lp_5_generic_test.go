package generic_test

import (
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"burrow/primer/generic"
	"burrow/primer/shapes"
)

// These only compile if the functions are generic with the signatures
// in the Reference.
var (
	_ func([]string, string) int                = generic.Index[string]
	_ func([]int, func(int) string) []string    = generic.Map[int, string]
	_ func([]float64) (float64, bool)           = generic.Max[float64]
	_ func([]shapes.Rect) (shapes.Rect, bool)   = generic.Largest[shapes.Rect]
	_ func() *generic.Set[string]               = generic.NewSet[string]
	_ func([]shapes.Shape) (shapes.Shape, bool) = generic.Largest[shapes.Shape]
)

func TestLP_5_Index(t *testing.T) {
	t.Run("ints", func(t *testing.T) {
		s := []int{10, 20, 15, -10}
		for want, x := range s {
			if got := generic.Index(s, x); got != want {
				t.Errorf("Index(%v, %d) = %d, want %d", s, x, got, want)
			}
		}
		if got := generic.Index(s, 99); got != -1 {
			t.Errorf("Index(%v, 99) = %d, want -1 for a missing value",
				s, got)
		}
	})
	t.Run("strings", func(t *testing.T) {
		s := []string{"foo", "bar", "baz", "bar"}
		if got := generic.Index(s, "bar"); got != 1 {
			t.Errorf("Index(%q, \"bar\") = %d, want 1 (the first match)",
				s, got)
		}
		if got := generic.Index(s, "hello"); got != -1 {
			t.Errorf("Index(%q, \"hello\") = %d, want -1", s, got)
		}
	})
	t.Run("structs", func(t *testing.T) {
		s := []shapes.Rect{{W: 1, H: 2}, {W: 3, H: 4}}
		if got := generic.Index(s, shapes.Rect{W: 3, H: 4}); got != 1 {
			t.Errorf("Index(rects, Rect{3, 4}) = %d, want 1. Structs "+
				"whose fields are comparable are comparable too", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := generic.Index(nil, 0); got != -1 {
			t.Errorf("Index(nil, 0) = %d, want -1", got)
		}
	})
}

func TestLP_5_Map(t *testing.T) {
	in := []int{3, 1, 2}
	got := generic.Map(in, strconv.Itoa)
	if !slices.Equal(got, []string{"3", "1", "2"}) {
		t.Errorf("Map([3 1 2], strconv.Itoa) = %q, want [\"3\" \"1\" "+
			"\"2\"]. If it's twice as long, you made a slice of length "+
			"n and then appended n more", got)
	}
	if !slices.Equal(in, []int{3, 1, 2}) {
		t.Errorf("Map changed its input to %v; build a new slice", in)
	}

	rects := []shapes.Rect{{W: 1, H: 2}, {W: 3, H: 4}}
	areas := generic.Map(rects, shapes.Rect.Area)
	if !slices.Equal(areas, []float64{2, 12}) {
		t.Errorf("Map(rects, shapes.Rect.Area) = %v, want [2 12]", areas)
	}

	lens := generic.Map([]string{"go", "", "burrow"},
		func(s string) int { return len(s) })
	if !slices.Equal(lens, []int{2, 0, 6}) {
		t.Errorf("Map(strings, len) = %v, want [2 0 6]", lens)
	}

	if got := generic.Map([]int(nil), strconv.Itoa); len(got) != 0 {
		t.Errorf("Map(nil, f) = %v, want an empty result", got)
	}
}

type celsius float64

func TestLP_5_Max(t *testing.T) {
	t.Run("ints", func(t *testing.T) {
		if got, ok := generic.Max([]int{3, 9, 2}); got != 9 || !ok {
			t.Errorf("Max([3 9 2]) = %d, %v; want 9, true", got, ok)
		}
	})
	t.Run("negative ints", func(t *testing.T) {
		got, ok := generic.Max([]int{-5, -2, -9})
		if got != -2 || !ok {
			t.Errorf("Max([-5 -2 -9]) = %d, %v; want -2, true. Start "+
				"from s[0], not from the zero value", got, ok)
		}
	})
	t.Run("one element", func(t *testing.T) {
		if got, ok := generic.Max([]int{7}); got != 7 || !ok {
			t.Errorf("Max([7]) = %d, %v; want 7, true", got, ok)
		}
	})
	t.Run("strings", func(t *testing.T) {
		got, ok := generic.Max([]string{"apple", "pear", "fig"})
		if got != "pear" || !ok {
			t.Errorf("Max(apple, pear, fig) = %q, %v; want \"pear\", "+
				"true (strings compare in byte order)", got, ok)
		}
	})
	t.Run("floats", func(t *testing.T) {
		got, ok := generic.Max([]float64{1.5, math.Pi, -1})
		if got != math.Pi || !ok {
			t.Errorf("Max(1.5, pi, -1) = %v, %v; want pi, true", got, ok)
		}
	})
	t.Run("named float type", func(t *testing.T) {
		got, ok := generic.Max([]celsius{21.5, 30, 18})
		if got != 30 || !ok {
			t.Errorf("Max of celsius values = %v, %v; want 30, true. "+
				"cmp.Ordered includes ~float64, so named types work",
				got, ok)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got, ok := generic.Max([]int{}); got != 0 || ok {
			t.Errorf("Max of no ints = %d, %v; want 0, false", got, ok)
		}
		if got, ok := generic.Max[string](nil); got != "" || ok {
			t.Errorf("Max of no strings = %q, %v; want \"\", false. "+
				"`var zero T` gives the zero value of any T", got, ok)
		}
	})
}

func TestLP_5_Largest(t *testing.T) {
	t.Run("rects", func(t *testing.T) {
		rects := []shapes.Rect{{W: 1, H: 1}, {W: 3, H: 4}, {W: 2, H: 2}}
		var got shapes.Rect // a Rect comes back, not a Shape
		got, ok := generic.Largest(rects)
		if got != (shapes.Rect{W: 3, H: 4}) || !ok {
			t.Errorf("Largest(rects) = %v, %v; want Rect(3x4), true",
				got, ok)
		}
	})
	t.Run("tie keeps the first", func(t *testing.T) {
		rects := []shapes.Rect{{W: 1, H: 1}, {W: 2, H: 3}, {W: 3, H: 2}}
		got, _ := generic.Largest(rects)
		if got != (shapes.Rect{W: 2, H: 3}) {
			t.Errorf("Largest with a tie returned %v; the first of the "+
				"biggest should win (Rect(2x3)). Replace only on >", got)
		}
	})
	t.Run("circles", func(t *testing.T) {
		got, ok := generic.Largest([]shapes.Circle{{R: 2}, {R: 5}, {R: 1}})
		if got.R != 5 || !ok {
			t.Errorf("Largest(circles) = %v, %v; want Circle(r=5), true",
				got, ok)
		}
	})
	t.Run("mixed shapes", func(t *testing.T) {
		mixed := []shapes.Shape{
			shapes.Rect{W: 3, H: 3}, shapes.Circle{R: 2},
			&shapes.Rect{W: 1, H: 1},
		}
		got, ok := generic.Largest(mixed)
		if c, isCircle := got.(shapes.Circle); !isCircle || c.R != 2 || !ok {
			t.Errorf("Largest(Rect 3x3, Circle r=2, Rect 1x1) = %v, %v; "+
				"want Circle(r=2) (area 12.57 beats 9), true", got, ok)
		}
	})
	t.Run("pointers", func(t *testing.T) {
		a, b := &shapes.Rect{W: 1, H: 5}, &shapes.Rect{W: 2, H: 2}
		if got, _ := generic.Largest([]*shapes.Rect{a, b}); got != a {
			t.Errorf("Largest([]*Rect) should return the pointer %p, "+
				"got %p", a, got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		got, ok := generic.Largest([]shapes.Rect{})
		if got != (shapes.Rect{}) || ok {
			t.Errorf("Largest of no rects = %v, %v; want the zero Rect, "+
				"false", got, ok)
		}
		if s, ok := generic.Largest([]shapes.Shape(nil)); s != nil || ok {
			t.Errorf("Largest of no Shapes = %v, %v; want nil, false",
				s, ok)
		}
	})
}

func TestLP_5_Set(t *testing.T) {
	t.Run("strings", func(t *testing.T) {
		s := generic.NewSet[string]()
		if s.Len() != 0 || s.Has("a") {
			t.Fatalf("a new set should be empty: Len %d, Has(\"a\") %v",
				s.Len(), s.Has("a"))
		}
		if !s.Add("a") {
			t.Error("Add(\"a\") on an empty set should return true " +
				"(newly added)")
		}
		if s.Add("a") {
			t.Error("Add(\"a\") a second time should return false " +
				"(already there)")
		}
		s.Add("b")
		if !s.Has("a") || !s.Has("b") || s.Has("c") {
			t.Errorf("after adding a and b: Has(a)=%v Has(b)=%v "+
				"Has(c)=%v, want true true false",
				s.Has("a"), s.Has("b"), s.Has("c"))
		}
		if s.Len() != 2 {
			t.Errorf("Len() = %d after adding a, a, b; want 2", s.Len())
		}
	})
	t.Run("zero values are members too", func(t *testing.T) {
		s := generic.NewSet[int]()
		if s.Has(0) {
			t.Error("a new set of ints shouldn't contain 0")
		}
		if !s.Add(0) || !s.Has(0) || s.Len() != 1 {
			t.Error("Add(0) should store 0 like any other value: " +
				"use the comma-ok form, v, ok := m[k], to test membership")
		}
	})
	t.Run("structs", func(t *testing.T) {
		s := generic.NewSet[shapes.Rect]()
		s.Add(shapes.Rect{W: 1, H: 2})
		if !s.Has(shapes.Rect{W: 1, H: 2}) || s.Has(shapes.Rect{W: 2, H: 1}) {
			t.Error("a Set[Rect] should compare rects field by field")
		}
	})
	t.Run("sets are independent", func(t *testing.T) {
		a, b := generic.NewSet[string](), generic.NewSet[string]()
		a.Add("x")
		if b.Has("x") || b.Len() != 0 {
			t.Error("adding to one set changed another; each NewSet " +
				"needs its own map")
		}
	})
	t.Run("many", func(t *testing.T) {
		s := generic.NewSet[int]()
		added := 0
		for i := range 3000 {
			if s.Add(i % 1000) {
				added++
			}
		}
		if added != 1000 || s.Len() != 1000 {
			t.Errorf("adding 0..999 three times: %d Adds returned true "+
				"and Len is %d, want 1000 and 1000", added, s.Len())
		}
	})
}

func lp5Imports(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			got[p] = true
		}
	}
	return got
}

func TestLP_5_UsesInterfacesKit(t *testing.T) {
	if !lp5Imports(t)["burrow/primer/shapes"] {
		t.Error("Largest should use shapes.Shape from P.4 as its " +
			"constraint: import \"burrow/primer/shapes\"")
	}
}
