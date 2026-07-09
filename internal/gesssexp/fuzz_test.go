package gesssexp

import (
	"bytes"
	"testing"
	"unicode"
)

var fuzzSeeds = [][]byte{
	[]byte(""),
	[]byte("; comment only\n"),
	[]byte("(deftemplate item\n  (slot id (type STRING) (required TRUE)))\n"),
	[]byte("(deffacts seed\n  (item (id \"I-1\")))\n"),
	[]byte("(deffacts)\n"),
	[]byte(`(defrule route ; trailing comment
  ?o <- (order (lane ?lane) (amount ?amount))
  (not (hold (lane ?lane)))
  (test (> ?amount 10))
  =>
  (assert (routed (lane ?lane)))
  (emit "routed " ?lane))
`),
	[]byte("(defquery q\n  (declare (variables ?id))\n  (item (id ?id))\n  (return (id ?id)))\n"),
	[]byte("(a (b (c (d (e (f))))))"),
	[]byte(`(str "escaped \" quote \\ backslash \n newline")`),
	[]byte("(nums 1 -2 3.5 -4.75 2.0 1e10 TRUE FALSE nil)"),
	[]byte("()"),
	[]byte("(unterminated \"string"),
	[]byte("(((("),
	[]byte("))))"),
	[]byte("(atom . ?var ?binding:field =)"),
	[]byte("; header\n\n; about a\n(a 1) ; trailing\n(b\n  ; before child\n  (c 2) ; after child\n  ; dangling\n)\n; tail one\n; tail two\n"),
	[]byte("(; head comment\n a)"),
	[]byte("(a ; between\n b)"),
	[]byte("(; only dangling\n)"),
	[]byte("; comment only file\n"),
}

func TestCountComments(t *testing.T) {
	cases := []struct {
		source string
		want   int
	}{
		{"", 0},
		{"(defrule r => (emit \"x\"))", 0},
		{"; leading comment\n(a)", 1},
		{"(a) ; trailing", 1},
		{"; one\n; two ; still two\n", 2},
		{`(str "; not a comment")`, 0},
		{`(str "escaped \" quote") ; real`, 1},
		{`(str "backslash \\") ; real`, 1},
		{`(unterminated "; still in string`, 0},
		{"a\"b ; after atom containing a quote", 1},
		{"0\" \";\"(;\n)", 1},
	}
	for _, tc := range cases {
		if got := countComments([]byte(tc.source)); got != tc.want {
			t.Errorf("countComments(%q) = %d, want %d", tc.source, got, tc.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		if _, err := Parse("fuzz.gess", source); err != nil {
			return
		}
		if _, err := Format("fuzz.gess", source); err != nil {
			t.Fatalf("Parse accepted input but Format failed: %v", err)
		}
	})
}

func FuzzFormatIdempotent(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		once, err := Format("fuzz.gess", source)
		if err != nil {
			return
		}
		if got, want := countComments(once), countComments(source); got != want {
			t.Fatalf("Format changed comment count from %d to %d:\nsource: %q\nonce:   %q", want, got, source, once)
		}
		twice, err := Format("fuzz.gess", once)
		if err != nil {
			t.Fatalf("formatted output does not reparse: %v\nonce: %q", err, once)
		}
		if !bytes.Equal(once, twice) {
			t.Fatalf("Format is not idempotent:\nonce:  %q\ntwice: %q", once, twice)
		}
	})
}

// countComments counts ; comments the lexer would produce. It mirrors the
// lexer's tokenization: strings open only at a token boundary (a '"' inside
// an atom continues the atom), and a comment runs to end of line.
func countComments(source []byte) int {
	const (
		boundary = iota
		inAtom
		inString
		inComment
	)
	count := 0
	state := boundary
	escaped := false
	for _, r := range string(source) {
		switch state {
		case inComment:
			if r == '\n' {
				state = boundary
			}
		case inString:
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				state = boundary
			}
		case inAtom:
			switch {
			case unicode.IsSpace(r) || r == '(' || r == ')':
				state = boundary
			case r == ';':
				state = inComment
				count++
			}
		default:
			switch {
			case unicode.IsSpace(r) || r == '(' || r == ')':
			case r == ';':
				state = inComment
				count++
			case r == '"':
				state = inString
			default:
				state = inAtom
			}
		}
	}
	return count
}
