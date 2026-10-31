package shapes_test

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"burrow/primer/collections"
	"burrow/primer/shapes"
)

func lp4Close(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLP_4_AreaAndPerimeter(t *testing.T) {
	tests := []struct {
		name            string
		s               shapes.Shape
		area, perimeter float64
	}{
		{"Rect 3x4", shapes.Rect{W: 3, H: 4}, 12, 14},
		{"Rect 0.5x2", shapes.Rect{W: 0.5, H: 2}, 1, 5},
		{"Rect 0x0", shapes.Rect{}, 0, 0},
		{"Circle r=1", shapes.Circle{R: 1}, math.Pi, 2 * math.Pi},
		{"Circle r=2", shapes.Circle{R: 2}, 4 * math.Pi, 4 * math.Pi},
		{"Circle r=0.5", shapes.Circle{R: 0.5}, math.Pi / 4, math.Pi},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Area(); !lp4Close(got, tt.area) {
				t.Errorf("Area() of %s = %v, want %v", tt.name, got, tt.area)
			}
			if got := tt.s.Perimeter(); !lp4Close(got, tt.perimeter) {
				t.Errorf("Perimeter() of %s = %v, want %v", tt.name,
					got, tt.perimeter)
			}
		})
	}
}

// Rect and Circle never say "implements Shape". They satisfy it
// because they have its methods. These checks run at test time so a
// mistake gives a message instead of a compile error.
func TestLP_4_ValuesSatisfyShape(t *testing.T) {
	for _, v := range []any{shapes.Rect{}, shapes.Circle{}} {
		if _, ok := v.(shapes.Shape); !ok {
			t.Errorf("a plain %T value should satisfy Shape. Give Area "+
				"and Perimeter value receivers, like func (r Rect) Area()", v)
		}
	}
	var p any = &shapes.Rect{}
	if _, ok := p.(shapes.Shape); !ok {
		t.Error("a *Rect should satisfy Shape too (a pointer gets the " +
			"value methods for free)")
	}
}

func TestLP_4_ScaleHasPointerReceiver(t *testing.T) {
	r := shapes.Rect{W: 2, H: 3}
	r.Scale(2)
	if r != (shapes.Rect{W: 4, H: 6}) {
		t.Errorf("after Rect{2, 3}.Scale(2) the rect is %+v, want "+
			"{W:4 H:6}. A value receiver changes a copy; Scale needs a "+
			"pointer receiver: func (r *Rect) Scale(f float64)", r)
	}
	r.Scale(0.5)
	if r != (shapes.Rect{W: 2, H: 3}) {
		t.Errorf("Scale(0.5) should halve both sides, got %+v", r)
	}

	type scaler interface{ Scale(float64) }
	var v any = shapes.Rect{}
	if _, ok := v.(scaler); ok {
		t.Error("a plain Rect value has a Scale method, so Scale has a " +
			"value receiver and can't change the rect. Use (r *Rect)")
	}
	var p any = &shapes.Rect{}
	if _, ok := p.(scaler); !ok {
		t.Error("*Rect should have a Scale(float64) method")
	}
}

func TestLP_4_TotalArea(t *testing.T) {
	if got := shapes.TotalArea(); got != 0 {
		t.Errorf("TotalArea() with no shapes = %v, want 0", got)
	}
	one := shapes.TotalArea(shapes.Rect{W: 3, H: 4})
	if !lp4Close(one, 12) {
		t.Errorf("TotalArea(Rect{3, 4}) = %v, want 12", one)
	}
	list := []shapes.Shape{
		shapes.Rect{W: 1, H: 2},
		shapes.Circle{R: 1},
		&shapes.Rect{W: 10, H: 10},
	}
	want := 2 + math.Pi + 100
	if got := shapes.TotalArea(list...); !lp4Close(got, want) {
		t.Errorf("TotalArea(Rect{1, 2}, Circle{1}, &Rect{10, 10}) = %v, "+
			"want %v", got, want)
	}
}

func TestLP_4_Stringers(t *testing.T) {
	r := shapes.Rect{W: 3, H: 4}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Rect{3, 4}", fmt.Sprint(r), "Rect(3x4)"},
		{"&Rect{3, 4}", fmt.Sprint(&r), "Rect(3x4)"},
		{"Rect{0.5, 2}", fmt.Sprint(shapes.Rect{W: 0.5, H: 2}),
			"Rect(0.5x2)"},
		{"Circle{1.5}", fmt.Sprint(shapes.Circle{R: 1.5}),
			"Circle(r=1.5)"},
		{"Circle{10}", fmt.Sprintf("%v", shapes.Circle{R: 10}),
			"Circle(r=10)"},
		{"a slice", fmt.Sprint([]shapes.Shape{
			shapes.Rect{W: 1, H: 2}, shapes.Circle{R: 3}}),
			"[Rect(1x2) Circle(r=3)]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("printing %s gave %q, want %q. fmt uses your "+
					"String() method; %%g prints 3 not 3.000000",
					tt.name, tt.got, tt.want)
			}
		})
	}
	var v any = shapes.Rect{}
	if _, ok := v.(fmt.Stringer); !ok {
		t.Error("a plain Rect value should be a fmt.Stringer: give " +
			"String a value receiver so fmt finds it on values too")
	}
}

