package main

import (
	"strconv"
	"strings"

	"github.com/progrium/darwinkit/macos/appkit"
)

// ansiRun is a stretch of text that shares a single foreground color. A nil
// color means "use the default text color".
type ansiRun struct {
	text  string
	color appkit.Color
	fg    bool // whether color is set (vs. default)
}

// sgrColor maps a standard SGR foreground code (30–37, 90–97) to an NSColor.
// Codes we don't recognise (bold, background, etc.) return fg=false so the
// current foreground is left untouched.
func sgrColor(code int) (appkit.Color, bool) {
	switch code {
	case 30, 90:
		return appkit.Color_SystemGrayColor(), true
	case 31, 91:
		return appkit.Color_SystemRedColor(), true
	case 32, 92:
		return appkit.Color_SystemGreenColor(), true
	case 33, 93:
		return appkit.Color_SystemYellowColor(), true
	case 34, 94:
		return appkit.Color_SystemBlueColor(), true
	case 35, 95:
		return appkit.Color_SystemPurpleColor(), true
	case 36, 96:
		return appkit.Color_SystemTealColor(), true
	case 37, 97:
		return appkit.Color_TextColor(), true
	}
	return appkit.Color{}, false
}

// ansiParser turns a byte stream containing ANSI SGR color escapes into a
// sequence of colored runs. It is stateful across Write calls: the current
// foreground color carries over, and an escape sequence split across two writes
// is buffered until it completes.
type ansiParser struct {
	color   appkit.Color
	hasFg   bool
	pending string // incomplete trailing escape sequence carried to next feed
}

// feed consumes s and returns the runs it produced. Recognised escape sequences
// (ESC [ … m) are applied to the current color and dropped from the output;
// their bytes never appear in a run. Any other escape sequence is passed
// through unchanged rather than swallowed.
func (p *ansiParser) feed(s string) []ansiRun {
	s = p.pending + s
	p.pending = ""

	var runs []ansiRun
	var buf strings.Builder

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		runs = append(runs, ansiRun{text: buf.String(), color: p.color, fg: p.hasFg})
		buf.Reset()
	}

	for i := 0; i < len(s); {
		c := s[i]
		if c != 0x1b { // ESC
			buf.WriteByte(c)
			i++
			continue
		}

		// Possible escape sequence. We only handle CSI (ESC [ … final).
		seq, consumed, complete := parseEscape(s[i:])
		if !complete {
			// Incomplete sequence at end of input: buffer it for next feed.
			p.pending = s[i:]
			break
		}
		if params, ok := parseSGR(seq); ok {
			flush()
			p.applySGR(params)
		} else {
			// Not an SGR sequence — keep it verbatim so nothing is lost.
			buf.WriteString(seq)
		}
		i += consumed
	}

	flush()
	return runs
}

// applySGR updates the current foreground from a parsed SGR parameter list. An
// empty list or a leading 0 is a reset to the default color.
func (p *ansiParser) applySGR(params []int) {
	if len(params) == 0 {
		p.hasFg = false
		return
	}
	for _, code := range params {
		switch {
		case code == 0 || code == 39:
			p.hasFg = false
		default:
			if col, ok := sgrColor(code); ok {
				p.color = col
				p.hasFg = true
			}
		}
	}
}

// parseEscape inspects a string beginning with ESC and, for a CSI sequence
// (ESC [ … final-byte), returns the full sequence, how many bytes it spans, and
// whether it is complete. For a non-CSI or as-yet-incomplete sequence it reports
// complete=false so the caller can buffer or pass it through.
func parseEscape(s string) (seq string, consumed int, complete bool) {
	if len(s) < 2 {
		return "", 0, false
	}
	if s[1] != '[' {
		// Not a CSI sequence; emit ESC alone and move on.
		return s[:1], 1, true
	}
	for i := 2; i < len(s); i++ {
		if b := s[i]; b >= 0x40 && b <= 0x7e { // final byte
			return s[: i+1], i + 1, true
		}
	}
	return "", 0, false // still waiting for the final byte
}

// parseSGR parses a CSI sequence (ESC [ … m) into its numeric parameters. It
// returns ok=false for any CSI sequence that isn't an SGR ('m') command.
func parseSGR(seq string) ([]int, bool) {
	if len(seq) < 3 || seq[len(seq)-1] != 'm' {
		return nil, false
	}
	body := seq[2 : len(seq)-1] // strip "ESC[" and trailing "m"
	if body == "" {
		return nil, true // ESC[m == reset
	}
	var params []int
	for _, part := range strings.Split(body, ";") {
		if part == "" {
			params = append(params, 0)
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		params = append(params, n)
	}
	return params, true
}
