package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderKeepsWidthAndPercent(t *testing.T) {
	width := 0
	for _, c := range []struct {
		done, total int
		pct         string
	}{{0, 0, "  0%"}, {0, 97, "  0%"}, {40, 97, " 41%"}, {97, 97, "100%"}, {150, 97, "100%"}} {
		line := render("smashing", c.done, c.total, 3, "claude 12/97")
		if n := utf8.RuneCountInString(line); width == 0 {
			width = n
		} else if n != width {
			t.Errorf("%d/%d: width %d, want %d: %q", c.done, c.total, n, width, line)
		}
		if !strings.Contains(line, c.pct) {
			t.Errorf("%d/%d: missing %q in %q", c.done, c.total, c.pct, line)
		}
	}
	if line := render("smashing", 5, 5, 0, ""); !strings.Contains(line, strings.Repeat("█", barWidth)) {
		t.Errorf("finished bar not fully redacted: %q", line)
	}
}
