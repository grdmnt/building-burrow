package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"sort"
	"strings"
	"time"
)

type Pos struct {
	Row  int `json:"row"`
	Col  int `json:"col"`
	Span int `json:"span"`
}

type Chapter struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Optional bool     `json:"optional"`
	Needs    []string `json:"needs"`
	Line     string   `json:"line"`
	Pos      Pos      `json:"pos"`
	Levels   []*Level `json:"levels"`
}

type Level struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Brief  string   `json:"brief"`
	Build  []string `json:"build"`
	Win    string   `json:"win"`
	Go     []string `json:"go"`
	Pkgs   []string `json:"pkgs"`
	Hints  []string `json:"hints"`
	Unlock string   `json:"unlock"`

	Idea       string   `json:"idea"`
	Difficulty int      `json:"difficulty"`
	Consumes   []string `json:"consumes"`
	Wild       string   `json:"wild"`
	Module     string   `json:"module,omitempty"` // nested module the level's code lives in, e.g. "grpc"

	ch  *Chapter
	idx int
}

type Curriculum struct {
	Title    string     `json:"title"`
	Chapters []*Chapter `json:"chapters"`

	root  string
	byID  map[string]*Level
	byKey map[string]*Chapter
	flat  []*Level
}

type Progress struct {
	Current   string               `json:"current,omitempty"`
	Started   map[string]time.Time `json:"started"`
	Completed map[string]time.Time `json:"completed"`
	Hints     map[string]int       `json:"hints"`

	path string
}

// ---------- loading ----------

func findRoot() (string, error) {
	if r := os.Getenv("BURROW_ROOT"); r != "" {
		return r, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "curriculum", "curriculum.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside a Building Burrow repo (no curriculum/curriculum.json above this folder)")
		}
		dir = parent
	}
}

func loadCurriculum(root string) (*Curriculum, error) {
	data, err := os.ReadFile(filepath.Join(root, "curriculum", "curriculum.json"))
	if err != nil {
		return nil, err
	}
	var c Curriculum
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("curriculum.json: %w", err)
	}
	c.root = root
	c.byID = map[string]*Level{}
	c.byKey = map[string]*Chapter{}
	for _, ch := range c.Chapters {
		c.byKey[ch.Key] = ch
		for i, l := range ch.Levels {
			l.ch, l.idx = ch, i
			c.byID[l.ID] = l
			c.flat = append(c.flat, l)
		}
	}
	return &c, nil
}

func loadProgress(root string) (*Progress, error) {
	p := &Progress{path: filepath.Join(root, ".burrow", "progress.json")}
	data, err := os.ReadFile(p.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("%s: %w", p.path, err)
		}
	}
	if p.Started == nil {
		p.Started = map[string]time.Time{}
	}
	if p.Completed == nil {
		p.Completed = map[string]time.Time{}
	}
	if p.Hints == nil {
		p.Hints = map[string]int{}
	}
	return p, nil
}

func (p *Progress) save() error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

// ---------- state ----------

type state int

const (
	stLocked state = iota
	stOpen
	stCurrent
	stDone
)

type world struct {
	cur  *Curriculum
	prog *Progress
}

func (w *world) levelDone(l *Level) bool { _, ok := w.prog.Completed[l.ID]; return ok }

func (w *world) chapterDone(ch *Chapter) bool {
	for _, l := range ch.Levels {
		if !w.levelDone(l) {
			return false
		}
	}
	return true
}

func (w *world) chapterOpen(ch *Chapter) bool {
	for _, k := range ch.Needs {
		if !w.chapterDone(w.cur.byKey[k]) {
			return false
		}
	}
	return true
}

func (w *world) chapterState(ch *Chapter) state {
	switch {
	case w.chapterDone(ch):
		return stDone
	case !w.chapterOpen(ch):
		return stLocked
	}
	for _, l := range ch.Levels {
		if w.prog.Current == l.ID {
			return stCurrent
		}
	}
	return stOpen
}

func (w *world) levelState(l *Level) state {
	switch {
	case w.levelDone(l):
		return stDone
	case !w.chapterOpen(l.ch):
		return stLocked
	case l.idx > 0 && !w.levelDone(l.ch.Levels[l.idx-1]):
		return stLocked
	case w.prog.Current == l.ID:
		return stCurrent
	}
	return stOpen
}

