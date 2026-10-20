package justcode

import (
	"strings"
	"testing"
	"time"
)

// The import path used to refuse a whole file with one message shared by four
// unrelated faults ("dotenv file is too large or contains invalid text"), which
// named neither the variable nor the rule: a UTF-8 problem read as a size
// problem, and a carriage return as an unsupported syntax. The qualification
// protocol requires each refused class to name the variable and the reason
// (#208). These tests pin the diagnosis and, just as importantly, the safety
// property that made the original message vague: the parser's own error quotes
// the surrounding source, which may be the secret, so it must never be
// returned.

func TestImportRefusalNamesTheVariableAndTheRule(t *testing.T) {
	for _, c := range []struct {
		name    string
		data    string
		wantVar string
		wantIn  string
	}{
		{
			name:    "bare carriage return inside a value",
			data:    "CR_VALUE=bad\rreturn\n",
			wantVar: "CR_VALUE",
			wantIn:  "carriage return",
		},
		{
			name:    "invalid UTF-8",
			data:    "INVALID_UTF8=bad\xff\xfe\n",
			wantVar: "INVALID_UTF8",
			wantIn:  "UTF-8",
		},
		{
			name:    "NUL byte",
			data:    "NUL_VALUE=bad\x00x\n",
			wantVar: "NUL_VALUE",
			wantIn:  "NUL",
		},
		{
			// A name is an identifier, not a value, so it is safe to print —
			// and necessary: a file with many variables gives the user nothing
			// to act on when the refusal does not say which one.
			name:    "unsupported variable name",
			data:    "has space=1\n",
			wantVar: "has space",
			wantIn:  "not supported",
		},
	} {
		_, err := ParseProjectDotenv([]byte(c.data))
		if err == nil {
			t.Fatalf("%s: expected a refusal", c.name)
		}
		if !strings.Contains(err.Error(), c.wantVar) {
			t.Errorf("%s: the refusal must name the variable %q: %v", c.name, c.wantVar, err)
		}
		if !strings.Contains(err.Error(), c.wantIn) {
			t.Errorf("%s: the refusal must name the rule (%q): %v", c.name, c.wantIn, err)
		}
		// The generic wording was the defect: it must be gone.
		if strings.Contains(err.Error(), "too large or contains invalid text") {
			t.Errorf("%s: the merged message must be split: %v", c.name, err)
		}
		if strings.Contains(err.Error(), "syntax is invalid or unsupported") && c.name != "carriage return inside a value" {
			t.Errorf("%s: a diagnosed fault must not fall back to the generic wording: %v", c.name, err)
		}
	}
}

// TestImportRefusalNeverEchoesTheValue is the reason the original message was
// vague, and it must survive: godotenv's error embeds the source around the
// fault ("near %q"), and that source is often the secret itself.
func TestImportRefusalNeverEchoesTheValue(t *testing.T) {
	const secret = "SUPERSECRET_TOKEN_9f3a"
	for _, c := range []struct{ name, data string }{
		{"carriage return", "K=" + secret + "\rtailfragment\n"},
		{"invalid UTF-8", "K=" + secret + "\xff\xfe\n"},
		{"NUL byte", "K=" + secret + "\x00x\n"},
	} {
		_, err := ParseProjectDotenv([]byte(c.data))
		if err == nil {
			t.Fatalf("%s: expected a refusal", c.name)
		}
		// "tailfragment" is the value text that follows the fault, and the
		// parser's own error would quote it; it must not reach the message.
		// Each fragment is chosen not to occur in the fixed wording, so it
		// cannot match the message itself.
		for _, fragment := range []string{secret, "SUPERSECRET", "9f3a", "tailfragment"} {
			if strings.Contains(err.Error(), fragment) {
				t.Errorf("%s: the refusal leaked %q: %v", c.name, fragment, err)
			}
		}
	}
}

// TestImportAcceptsTheCarriageReturnFormsThatAreLegal guards the fix against
// over-reach. A carriage return is only fatal where it splits a value: CRLF is
// an ordinary line ending, and a bare CR followed by a well-formed assignment
// parses as two assignments. Refusing those would reject valid files.
func TestImportAcceptsTheCarriageReturnFormsThatAreLegal(t *testing.T) {
	for _, c := range []struct{ name, data string }{
		{"CRLF line endings", "A=ok\r\nB=two\r\n"},
		{"bare CR between assignments", "A=1\rB=2\n"},
		{"lone CRLF", "\r\n"},
	} {
		got, err := ParseProjectDotenv([]byte(c.data))
		if err != nil {
			t.Errorf("%s: must parse, not refuse: %v", c.name, err)
			continue
		}
		_ = got
	}
}