func TestLP_4_FetchError(t *testing.T) {
	cause := errors.New("connection refused")
	var err error = &shapes.FetchError{URL: "https://go.dev/", Err: cause}
	want := "fetch https://go.dev/: connection refused"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is(fetchErr, cause) should be true: Unwrap " +
			"should return the Err field")
	}
	if errors.Unwrap(err) != cause {
		t.Errorf("errors.Unwrap(fetchErr) = %v, want the Err field",
			errors.Unwrap(err))
	}

	wrapped := fmt.Errorf("crawl: %w", &shapes.FetchError{
		URL: "https://x.test/a", Err: fs.ErrNotExist})
	var fe *shapes.FetchError
	if !errors.As(wrapped, &fe) {
		t.Fatal("errors.As should find a *FetchError inside an error " +
			"wrapped with %w")
	}
	if fe.URL != "https://x.test/a" {
		t.Errorf("errors.As found URL %q, want %q", fe.URL,
			"https://x.test/a")
	}
	if !errors.Is(wrapped, fs.ErrNotExist) {
		t.Error("errors.Is should see through both layers: the %w " +
			"wrap and your Unwrap")
	}
}

func TestLP_4_Sqrt(t *testing.T) {
	var v any = shapes.ErrNegativeSqrt(-2)
	if _, ok := v.(error); !ok {
		t.Fatal("ErrNegativeSqrt should be an error: " +
			"func (e ErrNegativeSqrt) Error() string")
	}
	for _, x := range []float64{0, 1, 2, 4, 9, 1e6} {
		got, err := shapes.Sqrt(x)
		if err != nil {
			t.Errorf("Sqrt(%v) returned error %v, want nil", x, err)
		}
		if math.Abs(got-math.Sqrt(x)) > 1e-9 {
			t.Errorf("Sqrt(%v) = %v, want %v", x, got, math.Sqrt(x))
		}
	}
	for _, x := range []float64{-2, -0.5, -100} {
		got, err := shapes.Sqrt(x)
		if err == nil {
			t.Errorf("Sqrt(%v) should return an error", x)
			continue
		}
		if got != 0 {
			t.Errorf("Sqrt(%v) returned %v with its error; return 0 "+
				"alongside an error", x, got)
		}
		want := "cannot Sqrt negative number: " +
			strconv.FormatFloat(x, 'g', -1, 64)
		if err.Error() != want {
			t.Errorf("Sqrt(%v) error = %q, want %q", x, err.Error(), want)
		}
		var neg shapes.ErrNegativeSqrt
		if !errors.As(err, &neg) || float64(neg) != x {
			t.Errorf("Sqrt(%v) should return ErrNegativeSqrt(%v), "+
				"got %#v", x, x, err)
		}
	}
}

func TestLP_4_Describe(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "nil"},
		{"int", 42, "int 42"},
		{"negative int", -7, "int -7"},
		{"string", "hi", `string "hi"`},
		{"empty string", "", `string ""`},
		{"plain error", errors.New("boom"), "error: boom"},
		{"FetchError", &shapes.FetchError{URL: "u",
			Err: errors.New("down")}, "error: fetch u: down"},
		{"ErrNegativeSqrt", shapes.ErrNegativeSqrt(-1),
			"error: cannot Sqrt negative number: -1"},
		{"Rect", shapes.Rect{W: 3, H: 4}, "shape with area 12"},
		{"*Rect", &shapes.Rect{W: 1, H: 2}, "shape with area 2"},
		{"Circle", shapes.Circle{}, "shape with area 0"},
		{"time.Duration", time.Second, "stringer 1s"},
		{"float64", 3.5, "other float64"},
		{"int64", int64(5), "other int64"},
		{"bool", true, "other bool"},
		{"slice", []int{1}, "other []int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shapes.Describe(tt.in)
			if got == tt.want {
				return
			}
			hint := ""
			switch tt.name {
			case "Rect", "*Rect":
				hint = " A Rect is a Shape and also a Stringer; the " +
					"first matching case wins, so put case Shape " +
					"before case fmt.Stringer."
			case "int64":
				hint = " case int matches only int, not int64."
			}
			t.Errorf("Describe(%#v) = %q, want %q.%s", tt.in, got,
				tt.want, hint)
		})
	}
}

func lp4ReadAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading a Rot13Reader: %v", err)
	}
	return string(b)
}

