package rex

import (
	"bytes"
	"strings"
)

// feedOSC scans p (carrying incomplete sequences across calls in *carry) for
// RIS ("ESC c"), shell prompt start ("OSC 133;A"), and OSC `code;` sequences
// up to maxSeq bytes, invoking onRIS, onPrompt, and onPayload respectively,
// and returning how many capability queries ("OSC code;?") were seen.
func feedOSC(
	carry *[]byte,
	p []byte,
	code string,
	maxSeq int,
	onRIS func(),
	onPrompt func(),
	onPayload func(string),
) (queries int) {
	if len(*carry) > 0 {
		p = append(*carry, p...)
		*carry = nil
	}
	codePrefix := code + ";"
	codeBytes := []byte(codePrefix)
	for {
		i := bytes.IndexByte(p, 0x1b)
		if i < 0 {
			return
		}
		p = p[i:]
		if len(p) < 2 {
			*carry = append([]byte(nil), p...)
			return
		}
		switch p[1] {
		case 'c': // RIS
			onRIS()
			p = p[2:]
			continue
		case ']':
		default:
			p = p[1:]
			continue
		}
		body, n := oscBody(p[2:])
		if n < 0 {
			if len(p) <= maxSeq && (mayBe(p[2:], codePrefix) || mayBe(p[2:], "133;")) {
				*carry = append([]byte(nil), p...)
			}
			return
		}
		p = p[2+n:]
		switch {
		case bytes.HasPrefix(body, []byte("133;A")) && (len(body) == 5 || body[5] == ';'):
			onPrompt()
		case !bytes.HasPrefix(body, codeBytes) || 2+n > maxSeq:
		case strings.TrimSpace(string(body[len(codeBytes):])) == "?":
			queries++
		default:
			onPayload(string(body[len(codeBytes):]))
		}
	}
}

// oscBody returns the body of an OSC string after its "ESC ]", and the
// length of the string with its terminator; -1 when it is unfinished. A
// sequence that cuts it ends it with no body.
func oscBody(p []byte) ([]byte, int) {
	for i, c := range p {
		if c == 0x07 {
			return p[:i], i + 1
		}
		if c == 0x1b {
			if i+1 == len(p) {
				return nil, -1
			}
			if p[i+1] == '\\' {
				return p[:i], i + 2
			}
			return nil, i
		}
	}
	return nil, -1
}

// mayBe tells whether p may be the start of a string starting with prefix.
func mayBe(p []byte, prefix string) bool {
	n := min(len(p), len(prefix))
	return string(p[:n]) == prefix[:n]
}