// TestImportRefusalNamesTheEnclosingAssignmentNotTheContinuation covers the
// non-disclosure hole an independent review found in the first version of this
// fix. That version located a fault's line by splitting on newlines, but
// godotenv lets a quoted value span physical lines — so in
// `K="first\nSECRET_TOKEN=...` it read the second line as an assignment and
// printed what was actually value text. The scanner now follows the parser's
// statement grammar, where the fault is attributed to K and no continuation
// line is ever mistaken for a key.
func TestImportRefusalNamesTheEnclosingAssignmentNotTheContinuation(t *testing.T) {
	const secret = "SUPERSECRET_TOKEN_9f3a"
	_, err := ParseProjectDotenv([]byte("K=\"first\n" + secret + "=\xff\"\n"))
	if err == nil {
		t.Fatal("an invalid byte inside a multiline value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("the refusal must name the enclosing assignment K: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "9f3a") {
		t.Errorf("the refusal leaked value text from a continuation line: %v", err)
	}
}

// TestImportRefusalBlamesOnlyTheFaultyCarriageReturn is the ordering guard: a
// bare carriage return is a legal separator, so when the parser fails on
// something else the carriage return must not be accused. The first version
// scanned unconditionally and named A for `A=1\rB=2\nBROKEN="`, whose real
// fault is the unterminated BROKEN quote.
func TestImportRefusalBlamesOnlyTheFaultyCarriageReturn(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("A=1\rB=2\nBROKEN=\"\n"))
	if err == nil {
		t.Fatal("an unterminated quoted value must be refused")
	}
	if strings.Contains(err.Error(), "A:") || strings.Contains(err.Error(), "B:") {
		t.Errorf("a legal bare-CR separator must not be blamed for an unrelated failure: %v", err)
	}
	if strings.Contains(err.Error(), "carriage return") {
		t.Errorf("the failure is an unterminated quote, not a carriage return: %v", err)
	}
}

// TestImportRefusalRecognizesYAMLDelimiter pins the second grammar gap: the
// parser accepts ':' as an assignment delimiter, so a diagnostic that only
// looks for '=' fails to name the variable on those lines.
func TestImportRefusalRecognizesYAMLDelimiter(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("API_KEY: bad\xff\n"))
	if err == nil {
		t.Fatal("an invalid byte must be refused")
	}
	if !strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("a colon-delimited assignment must be named like an equals one: %v", err)
	}
}

// TestCarriageReturnRemedyIsAccurate pins the advice itself. Converting the CR
// to LF does NOT repair `K=bad\rtail`: the trailing fragment is still a line
// with no assignment. Telling the user to do it would send them in a circle.
func TestCarriageReturnRemedyIsAccurate(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("K=bad\rtail\n"))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	msg := err.Error()
	if strings.Contains(msg, "convert the file to LF") {
		t.Errorf("LF conversion does not repair this shape, so it must not be advised: %v", err)
	}
	if !strings.Contains(msg, "remove the carriage return") {
		t.Errorf("the advice must be the remedy that works: %v", err)
	}
}

// The three cases below are the third review round's findings. Each is a fresh
// divergence between a hand-written grammar walker and the parser it imitated:
// comment blocks, carriage returns inside quoted values, and escape parity.
// They are the reason the walker was replaced by asking the parser directly
// (attributeFaultByte), so they are kept as the regression fixtures that pin
// the delegation.

// TestImportRefusalDoesNotBlameACommentLine: a byte inside a comment belongs to
// no variable. The walker used to advance past it and name the following
// assignment.
func TestImportRefusalDoesNotBlameACommentLine(t *testing.T) {
	for _, c := range []struct{ name, data string }{
		{"NUL in a leading comment", "# bad\x00\nNEXT=ok\n"},
		{"NUL in a middle comment", "A=1\n# bad\x00\nB=2\n"},
	} {
		_, err := ParseProjectDotenv([]byte(c.data))
		if err == nil {
			t.Fatalf("%s: expected a refusal", c.name)
		}
		for _, innocent := range []string{"NEXT:", "A:", "B:"} {
			if strings.Contains(err.Error(), innocent) {
				t.Errorf("%s: a comment byte must not be attributed to %s: %v", c.name, innocent, err)
			}
		}
	}
}