func TestLP_4_Rot13Reader(t *testing.T) {
	var _ io.Reader = shapes.Rot13Reader{}
	tests := []struct{ in, want string }{
		{"Lbh penpxrq gur pbqr!", "You cracked the code!"},
		{"abcxyz ABCXYZ", "nopklm NOPKLM"},
		{"nopklm NOPKLM", "abcxyz ABCXYZ"},
		{"123 !? [] `{}@", "123 !? [] `{}@"},
		{"héllo", "uéyyb"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			r := shapes.Rot13Reader{R: strings.NewReader(tt.in)}
			if got := lp4ReadAll(t, r); got != tt.want {
				t.Errorf("rot13 of %q = %q, want %q. Only a-z and A-Z "+
					"move, 13 places, wrapping round; case is kept",
					tt.in, got, tt.want)
			}
		})
	}

	plain := strings.Repeat("The quick brown fox jumps. ", 40)
	twice := shapes.Rot13Reader{R: shapes.Rot13Reader{
		R: strings.NewReader(plain)}}
	if got := lp4ReadAll(t, twice); got != plain {
		t.Error("rot13 applied twice should give back the original text")
	}
}

func TestLP_4_Rot13ReaderContract(t *testing.T) {
	plain := "Readers can hand back any amount, even one byte at a time."
	enc := lp4ReadAll(t, shapes.Rot13Reader{R: strings.NewReader(plain)})

	t.Run("iotest.TestReader", func(t *testing.T) {
		r := shapes.Rot13Reader{R: strings.NewReader(enc)}
		if err := iotest.TestReader(r, []byte(plain)); err != nil {
			t.Errorf("Rot13Reader breaks the io.Reader rules: %v", err)
		}
	})
	t.Run("one byte at a time", func(t *testing.T) {
		r := shapes.Rot13Reader{
			R: iotest.OneByteReader(strings.NewReader(enc))}
		if got := lp4ReadAll(t, r); got != plain {
			t.Errorf("got %q, want %q", got, plain)
		}
	})
	t.Run("data with EOF", func(t *testing.T) {
		r := shapes.Rot13Reader{
			R: iotest.DataErrReader(strings.NewReader(enc))}
		if got := lp4ReadAll(t, r); got != plain {
			t.Errorf("got %q, want %q. When the inner Read returns n > 0 "+
				"and io.EOF together, those n bytes are still data: "+
				"convert them before returning", got, plain)
		}
	})
	t.Run("only p[:n]", func(t *testing.T) {
		r := shapes.Rot13Reader{R: strings.NewReader("ab")}
		p := []byte("zzzzzzzzzz")
		n, _ := r.Read(p)
		if n != 2 || string(p) != "nozzzzzzzz" {
			t.Errorf("Read into a 10-byte buffer from \"ab\" gave n=%d "+
				"and buffer %q, want 2 and %q. Only p[:n] holds data; "+
				"leave the rest alone", n, p, "nozzzzzzzz")
		}
	})
	t.Run("errors pass through", func(t *testing.T) {
		boom := errors.New("disk on fire")
		r := shapes.Rot13Reader{R: iotest.ErrReader(boom)}
		n, err := r.Read(make([]byte, 8))
		if n != 0 || err != boom {
			t.Errorf("Read from a failing reader = (%d, %v), want "+
				"(0, %v): return the inner reader's error", n, err, boom)
		}
	})
}

func TestLP_4_CountWords(t *testing.T) {
	text := "the cat and the hat and THE bat"
	got, err := shapes.CountWords(strings.NewReader(text))
	if err != nil {
		t.Fatalf("CountWords: %v", err)
	}
	if want := collections.WordCount(text); !reflect.DeepEqual(got, want) {
		t.Errorf("CountWords(%q) = %v, want %v (what "+
			"collections.WordCount gives for the same text)", text, got,
			want)
	}
	if got["the"] != 3 {
		t.Errorf("CountWords(%q)[\"the\"] = %d, want 3", text, got["the"])
	}

	secret := shapes.Rot13Reader{R: strings.NewReader("Uryyb uryyb jbeyq")}
	got, err = shapes.CountWords(secret)
	if err != nil {
		t.Fatalf("CountWords(Rot13Reader): %v", err)
	}
	want := map[string]int{"hello": 2, "world": 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CountWords on a Rot13Reader = %v, want %v. CountWords "+
			"takes any io.Reader, decoded or not", got, want)
	}

	boom := errors.New("network down")
	r := io.MultiReader(strings.NewReader("a b c"), iotest.ErrReader(boom))
	if _, err := shapes.CountWords(r); !errors.Is(err, boom) {
		t.Errorf("CountWords on a reader that fails should return its "+
			"error, got %v", err)
	}
}

// lp4Imports lists the packages that the non-test files of this
// package import.
func lp4Imports(t *testing.T) map[string]bool {
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

func TestLP_4_UsesCollectionsKit(t *testing.T) {
	if !lp4Imports(t)["burrow/primer/collections"] {
		t.Error("CountWords should reuse collections.WordCount from P.3 " +
			"rather than counting again: import " +
			"\"burrow/primer/collections\"")
	}
}
