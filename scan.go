package main

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Local pre-filter: only lines matching one of these reach the model. False positives cost
// tokens; false negatives leave a secret behind, so the patterns lean broad.
var secretPatterns = []*regexp.Regexp{
	// Known key formats.
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}|sk_(live|test)_[A-Za-z0-9]{10,}|gh[opusr]_[A-Za-z0-9]{16,}|github_pat_\w{20,}|AKIA[0-9A-Z]{16}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{35}|eyJ[\w-]{10,}\.[\w-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY`),
	regexp.MustCompile(`(?i)--?(password|passwd|token|otp|api-key|secret)[ =]\S{4,}`),
	regexp.MustCompile(`(?i)\b(bearer|basic)\s+[\w.~+/=-]{8,}`),
	regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`),      // credentials in URLs
	regexp.MustCompile(`(^|\s)-p\S*[0-9!@#$%^&*]\S*`), // mysql -pSECRET
}

// KEY=value, "token": "value", Authorization: value. The value (group 1) must not be all digits
// (token counters) and must not start like code or a variable reference.
var keywordRe = regexp.MustCompile(`(?i)(?:api[_-]?key|secret|token|passw(?:or)?d|passwd|pwd|otp|credential|auth)[\w-]*["']?\s*[:=]\s*["']?([^\s"'\[{}()<>$,;]{4,})`)

var tokenRe = regexp.MustCompile(`[A-Za-z0-9+/_.=-]{20,}`)

// Random-looking but not secret: Claude/OpenAI object IDs and anything carrying a date.
var notSecret = regexp.MustCompile(`^(toolu|srvtoolu|msg|req|call|resp|rs|fc|ws|chatcmpl|run|thread|asst|step|batch|evt)_|\d{4}-\d{2}-\d{2}`)

// suspicious returns byte spans of line that look like secrets.
func suspicious(line string) [][2]int {
	var hits [][2]int
	for _, re := range secretPatterns {
		for _, m := range re.FindAllStringIndex(line, -1) {
			hits = append(hits, [2]int{m[0], m[1]})
		}
	}
	for _, m := range keywordRe.FindAllStringSubmatchIndex(line, -1) {
		if strings.Trim(line[m[2]:m[3]], "0123456789") != "" {
			hits = append(hits, [2]int{m[0], m[1]})
		}
	}
	// High-entropy tokens. Longer runs are blobs (base64 images); 2+ slashes are paths.
	for _, m := range tokenRe.FindAllStringIndex(line, -1) {
		tok := line[m[0]:m[1]]
		if len(tok) <= 200 && strings.Count(tok, "/") < 2 && !notSecret.MatchString(tok) && mixedCase(tok) && entropy(tok) >= 3.5 {
			hits = append(hits, [2]int{m[0], m[1]})
		}
	}
	return hits
}

func mixedCase(s string) bool {
	var up, low, dig bool
	for _, r := range s {
		up, low, dig = up || unicode.IsUpper(r), low || unicode.IsLower(r), dig || unicode.IsDigit(r)
	}
	return up && low && dig
}

func entropy(s string) float64 {
	freq := map[rune]float64{}
	for _, r := range s {
		freq[r]++
	}
	h, n := 0.0, float64(len(s))
	for _, c := range freq {
		h -= c / n * math.Log2(c/n)
	}
	return h
}

const (
	maxSnippet = 2000 // longer lines are sent as windows around each hit
	window     = 200
)

// snippets splits text into lines and returns the suspicious ones (or windows of them).
func snippets(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		hits := suspicious(line)
		if len(hits) == 0 {
			continue
		}
		if len(line) <= maxSnippet {
			out = append(out, line)
			continue
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i][0] < hits[j][0] })
		var ws [][2]int
		for _, h := range hits {
			s, e := max(0, h[0]-window), min(len(line), h[1]+window)
			for s > 0 && !utf8.RuneStart(line[s]) {
				s--
			}
			for e < len(line) && !utf8.RuneStart(line[e]) {
				e++
			}
			if n := len(ws); n > 0 && s <= ws[n-1][1] {
				ws[n-1][1] = max(ws[n-1][1], e)
			} else {
				ws = append(ws, [2]int{s, e})
			}
		}
		for _, w := range ws {
			out = append(out, line[w[0]:w[1]])
		}
	}
	return out
}

// masked reports whether red is orig with one or more non-empty spans replaced by Mask
// and nothing else changed. The model's answer is only applied when this holds.
func masked(orig, red string) bool {
	if red == orig || !strings.Contains(red, Mask) {
		return false
	}
	parts := strings.Split(red, Mask)
	if !strings.HasPrefix(orig, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for _, p := range parts[1 : len(parts)-1] {
		if pos+1 > len(orig) {
			return false
		}
		i := strings.Index(orig[pos+1:], p)
		if i < 0 {
			return false
		}
		pos += 1 + i + len(p)
	}
	last := parts[len(parts)-1]
	return len(orig)-len(last) > pos && strings.HasSuffix(orig, last)
}

// jsonRecord returns every string value in a JSON record and its top-level "timestamp"/"ts"
// (RFC 3339, epoch ms or epoch s); zero if it has none.
func jsonRecord(raw []byte) ([]string, time.Time) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, time.Time{}
	}
	var ts time.Time
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"timestamp", "ts"} {
			switch t := m[k].(type) {
			case string:
				ts, _ = time.Parse(time.RFC3339Nano, t)
			case float64:
				if t > 1e12 {
					ts = time.UnixMilli(int64(t))
				} else if t > 0 {
					ts = time.Unix(int64(t), 0)
				}
			}
			if !ts.IsZero() {
				break
			}
		}
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(v)
	return out, ts
}

// replaceJSON swaps orig for red inside a raw JSON record, matching their JSON-escaped
// forms so every other byte stays as the writer left it.
func replaceJSON(raw []byte, orig, red string) ([]byte, bool) {
	eo, er := jsonEscape(orig), jsonEscape(red)
	if !bytes.Contains(raw, eo) {
		return raw, false // ponytail: writer escaped differently (e.g.  ); counted as rejected
	}
	out := bytes.ReplaceAll(raw, eo, er)
	if !json.Valid(out) {
		return raw, false
	}
	return out, true
}

func jsonEscape(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return b.Bytes()[1 : b.Len()-2] // drop quotes and newline
}