// TestImportRefusalIgnoresCarriageReturnInsideQuotes: godotenv keeps a CR
// inside a quoted value rather than treating it as a line ending, so a CR
// there cannot explain a failure elsewhere in the file.
func TestImportRefusalIgnoresCarriageReturnInsideQuotes(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("A=\"ok\rstill\"\nBROKEN=\"\n"))
	if err == nil {
		t.Fatal("an unterminated quoted value must be refused")
	}
	if strings.Contains(err.Error(), "A:") {
		t.Errorf("a carriage return inside a quoted value must not be blamed: %v", err)
	}
	if strings.Contains(err.Error(), "carriage return") {
		t.Errorf("the real fault is the unterminated quote: %v", err)
	}
}

// TestImportRefusalHandlesEscapedQuotes: godotenv closes a quoted value on an
// even run of backslashes and keeps it open on an odd one. A byte after such a
// value must be attributed with the same parity, or the wrong variable is named.
func TestImportRefusalHandlesEscapedQuotes(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("A=\"x\\\\\"\nB=bad\x00\n"))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), "A:") {
		t.Errorf("the byte after an escaped quote belongs to B, not A: %v", err)
	}
}

// The four tests below are the fourth review round's findings. Three of them
// target the parser-delegating oracle itself rather than the grammar: two are
// quadratic blow-ups and one is an interaction with dollar expansion. They are
// bounded engineering defects in a mechanism that can no longer diverge from
// the grammar, so they were fixed rather than replaced.

// TestImportRefusalAttributesDollarAdjacentByte: the injected marker must not
// be expanded away. godotenv reads `$NAME` as a variable reference, so a
// marker placed just after a '$' would vanish from the parsed value and lose
// the attribution with it. The oracle masks dollars exactly as the main parse
// does.
func TestImportRefusalAttributesDollarAdjacentByte(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("K=secret$\x00\n"))
	if err == nil {
		t.Fatal("a NUL byte must be refused")
	}
	if !strings.Contains(err.Error(), "K") {
		t.Errorf("a byte after a literal dollar must still be attributed to K: %v", err)
	}
}

// TestImportRefusalAttributesOverwrittenDuplicate: godotenv resolves duplicate
// assignments last-definition-wins, so the marker is absent from the parsed
// map when a later assignment overwrote the value it was inserted into. The
// oracle cuts the input after the marker so the parser can still see it.
func TestImportRefusalAttributesOverwrittenDuplicate(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("A=bad\x00\nA=good\n"))
	if err == nil {
		t.Fatal("a NUL byte must be refused")
	}
	if !strings.Contains(err.Error(), "A") {
		t.Errorf("an overwritten duplicate still names its key: %v", err)
	}
}

// TestImportRefusalStaysFastOnManyCandidateBytes bounds the work spent on
// attribution. Each candidate re-parses the whole input, so an unbounded scan
// over a file full of bare carriage returns is quadratic; the file is still
// within the 1 MiB limit, so the size check does not protect against it.
func TestImportRefusalStaysFastOnManyCandidateBytes(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString("K=1\r")
	}
	b.WriteString("BROKEN=\"\n")
	done := make(chan error, 1)
	go func() {
		_, err := ParseProjectDotenv([]byte(b.String()))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an unterminated quoted value must be refused")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attribution must stay bounded on a file full of bare carriage returns")
	}
}

// TestImportRefusalStaysFastOnHostileMarkerPrefix bounds token generation. A
// file containing the marker prefix followed by a long run of the extension
// byte would make every progressively longer candidate occur, copying an
// increasingly large string before the parse even begins.
func TestImportRefusalStaysFastOnHostileMarkerPrefix(t *testing.T) {
	var b strings.Builder
	b.WriteString("A=x\n# " + faultTokenPrefix)
	for i := 0; i < 200000; i++ {
		b.WriteByte('X')
	}
	b.WriteString("\nB=y\x00\n")
	done := make(chan error, 1)
	go func() {
		_, err := ParseProjectDotenv([]byte(b.String()))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a NUL byte must be refused")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("marker generation must not be quadratic on a hostile prefix")
	}
}

// TestImportSizeLimitStillReportsSize pins the one fault that really is about
// size: it must say so rather than fall into the encoding wording.
func TestImportSizeLimitStillReportsSize(t *testing.T) {
	big := make([]byte, MaxProjectDotenvBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	_, err := ParseProjectDotenv(big)
	if err == nil {
		t.Fatal("a file over the limit must be refused")
	}
	if !strings.Contains(err.Error(), "limit") || !strings.Contains(err.Error(), "byte") {
		t.Fatalf("the refusal must name the size and the limit: %v", err)
	}
	if strings.Contains(err.Error(), "UTF-8") || strings.Contains(err.Error(), "carriage return") {
		t.Fatalf("a size fault must not be reported as an encoding fault: %v", err)
	}
}
