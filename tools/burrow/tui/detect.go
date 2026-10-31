package tui

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
)

// DetectLight decides whether the terminal has a light background.
//
// In order: BURROW_THEME=light or dark wins; then COLORFGBG, which many
// terminals set; then the terminal is asked for its background colour with
// an OSC 11 query. The query is followed by a device-attributes request that
// every terminal answers, so a terminal that ignores OSC 11 costs no waiting.
// Anything unknown means dark.
func DetectLight() bool {
	switch strings.ToLower(os.Getenv("BURROW_THEME")) {
	case "light":
		return true
	case "dark":
		return false
	}
	if v := os.Getenv("COLORFGBG"); v != "" {
		parts := strings.Split(v, ";")
		if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			return bg == 7 || bg >= 9 && bg <= 15
		}
	}
	if light, ok := queryBackground(300 * time.Millisecond); ok {
		return light
	}
	return false
}

var oscBG = regexp.MustCompile(`\]11;rgb:([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})`)

func queryBackground(timeout time.Duration) (light, ok bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, false
	}
	defer tty.Close()
	state, err := term.MakeRaw(tty.Fd())
	if err != nil {
		return false, false
	}
	defer term.Restore(tty.Fd(), state)

	r, err := cancelreader.NewReader(tty)
	if err != nil {
		return false, false
	}
	defer r.Close()
	if _, err := tty.WriteString("\x1b]11;?\x1b\\\x1b[c"); err != nil {
		return false, false
	}

	got := make(chan string, 1)
	go func() {
		var buf []byte
		b := make([]byte, 256)
		for {
			n, err := r.Read(b)
			buf = append(buf, b[:n]...)
			// The device-attributes reply ends with "c" after "\x1b[?".
			if i := strings.LastIndex(string(buf), "\x1b[?"); i >= 0 && strings.Contains(string(buf[i:]), "c") || err != nil {
				got <- string(buf)
				return
			}
		}
	}()
	var reply string
	select {
	case reply = <-got:
	case <-time.After(timeout):
		r.Cancel()
		reply = <-got
	}
	m := oscBG.FindStringSubmatch(reply)
	if m == nil {
		return false, false
	}
	var lum float64
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		v, _ := strconv.ParseUint(m[i+1], 16, 32)
		max := float64(uint64(1)<<(4*len(m[i+1])) - 1)
		lum += w * float64(v) / max
	}
	return lum > 0.5, true
}
