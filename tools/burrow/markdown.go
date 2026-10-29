package main

import (
	"regexp"
	"strings"
)

var (
	mdCode  = regexp.MustCompile("`([^`]+)`")
	mdBold  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	goWords = regexp.MustCompile(`\b(func|type|struct|interface|package|var|const|return|map|chan|go|defer|select|case|if|else|for|range|switch)\b`)
	goComm  = regexp.MustCompile(`(//.*)$`)
)

func inline(s string) string {
	s = mdCode.ReplaceAllStringFunc(s, func(m string) string { return yellow(m[1 : len(m)-1]) })
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string { return bold(m[2 : len(m)-2]) })
	return s
}

// renderMarkdown renders the small markdown subset the level READMEs use.
func renderMarkdown(src string) string {
	var out []string
	lines := strings.Split(src, "\n")
	width := termWidth - 4
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
			code := strings.ReplaceAll(l, "\t", "    ")
			if lang == "go" {
				comment := ""
				if m := goComm.FindStringIndex(code); m != nil {
					comment, code = code[m[0]:], code[:m[0]]
				}
				code = goWords.ReplaceAllStringFunc(code, magenta) + gray(comment)
			} else if lang == "bash" {
				code = cyan(code)
			}
			out = append(out, "  "+faint("│ ")+code)
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
		case strings.HasPrefix(l, "- "):
			flush()
			for j, wl := range wrapVisible(inline(strings.TrimPrefix(l, "- ")), width-2) {
				if j == 0 {
					out = append(out, "  "+cyan("•")+" "+wl)
				} else {
					out = append(out, "    "+wl)
				}
			}
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

func appendGap(out []string) []string {
	if len(out) > 0 && out[len(out)-1] != "" {
		return append(out, "")
	}
	return out
}
