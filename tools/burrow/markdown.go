package main

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	mdCode  = regexp.MustCompile("`([^`]+)`")
	mdBold  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	goWords = regexp.MustCompile(`\b(func|type|struct|interface|package|var|const|return|map|chan|go|defer|select|case|if|else|for|range|switch)\b`)
	goComm  = regexp.MustCompile(`(//.*)$`)
	mdItal  = regexp.MustCompile(`(^|[\s(])\*([^*\s][^*]*)\*`)
	mdSpan  = regexp.MustCompile("\x00[0-9]+\x00")
	mdNum   = regexp.MustCompile(`^(\d+)\. (.*)$`)
	mdSub   = regexp.MustCompile(`^\s+[-*] (.*)$`)
)

func inline(s string) string {
	// Code spans first, kept aside so * and ** inside them stay literal.
	var spans []string
	s = mdCode.ReplaceAllStringFunc(s, func(m string) string {
		spans = append(spans, yellow(m[1:len(m)-1]))
		return "\x00" + strconv.Itoa(len(spans)-1) + "\x00"
	})
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string { return bold(m[2 : len(m)-2]) })
	s = mdItal.ReplaceAllString(s, "${1}"+paint("3", "${2}"))
	return mdSpan.ReplaceAllStringFunc(s, func(m string) string {
		i, _ := strconv.Atoi(m[1 : len(m)-1])
		return spans[i]
	})
}

// renderMarkdown renders the small markdown subset the level READMEs use.
func renderMarkdown(src string) string { return renderMarkdownW(src, termWidth-4) }

// renderMarkdownW renders markdown to fit width columns (plus a 2-space indent).
func renderMarkdownW(src string, width int) string {
	var out []string
	lines := strings.Split(src, "\n")
	inCode, lang := false, ""
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		for _, l := range wrapVisible(inline(strings.Join(para, " ")), width) {
			out = append(out, "  "+l)
		}
		para = nil
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "```") {
			flush()
			if !inCode {
				inCode, lang = true, strings.TrimPrefix(l, "```")
				out = append(out, "  "+faint("┌"+strings.Repeat("─", 3)+ifs(lang != "", " "+lang+" ")))
			} else {
				inCode = false
				out = append(out, "  "+faint("└"+strings.Repeat("─", 3)))
			}
			continue
		}
		if inCode {
			// Long lines wrap inside the box, continuing under a faint ↳.
			commented := false
			for j, chunk := range chunkRunes(strings.ReplaceAll(l, "\t", "    "), width-4) {
				prefix := "  " + faint("│ ")
				if j > 0 {
					prefix = "  " + faint("│ ↳ ")
				}
				if commented {
					out = append(out, prefix+gray(chunk))
					continue
				}
				out = append(out, prefix+highlight(chunk, lang))
				commented = lang == "go" && goComm.MatchString(chunk)
			}
			continue
		}
		switch {
		case strings.HasPrefix(l, "# "):
			flush()
			out = appendGap(out)
			out = append(out, "  "+bold(cyan(strings.TrimPrefix(l, "# "))))
		case strings.HasPrefix(l, "## "):
			flush()
			h := strings.ToUpper(strings.TrimPrefix(l, "## "))
			out = appendGap(out)
			out = append(out, "  "+bold(h)+" "+faint(strings.Repeat("─", max(0, width-len(h)-1))))
		case strings.HasPrefix(l, "> "):
			flush()
			for _, wl := range wrapVisible(inline(strings.TrimPrefix(l, "> ")), width-2) {
				out = append(out, "  "+amber("▍ ")+wl)
			}
		case strings.HasPrefix(l, "- [ ] "), strings.HasPrefix(l, "- [x] "):
			flush()
			box := faint("☐")
			if strings.HasPrefix(l, "- [x] ") {
				box = green("☑")
			}
			out = append(out, hanging("  "+box+" ", "    ", inline(l[6:]), width-2)...)
		case strings.HasPrefix(l, "- "):
			flush()
			out = append(out, hanging("  "+cyan("•")+" ", "    ", inline(strings.TrimPrefix(l, "- ")), width-2)...)
		case mdNum.MatchString(l):
			flush()
			m := mdNum.FindStringSubmatch(l)
			num := m[1] + "."
			out = append(out, hanging("  "+cyan(num)+" ", "  "+strings.Repeat(" ", len(num)+1), inline(m[2]), width-len(num)-1)...)
		case mdSub.MatchString(l):
			flush()
			out = append(out, hanging("     "+faint("–")+" ", "       ", inline(mdSub.FindStringSubmatch(l)[1]), width-5)...)
		case strings.HasPrefix(l, "|"):
			flush()
			var rows [][]string
			for ; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
				cells := strings.Split(strings.Trim(lines[i], "|"), "|")
				if strings.Trim(lines[i], "|- ") == "" {
					continue
				}
				for k := range cells {
					cells[k] = inline(strings.TrimSpace(cells[k]))
				}
				rows = append(rows, cells)
			}
			i--
			out = append(out, renderTable(rows, width)...)
		case strings.TrimSpace(l) == "":
			flush()
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
		default:
			para = append(para, strings.TrimSpace(l))
		}
	}
	flush()
	return strings.Join(out, "\n")
}

