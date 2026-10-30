package store_test

import (
	"testing"

	"burrow/internal/testkit"
)

// Your own tests in store/ are graded here. Each mutant below is ValidateKey
// with one bug planted in it; at least one of your tests must fail for each.
func TestL0_2_YourTestsCatchBugs(t *testing.T) {
	testkit.CheckYourTests(t, testkit.MutationCheck{
		Dir:             "store",
		RequireSubtests: true,
		Mutants: []testkit.Mutant{
			{
				Bug:    "ValidateKey accepts a 251-byte key",
				Target: "ValidateKey",
				Source: `func ValidateKey(key string) error {
	if len(key) == 251 && OrigValidateKey(key[:250]) == nil {
		return nil
	}
	return OrigValidateKey(key)
}
`,
			},
			{
				Bug:    "ValidateKey rejects a key of exactly 250 bytes",
				Target: "ValidateKey",
				Source: `import "fmt"

func ValidateKey(key string) error {
	if len(key) == 250 {
		return fmt.Errorf("%w: %q is too long", ErrInvalidKey, key)
	}
	return OrigValidateKey(key)
}
`,
			},
			{
				Bug:    "ValidateKey accepts a key containing a tab",
				Target: "ValidateKey",
				Source: `import "strings"

func ValidateKey(key string) error {
	if strings.ContainsRune(key, '\t') && OrigValidateKey(strings.ReplaceAll(key, "\t", "x")) == nil {
		return nil
	}
	return OrigValidateKey(key)
}
`,
			},
			{
				Bug:    "ValidateKey accepts invalid UTF-8",
				Target: "ValidateKey",
				Source: `import (
	"strings"
	"unicode/utf8"
)

func ValidateKey(key string) error {
	if key != "" && !utf8.ValidString(key) && OrigValidateKey(strings.ToValidUTF8(key, "x")) == nil {
		return nil
	}
	return OrigValidateKey(key)
}
`,
			},
			{
				Bug:    "ValidateKey limits the length in runes instead of bytes (accepts 126 two-byte runes, 252 bytes)",
				Target: "ValidateKey",
				Source: `import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func ValidateKey(key string) error {
	if len(key) > 250 && utf8.ValidString(key) && utf8.RuneCountInString(key) <= 250 &&
		!strings.ContainsFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return nil
	}
	return OrigValidateKey(key)
}
`,
			},
		},
	})
}

// An example is documentation that's also a test: go test runs it and compares
// what it prints with its // Output: comment. go doc shows it next to ValidateKey.
func TestL0_2_Example(t *testing.T) {
	testkit.RequireExample(t, "store", "ExampleValidateKey")
}
