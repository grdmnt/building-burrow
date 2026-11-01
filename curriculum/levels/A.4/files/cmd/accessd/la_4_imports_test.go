package main

import (
	"bufio"
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"burrow/internal/testkit"
)

// An import rule: no non-test file in dir may import any path in deny
// (or anything under it). The tests check direction, never names.
type importRule struct {
	dir  string
	deny map[string]string // import path prefix -> why
}

var importRules = []importRule{
	{"access", map[string]string{
		"burrow": "The access package is the domain and its ports: it knows teams and keys, " +
			"not where they are stored or how they are served. Declare the interface you " +
			"need here (TeamStore, EventSink) and implement it in an adapter package.",
		"net/http": "HTTP belongs to the edge, access/httpapi. The domain takes plain Go " +
			"values and returns errors; the handler turns them into requests and statuses.",
		"database/sql": "Persistence belongs in an adapter that implements TeamStore, " +
			"like access/kvrepo; the domain must not know a database exists.",
	}},
	{"access/kvrepo", map[string]string{
		"net/http": "kvrepo speaks to the KV interface it owns. Whether that KV is in " +
			"memory or behind HTTP is decided in main, which passes in a *burrow.Client.",
		"burrow/api": "Those are the Burrow API's wire types. kvrepo stores its own JSON " +
			"document and talks only through its KV interface.",
		"burrow/access/httpapi": "One adapter must not depend on another. Both depend " +
			"on access; cmd/accessd wires them together.",
	}},
	{"access/httpapi", map[string]string{
		"burrow/access/kvrepo": "Handlers call the Service through its port; only " +
			"cmd/accessd decides which TeamStore is real. Build it in main and pass it in.",
		"burrow/burrow": "Handlers call the Service, never Burrow. The Burrow client is " +
			"kvrepo's transport, and main is the only place that creates it.",
		"burrow/store": "Handlers know teams and versions, not keys in a KV store. Map " +
			"access errors in writeError; store errors never reach this layer.",
	}},
}

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// buildIgnored reports whether src opts out of every build with
// //go:build ignore before its package clause.
func buildIgnored(src []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if line == "//go:build ignore" {
			return true
		}
	}
	return false
}

type goImport struct {
	file, path string // file relative to the module root, slash-separated
	pos        token.Position
}

// importsIn parses the non-test files directly in dir (relative to root).
func importsIn(t *testing.T, root, dir string) (imps []goImport, files int) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return nil, 0
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(dir, n))
		src, err := os.ReadFile(filepath.Join(root, dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if buildIgnored(src) {
			continue
		}
		f, err := parser.ParseFile(fset, rel, src, parser.ImportsOnly)
		if err != nil {
			t.Errorf("%s doesn't parse: %v", rel, err)
			continue
		}
		files++
		for _, is := range f.Imports {
			p, _ := strconv.Unquote(is.Path.Value)
			imps = append(imps, goImport{rel, p, fset.Position(is.Pos())})
		}
	}
	return imps, files
}

func TestLA_4_ImportRules(t *testing.T) {
	root := testkit.ModuleRoot(t)
	for _, r := range append(importRules, importRule{dir: "cmd/accessd"}) {
		imps, files := importsIn(t, root, r.dir)
		if files == 0 {
			t.Errorf("%s/ has no Go files (outside tests). The Reference pins this directory.", r.dir)
			continue
		}
		for _, imp := range imps {
			for prefix, why := range r.deny {
				if under(imp.path, prefix) {
					t.Errorf("%s:%d imports %s. %s", imp.file, imp.pos.Line, imp.path, why)
				}
			}
		}
	}
}

// Only a program chooses an adapter. Everything else depends on access
// and its ports.
func TestLA_4_OnlyMainKnowsKvrepo(t *testing.T) {
	root := testkit.ModuleRoot(t)
	var dirs []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if rel != "." {
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir // another module: curriculum/, tools/, grpc/
			}
		}
		dirs = append(dirs, rel)
		return nil
	})
	for _, dir := range dirs {
		if dir == "cmd" || strings.HasPrefix(dir, "cmd/") || dir == "access/kvrepo" {
			continue
		}
		imps, _ := importsIn(t, root, dir)
		for _, imp := range imps {
			if under(imp.path, "burrow/access/kvrepo") {
				t.Errorf("%s:%d imports %s. Only programs under cmd/ choose which TeamStore is "+
					"real; %s should depend on access.TeamStore and be handed a store.",
					imp.file, imp.pos.Line, imp.path, dir)
			}
		}
	}
}