func renderTable(rows [][]string, width int) []string {
	if len(rows) == 0 {
		return nil
	}
	cols := len(rows[0])
	widths := make([]int, cols)
	for _, r := range rows {
		for k := 0; k < cols && k < len(r); k++ {
			widths[k] = max(widths[k], visibleLen(r[k]))
		}
	}
	// Squeeze the last column if the table is too wide.
	total := 0
	for _, w := range widths {
		total += w + 3
	}
	for total > width {
		wi := 0
		for k := range widths {
			if widths[k] > widths[wi] {
				wi = k
			}
		}
		if widths[wi] <= 12 {
			break
		}
		widths[wi]--
		total--
	}
	var out []string
	for ri, r := range rows {
		var cells [][]string
		height := 1
		for k := 0; k < cols; k++ {
			v := ""
			if k < len(r) {
				v = r[k]
			}
			wrapped := wrapVisible(v, widths[k])
			cells = append(cells, wrapped)
			height = max(height, len(wrapped))
		}
		for h := 0; h < height; h++ {
			line := "  "
			for k := 0; k < cols; k++ {
				v := ""
				if h < len(cells[k]) {
					v = cells[k][h]
				}
				if ri == 0 {
					v = bold(ansiRE.ReplaceAllString(v, ""))
				}
				line += padRight(v, widths[k]) + faint(" │ ")
			}
			out = append(out, strings.TrimSuffix(line, faint(" │ ")))
		}
		if ri == 0 {
			sep := "  "
			for k := 0; k < cols; k++ {
				sep += faint(strings.Repeat("─", widths[k])) + ifs(k < cols-1, faint("─┼─"))
			}
			out = append(out, sep)
		}
	}
	return out
}

// hanging wraps text to width, putting first before the first line and rest
// before the others.
func hanging(first, rest, text string, width int) []string {
	var out []string
	for j, wl := range wrapVisible(text, width) {
		if j == 0 {
			out = append(out, first+wl)
		} else {
			out = append(out, rest+wl)
		}
	}
	return out
}

// chunkRunes splits s into pieces of at most n runes.
func chunkRunes(s string, n int) []string {
	r := []rune(s)
	if n < 10 || len(r) <= n {
		return []string{s}
	}
	var out []string
	for len(r) > n {
		cut := n
		// Prefer breaking after a space in the second half of the line.
		for k := n; k > n/2; k-- {
			if r[k-1] == ' ' {
				cut = k
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	return append(out, string(r))
}

func highlight(code, lang string) string {
	switch lang {
	case "go":
		comment := ""
		if m := goComm.FindStringIndex(code); m != nil {
			comment, code = code[m[0]:], code[:m[0]]
		}
		return goWords.ReplaceAllStringFunc(code, magenta) + gray(comment)
	case "bash":
		return cyan(code)
	}
	return code
}

func appendGap(out []string) []string {
	if len(out) > 0 && out[len(out)-1] != "" {
		return append(out, "")
	}
	return out
}