func (w *world) lockReason(l *Level) string {
	if !w.chapterOpen(l.ch) {
		var need []string
		for _, k := range l.ch.Needs {
			if !w.chapterDone(w.cur.byKey[k]) {
				need = append(need, chapterLabel(w.cur.byKey[k])+" "+w.cur.byKey[k].Name)
			}
		}
		return "needs " + strings.Join(need, " and ")
	}
	return "finish " + l.ch.Levels[l.idx-1].ID + " first"
}

// available lists levels you could start right now, in curriculum order.
func (w *world) available() []*Level {
	var out []*Level
	for _, l := range w.cur.flat {
		if s := w.levelState(l); s == stOpen || s == stCurrent {
			out = append(out, l)
		}
	}
	return out
}

func chapterLabel(ch *Chapter) string {
	if ch.Optional {
		return "Opt " + ch.Key
	}
	return "Ch " + ch.Key
}

// producer returns the level that builds the named part.
func (c *Curriculum) producer(part string) *Level {
	for _, l := range c.flat {
		if l.Unlock == part {
			return l
		}
	}
	return nil
}

// consumers returns the levels that use the named part.
func (c *Curriculum) consumers(part string) []*Level {
	var out []*Level
	for _, l := range c.flat {
		for _, p := range l.Consumes {
			if p == part {
				out = append(out, l)
			}
		}
	}
	return out
}

// ---------- level files ----------

func (c *Curriculum) levelDir(l *Level) string {
	return filepath.Join(c.root, "curriculum", "levels", l.ID)
}

// files returns the level's provided files as paths relative to the repo root.
func (c *Curriculum) files(l *Level) []string {
	base := filepath.Join(c.levelDir(l), "files")
	var out []string
	filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(base, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func (c *Curriculum) hasTests(l *Level) bool {
	for _, f := range c.files(l) {
		if strings.HasSuffix(f, "_test.go") {
			return true
		}
	}
	return false
}

// packages returns the ./dir package patterns that hold the level's tests.
func (c *Curriculum) packages(l *Level) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range c.files(l) {
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		d := "./" + filepath.ToSlash(filepath.Dir(f))
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func testPrefix(l *Level) string { return "L" + strings.ReplaceAll(l.ID, ".", "_") + "_" }

// install copies the level's files into the repo. It returns what it wrote.
func (c *Curriculum) install(l *Level) (added, updated []string, err error) {
	for _, rel := range c.files(l) {
		src := filepath.Join(c.levelDir(l), "files", rel)
		dst := filepath.Join(c.root, rel)
		want, err := os.ReadFile(src)
		if err != nil {
			return nil, nil, err
		}
		have, err := os.ReadFile(dst)
		switch {
		case err == nil && bytes.Equal(have, want):
			continue
		case err == nil:
			updated = append(updated, rel)
		default:
			added = append(added, rel)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(dst, want, 0o644); err != nil {
			return nil, nil, err
		}
	}
	return added, updated, nil
}

// tampered lists installed files that differ from the curriculum's copy.
func (c *Curriculum) tampered(l *Level) (changed, missing []string) {
	for _, rel := range c.files(l) {
		want, _ := os.ReadFile(filepath.Join(c.levelDir(l), "files", rel))
		have, err := os.ReadFile(filepath.Join(c.root, rel))
		if err != nil {
			missing = append(missing, rel)
		} else if !bytes.Equal(have, want) {
			changed = append(changed, rel)
		}
	}
	return
}

func (w *world) installedLevels() []*Level {
	var out []*Level
	for _, l := range w.cur.flat {
		_, started := w.prog.Started[l.ID]
		if (started || w.levelDone(l)) && w.cur.hasTests(l) {
			out = append(out, l)
		}
	}
	return out
}

func (w *world) resolve(args []string) (*Level, error) {
	if len(args) > 0 {
		l, ok := w.cur.byID[args[0]]
		if !ok {
			return nil, fmt.Errorf("no level %q. Levels look like 1.2 or S3.1 (see `burrow list`)", args[0])
		}
		return l, nil
	}
	if w.prog.Current != "" {
		if l, ok := w.cur.byID[w.prog.Current]; ok {
			return l, nil
		}
	}
	if av := w.available(); len(av) > 0 {
		return nil, fmt.Errorf("no level in progress. Start one with `burrow start %s` (or `burrow next`)", av[0].ID)
	}
	return nil, errors.New("no level in progress")
}
