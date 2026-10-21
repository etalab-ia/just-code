package justcode

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joho/godotenv"
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
			// A short, whitespace-free token is named (the qualification
			// protocol requires it); names that carry spaces or other stray
			// text are redacted, covered below.
			name:    "unsupported variable name",
			data:    "1invalid=1\n",
			wantVar: "1invalid",
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
		if strings.Contains(err.Error(), "syntax is invalid or unsupported") {
			t.Errorf("%s: a diagnosed fault must not fall back to the generic wording: %v", c.name, err)
		}
	}
}

// TestImportRefusalRedactsUnsafeVariableName pins the redaction half of the
// unsupported-name diagnosis. godotenv accepts names containing spaces, so a
// rejected name is not guaranteed to be a safe identifier: it can be stray or
// pasted text to the left of a delimiter (e.g. a value typed before an '='),
// or long enough to bloat the refusal. Such names must not be echoed — the
// rule is stated, but the raw name never reaches the message.
func TestImportRefusalRedactsUnsafeVariableName(t *testing.T) {
	longName := strings.Repeat("a", 200)
	for _, c := range []struct {
		name, data, suspect string
	}{
		{"name with spaces", "has space=1\n", "has space"},
		{"overlong name", longName + "=1\n", longName},
	} {
		_, err := ParseProjectDotenv([]byte(c.data))
		if err == nil {
			t.Fatalf("%s: an unsupported variable name must be refused", c.name)
		}
		if !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: the refusal must state the rule: %v", c.name, err)
		}
		if strings.Contains(err.Error(), c.suspect) {
			t.Errorf("%s: the rejected name %q must be redacted: %v", c.name, c.suspect, err)
		}
		if len(err.Error()) > 512 {
			t.Errorf("%s: the refusal must stay small: %d bytes", c.name, len(err.Error()))
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
	for _, c := range []struct {
		name, data string
		want       map[string]string
	}{
		{"CRLF line endings", "A=ok\r\nB=two\r\n", map[string]string{"A": "ok", "B": "two"}},
		{"bare CR between assignments", "A=1\rB=2\n", map[string]string{"A": "1", "B": "2"}},
		{"lone CRLF", "\r\n", map[string]string{}},
	} {
		got, err := ParseProjectDotenv([]byte(c.data))
		if err != nil {
			t.Errorf("%s: must parse, not refuse: %v", c.name, err)
			continue
		}
		for wantKey, wantVal := range c.want {
			if got.Values[wantKey] != wantVal {
				t.Errorf("%s: expected %s=%q, got %q", c.name, wantKey, wantVal, got.Values[wantKey])
			}
		}
		if len(got.Values) != len(c.want) {
			t.Errorf("%s: expected %d keys, got %d: %v", c.name, len(c.want), len(got.Values), got.Values)
		}
	}
}

// TestImportRefusalNamesTheCarriageReturnSplitAtEOF is the P2 counterpart to
// the newline-carrying case: a bare CR that splits a value whose trailing
// fragment sits at the very end of the file (no following newline) parses as a
// stray empty-named assignment, so the parse succeeds and carriageReturnFault
// never runs. It must still be refused with the same named carriage-return
// diagnosis rather than the generic empty-variable-name message. This is the
// shape a classic-Mac file (CR line endings) produces when its last line has a
// split value.
func TestImportRefusalNamesTheCarriageReturnSplitAtEOF(t *testing.T) {
	for _, c := range []struct{ name, data string }{
		{"split at EOF no newline", "K=bad\rreturn"},
		{"split at EOF", "K=bad\rtail"},
		{"split then more CRs", "A=1\rK=bad\rtail\r"},
	} {
		_, err := ParseProjectDotenv([]byte(c.data))
		if err == nil {
			t.Fatalf("%s: a carriage return splitting a value must be refused", c.name)
		}
		if !strings.Contains(err.Error(), "K:") {
			t.Errorf("%s: the refusal must name K: %v", c.name, err)
		}
		if !strings.Contains(err.Error(), "carriage return") {
			t.Errorf("%s: the refusal must state the carriage-return rule: %v", c.name, err)
		}
		if strings.Contains(err.Error(), "variable name") && strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: the empty-variable-name message must not replace the CR diagnosis: %v", c.name, err)
		}
	}
}

// TestImportRefusalNamesStrayTextWithoutAnAssignment covers the other empty-
// named shape: text with no '=' or ':' delimiter, preceded by a newline rather
// than a bare CR, is genuine stray content and must be refused with a message
// that says so instead of the generic empty-variable-name wording.
func TestImportRefusalNamesStrayTextWithoutAnAssignment(t *testing.T) {
	for _, c := range []struct{ name, data string }{
		{"stray text after a line", "A=1\nfoo"},
		{"bare value line", "=value\n"},
	} {
		_, err := ParseProjectDotenv([]byte(c.data))
		if err == nil {
			t.Fatalf("%s: stray non-assignment text must be refused", c.name)
		}
		if !strings.Contains(err.Error(), "not a named assignment") {
			t.Errorf("%s: the refusal must name the stray-text class: %v", c.name, err)
		}
		if strings.Contains(err.Error(), "variable name") && strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: the empty-variable-name message must not be used: %v", c.name, err)
		}
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

// TestImportRefusalReachesTheFaultyCarriageReturnBeyondLegalOnes is the fifth
// review round's finding, and the reason the candidate bound was replaced by a
// filter rather than merely raised. A legitimate file can carry many legal
// bare-CR separators before the one that actually splits a value; "try the
// first N" discarded exactly the candidate that mattered and fell back to the
// generic message. Skipping the legal separators without a parse keeps the scan
// linear *and* reaches the fault, whatever its position.
func TestImportRefusalReachesTheFaultyCarriageReturnBeyondLegalOnes(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "A%d=1\r", i)
	}
	b.WriteString("K=bad\rtail\n")
	_, err := ParseProjectDotenv([]byte(b.String()))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("the faulty carriage return must be named even after legal ones: %v", err)
	}
	if !strings.Contains(err.Error(), "carriage return") {
		t.Errorf("the refusal must state the carriage-return rule: %v", err)
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

// TestImportRefusalSkipsCommentAndBlankCarriageReturnSeparators is the sixth
// review round's P1. Comments and blank lines are legal statements too, so a
// file that uses them as bare-CR separators was re-parsing the whole input once
// per separator — the quadratic cost the filter exists to remove, reintroduced
// because the first classifier recognised only assignments. The filter now asks
// the parser whether the following statement stands on its own.
func TestImportRefusalSkipsCommentAndBlankCarriageReturnSeparators(t *testing.T) {
	var b strings.Builder
	// 8000 separators, not 2000: the quadratic implementation took ~1.3 s at
	// 2000 and slipped under a 2 s budget, so the test passed against the very
	// code it was written to catch. At 8000 it takes ~20 s against ~5 ms here.
	// A threshold is only as good as the gap it splits.
	for i := 0; i < 8000; i++ {
		b.WriteString("A=1\r# comment\n")
	}
	b.WriteString("K=bad\rtail\n")
	done := make(chan error, 1)
	go func() {
		_, err := ParseProjectDotenv([]byte(b.String()))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a carriage return splitting a value must be refused")
		}
		if !strings.Contains(err.Error(), "K:") {
			t.Errorf("the fault must be named past comment separators: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("comment-only separators must not cause a re-parse per separator")
	}
}

// TestImportRefusalKeepsAMalformedSeparatorAsCandidate is the sixth round's P2.
// `tail="` is not a well-formed statement — its quote is unterminated — so a
// carriage return in front of it may be the cause and must stay a candidate.
// The first classifier checked only the identifier and skipped it.
func TestImportRefusalKeepsAMalformedSeparatorAsCandidate(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("K=bad\rtail=\"\n"))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("a malformed-looking separator must not hide the fault: %v", err)
	}
}

// TestImportRefusalNamesQuotedDuplicateOverwrittenByLaterAssignment is the
// sixth round's P3. When the faulty value is quoted and a later duplicate
// overwrites it, cutting the input just after the marker leaves the quote open,
// so the truncated form does not parse and the key was lost. The cut is now
// also offered with the quote closed.
func TestImportRefusalNamesQuotedDuplicateOverwrittenByLaterAssignment(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("A=\"bad\x00\"\nA=good\n"))
	if err == nil {
		t.Fatal("a NUL byte in a value must be refused")
	}
	if !strings.Contains(err.Error(), "A:") {
		t.Errorf("a quoted duplicate overwritten later must still be named: %v", err)
	}
	if !strings.Contains(err.Error(), "NUL") {
		t.Errorf("the refusal must state the NUL rule: %v", err)
	}
}

// TestImportRefusalNamesTheFaultPastOverlappingLegalSeparators is the ninth
// round's P2 on the scan budget. Five CR-only blank separators in front of one
// long statement each opened a "statement" running to that same distant line
// end, so five scans of ~900 KB exceeded the 4 MiB budget before the loop
// reached the carriage return that actually split K. The file is legal apart
// from that one fault, and it received only the generic message.
//
// Walking statements removes the overlap: the separators partition the input, so
// each byte is scanned once. The assertion is therefore the invariant itself —
// the scan never exceeds the input — rather than a budget it could never reach.
func TestImportRefusalNamesTheFaultPastOverlappingLegalSeparators(t *testing.T) {
	var b strings.Builder
	b.WriteString("\r\r\r\r\r")
	b.WriteString("A=" + strings.Repeat("x", 900_000) + "\n")
	b.WriteString("K=bad\rtail\n")
	if b.Len() > MaxProjectDotenvBytes {
		t.Fatalf("fixture is %d bytes, over the import limit", b.Len())
	}

	var scanned int64
	prev := observeFaultScanBytes
	observeFaultScanBytes = func(n int) { atomic.AddInt64(&scanned, int64(n)) }
	defer func() { observeFaultScanBytes = prev }()

	_, err := ParseProjectDotenv([]byte(b.String()))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("overlapping legal separators must not cost the diagnosis: %v", err)
	}
	if got := atomic.LoadInt64(&scanned); got > int64(b.Len()) {
		t.Errorf("scanned %d bytes of a %d-byte file: the walk must stay one pass", got, b.Len())
	}
}

// TestImportFaultScanStaysLinearAcrossQuotedValues pins the scan invariant of the
// statement walk alongside the closing-quote cut.
//
// A quoted value holding many carriage returns is the shape that used to cost one
// whole-file filter pass per CR. The walk now charges each statement exactly the
// bytes it then advances by, so the total cannot exceed the input: this is a
// property of the loop's shape, not a budget that a hostile input can reach. No
// earlier implementation satisfies it, so the cost assertion cannot be mutated
// into failing — it fails earlier, on attribution. Read it as a guard against a
// future edit that charges a statement and then advances by less, not as the
// regression test for the quadratic behaviour: that one is the attribution check
// below, which names K here and A (twice) on the heads before this fix.
//
// The assertion is on scanned bytes, not wall clock: instrumented runs are far
// slower, so a seconds-based budget measures the machine rather than the code.
func TestImportFaultScanStaysLinearAcrossQuotedValues(t *testing.T) {
	// One long quoted value holding many CRs, closed once, then a real fault.
	const quotes = 4000
	var b strings.Builder
	b.WriteString("A=\"")
	b.WriteString(strings.Repeat("a\r", quotes))
	b.WriteString("\"\rK=bad\rtail\n")

	var scanned int64
	prev := observeFaultScanBytes
	observeFaultScanBytes = func(n int) { atomic.AddInt64(&scanned, int64(n)) }
	defer func() { observeFaultScanBytes = prev }()

	_, err := ParseProjectDotenv([]byte(b.String()))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.HasPrefix(err.Error(), "K:") {
		t.Errorf("the fault is in K: %v", err)
	}
	if got, limit := atomic.LoadInt64(&scanned), int64(len(b.String())); got > limit {
		t.Errorf("scanning is quadratic in the quoted value: scanned %d bytes of %d", got, limit)
	}
}

// TestImportRefusalNamesTheFaultAfterAQuotedValueOnTheSameLine is the tenth
// round's P2. Re-cutting an unterminated quote at the next LF swallowed every
// CR-separated statement in between, so `P=ok\rA="a\rb"\rK=bad\rtail` reported P
// — a legal separator — while the fault was two statements later in K. The cut
// now ends at the quote that closes the value, which is where the parser's own
// value scan ends, and takes only the one separator that follows it.
//
// The single-quoted and escaped-quote forms are here because the closing-quote
// scan has to agree with godotenv's, including its naive rule that a quote
// preceded by one backslash is escaped whatever the backslash run length.
func TestImportRefusalNamesTheFaultAfterAQuotedValueOnTheSameLine(t *testing.T) {
	cases := []struct{ name, input string }{
		{"double quoted", "P=ok\rA=\"a\rb\"\rK=bad\rtail\n"},
		{"single quoted", "P=ok\rA='a\rb'\rK=bad\rtail\n"},
		{"escaped quote inside", "P=ok\rA=\"a\\\"c\rb\"\rK=bad\rtail\n"},
		{"several CRs inside", "A=\"a\rb\rc\"\rK=bad\rtail\n"},
		{"legal statement between", "A=\"a\rb\"\rB=ok\rK=bad\rtail\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProjectDotenv([]byte(tc.input))
			if err == nil {
				t.Fatal("a carriage return splitting a value must be refused")
			}
			if !strings.HasPrefix(err.Error(), "K:") {
				t.Errorf("the fault is in K, not in the separators before it: %v", err)
			}
		})
	}
}

// TestImportRefusalIgnoresQuotedCarriageReturnsInABudgetedFile is the ninth
// round's P2 on the re-parse budget. A legal quoted value holding four carriage
// returns in a near-limit file spent four whole-file confirmations — they are
// content, not separators — and the fifth candidate, the real fault, was
// rejected with the generic message.
//
// A carriage return inside a quoted value must not reach the re-parse budget at
// all. The fixture sits at the import limit so the budget is genuinely tight:
// at 200 KB the old code still named K, which is why the size matters.
func TestImportRefusalIgnoresQuotedCarriageReturnsInABudgetedFile(t *testing.T) {
	padLen := MaxProjectDotenvBytes - 6000
	var b strings.Builder
	b.WriteString("A=\"a\rb\rc\rd\re\"\n")
	b.WriteString("P=" + strings.Repeat("z", padLen) + "\n")
	b.WriteString("K=bad\rtail\n")
	if b.Len() > MaxProjectDotenvBytes {
		t.Fatalf("fixture is %d bytes, over the import limit", b.Len())
	}

	var reparsed int64
	prev := observeFaultReparseBytes
	observeFaultReparseBytes = func(n int) { atomic.AddInt64(&reparsed, int64(n)) }
	defer func() { observeFaultReparseBytes = prev }()

	_, err := ParseProjectDotenv([]byte(b.String()))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("legal quoted carriage returns must not cost the diagnosis: %v", err)
	}
	if got := atomic.LoadInt64(&reparsed); got > maxFaultReparseBytes {
		t.Errorf("re-parsed %d bytes, over the %d-byte budget", got, maxFaultReparseBytes)
	}
}

// TestImportRefusalBoundsSeparatorFilterWork is the eighth round's P2, and the
// one that showed the separator filter itself had no work bound.
//
// The filter's premise is that the statements it cuts partition the input, so
// skipping a legal separator costs linear work overall. Carriage returns stacked
// on one long physical line break that premise: every one of them opens a
// statement running to the same distant line end, so k CRs on a line of length L
// cost k*L. Before the bound this was measured at 13 ms for 2000 CRs, 171 ms for
// 8000, 2.8 s for 32000 and 11.3 s for 64000 -- doubling the input quadrupled
// the time, i.e. genuinely quadratic, and the 1 MiB import limit extrapolates to
// tens of minutes inside a single scan.
//
// The assertion is on bytes scanned, not on elapsed time, for the reason the
// re-parse budget is: CI runs `go test -race`, roughly 23x slower, so a
// wall-clock threshold would measure the runner rather than the code.
func TestImportRefusalBoundsSeparatorFilterWork(t *testing.T) {
	var b strings.Builder
	b.WriteString(strings.Repeat("\r", 20000))
	b.WriteString("A=" + strings.Repeat("x", 20) + "\n")
	b.WriteString("BROKEN=\"\n")

	var scanned, reparsed int64
	prevScan, prevReparse := observeFaultScanBytes, observeFaultReparseBytes
	observeFaultScanBytes = func(n int) { atomic.AddInt64(&scanned, int64(n)) }
	observeFaultReparseBytes = func(n int) { atomic.AddInt64(&reparsed, int64(n)) }
	defer func() {
		observeFaultScanBytes, observeFaultReparseBytes = prevScan, prevReparse
	}()

	_, err := ParseProjectDotenv([]byte(b.String()))
	if err == nil {
		t.Fatal("a malformed file must be refused")
	}
	// The generic fallback is the expected outcome once the budget is spent; a
	// wrong variable name would not be.
	if strings.Contains(err.Error(), "BROKEN") {
		t.Errorf("the refusal must not name a variable it did not prove: %v", err)
	}
	if got := atomic.LoadInt64(&scanned); got > int64(b.Len()) {
		t.Errorf("the separator filter scanned %d bytes of a %d-byte file: the walk must stay one pass", got, b.Len())
	}
	if got := atomic.LoadInt64(&reparsed); got > maxFaultReparseBytes {
		t.Errorf("attribution re-parsed %d bytes, over the %d-byte budget", got, maxFaultReparseBytes)
	}
}

// TestImportRefusalStillNamesTheFaultAfterTheFilterBudget guards the obvious
// over-correction: bounding the scan must not cost a diagnosis on an ordinary
// file. A legitimate file's statements partition the input and so scan
// linearly, leaving it far below the budget.
func TestImportRefusalStillNamesTheFaultAfterTheFilterBudget(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "A%d=1\r", i)
	}
	b.WriteString("K=bad\rtail\n")
	_, err := ParseProjectDotenv([]byte(b.String()))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("a legal-separator prefix must not cost the diagnosis: %v", err)
	}
}

// TestImportRefusalNamesTheFaultInALargeClassicMacFile pins the largest
// classic-Mac file the importer accepts: ~1.04 MB of CR-terminated assignments,
// every byte of it scanned, with the fault on the last line. It is the shape
// that made the removed scan budget degrade — a budget sized from whole-file
// assumptions could be spent by a legal prefix — so it guards against
// reintroducing one. The scan assertion is the walk's invariant: one pass, never
// more bytes than the input holds.
func TestImportRefusalNamesTheFaultInALargeClassicMacFile(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < MaxProjectDotenvBytes-64; i++ {
		fmt.Fprintf(&b, "KEY%05d=value%05d\r", i, i)
	}
	b.WriteString("K=bad\rtail\n")
	if b.Len() > MaxProjectDotenvBytes {
		t.Fatalf("fixture is %d bytes, over the import limit; it would test the wrong refusal", b.Len())
	}

	var scanned int64
	prev := observeFaultScanBytes
	observeFaultScanBytes = func(n int) { atomic.AddInt64(&scanned, int64(n)) }
	defer func() { observeFaultScanBytes = prev }()

	_, err := ParseProjectDotenv([]byte(b.String()))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("a large well-formed file must not cost the diagnosis: %v", err)
	}
	if got := atomic.LoadInt64(&scanned); got > int64(b.Len()) {
		t.Errorf("scanned %d bytes of a %d-byte file: the walk must stay one pass", got, b.Len())
	}
}

// TestImportRefusalNamesAValueSplitBySeveralCarriageReturns is the eighth
// round's P2. Confirming one CR at a time could not repair `K=bad\rtail\rend`:
// neutralising either CR left the other fragment malformed, so the whole file
// still failed to parse and nothing was attributed. The repair drops the run of
// splits that continues the same value, which is what the remedy asks for.
func TestImportRefusalNamesAValueSplitBySeveralCarriageReturns(t *testing.T) {
	for _, in := range []string{
		"K=bad\rtail\rend\n",
		"K=a\rb\rc\rd\n",
	} {
		_, err := ParseProjectDotenv([]byte(in))
		if err == nil {
			t.Fatalf("a value split by carriage returns must be refused: %q", in)
		}
		if !strings.Contains(err.Error(), "K:") {
			t.Errorf("a value split by several carriage returns must be named: %q -> %v", in, err)
		}
		if !strings.Contains(err.Error(), "carriage return") {
			t.Errorf("the refusal must state the carriage-return rule: %v", err)
		}
	}
}

// TestImportRefusalDoesNotClaimTheRemainderHasNoAssignment is the eighth round's
// P2 on wording. `K=bad\rtail="` is attributed to K, but the remainder does
// contain an assignment delimiter and fails on its unterminated quote, so a
// message asserting the remainder "has no assignment" describes the wrong fault.
// The rule is about validity, and the message says so.
func TestImportRefusalDoesNotClaimTheRemainderHasNoAssignment(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("K=bad\rtail=\"\n"))
	if err == nil {
		t.Fatal("a carriage return splitting a value must be refused")
	}
	if !strings.Contains(err.Error(), "K:") {
		t.Errorf("the fault must be named: %v", err)
	}
	if strings.Contains(err.Error(), "has no assignment") {
		t.Errorf("the remainder does contain a delimiter; the message must not claim otherwise: %v", err)
	}
}

// TestImportRefusalBoundsWorkOnManyMalformedFragments is the seventh round's P1.
// The filter made legal separators free, but every *malformed* fragment still
// costs a whole-file re-parse, and a file can carry about one candidate per
// byte — so an 8 KB file with 2000 such fragments spent 1.04 s. A count cap was
// ruled out by fuzzing: the successful candidate can sit arbitrarily far down
// the list, so the budget is spent in re-parsed bytes and adapts to file size.
//
// It asserts the re-parsed bytes directly rather than a wall-clock budget. The
// first version used 3 seconds, and it failed in CI, whose `go test -race`
// instruments execution ~23x slower: the same input took 7.3 s instrumented
// against 0.3 s plain. A timing assertion measures the machine as much as the
// code, and this one had already been loosened once for the same reason. The
// byte count is the contract the code actually holds and does not vary with the
// runner; the time bound is kept only as a generous backstop against a hang.
func TestImportRefusalBoundsWorkOnManyMalformedFragments(t *testing.T) {
	var b strings.Builder
	b.WriteString("BROKEN=\"\n")
	for i := 0; i < 4000; i++ {
		b.WriteString("K=\"\r")
	}
	var reparsed int64
	prev := observeFaultReparseBytes
	observeFaultReparseBytes = func(n int) { atomic.AddInt64(&reparsed, int64(n)) }
	defer func() { observeFaultReparseBytes = prev }()

	done := make(chan error, 1)
	go func() {
		_, err := ParseProjectDotenv([]byte(b.String()))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a malformed file must be refused")
		}
		// The budget may be exhausted before the fault is found, so either the
		// key or the generic fallback is acceptable — a wrong name is not.
		if strings.Contains(err.Error(), "tail") || strings.Contains(err.Error(), "K=\"") {
			t.Errorf("the refusal must not quote source text: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("attribution must terminate on a file of malformed fragments")
	}
	got := atomic.LoadInt64(&reparsed)
	if got > maxFaultReparseBytes {
		t.Errorf("attribution re-parsed %d bytes, over the %d-byte budget", got, maxFaultReparseBytes)
	}
	// And the budget must actually be spent: without the bound this input
	// re-parses about len(data) per fragment, tens of MiB in total. A tiny
	// figure would mean the bypass was reached without doing the work.
	if got == 0 {
		t.Error("no re-parsing was observed: the attribution never ran")
	}
}

// TestImportRefusalNamesSingleQuotedDuplicateOverwrittenByLaterAssignment is the
// seventh round's P2. godotenv treats both `"` and `'` as quotes, so a quoted
// duplicate may use either; closing only the double quote left the single-quoted
// form unparsed and the key unnamed.
func TestImportRefusalNamesSingleQuotedDuplicateOverwrittenByLaterAssignment(t *testing.T) {
	_, err := ParseProjectDotenv([]byte("A='bad\x00'\nA=good\n"))
	if err == nil {
		t.Fatal("a NUL byte in a value must be refused")
	}
	if !strings.Contains(err.Error(), "A:") {
		t.Errorf("a single-quoted duplicate overwritten later must still be named: %v", err)
	}
	if !strings.Contains(err.Error(), "NUL") {
		t.Errorf("the refusal must state the NUL rule: %v", err)
	}
}

// TestImportRefusalStillNamesTheFaultAcrossCRLFLineEndings pins the CRLF
// boundary: a CRLF pair is an ordinary line ending, never a fault candidate, so
// a file that uses CRLF endings must still have a genuine bare-CR split
// diagnosed, and the CRLF pairs themselves must never be blamed.
func TestImportRefusalStillNamesTheFaultAcrossCRLFLineEndings(t *testing.T) {
	for _, c := range []struct{ name, data string }{
		{"fault after CRLF", "A=ok\r\nK=bad\rtail\r\n"},
		{"CRLF everywhere", "A=ok\r\nK=bad\rtail\r\nB=2\r\n"},
	} {
		_, err := ParseProjectDotenv([]byte(c.data))
		if err == nil {
			t.Fatalf("%s: a carriage return splitting a value must be refused", c.name)
		}
		if !strings.Contains(err.Error(), "K:") {
			t.Errorf("%s: the fault is in K: %v", c.name, err)
		}
		if !strings.Contains(err.Error(), "carriage return") {
			t.Errorf("%s: the refusal must state the carriage-return rule: %v", c.name, err)
		}
	}
}

// TestImportRefusalAmbiguousLocationNamesNothing pins the non-disclosure guard
// on attribution: when the injected marker lands in more than one value, no
// single variable can be named without guessing, so attribution returns nothing
// and the caller falls back to a safe, value-free message. Naming one of them
// would accuse a line that may be innocent, and explaining the ambiguity cannot
// be done without quoting values.
func TestImportRefusalAmbiguousLocationNamesNothing(t *testing.T) {
	marker := faultTokenPrefix + "DEADBEEF"
	// The marker occupies two distinct values: attribution must not choose.
	name, ok := keyHoldingMarker([]byte("A="+marker+"\nB="+marker+"\n"), "MASK", marker)
	if name != "" || !ok {
		t.Errorf("ambiguous marker must yield no name: got name=%q ok=%v", name, ok)
	}
	// A single holder still resolves, proving the guard above is what changed.
	single, ok := keyHoldingMarker([]byte("A="+marker+"\nB=hello\n"), "MASK", marker)
	if single != "A" || !ok {
		t.Errorf("single marker must resolve to A: got name=%q ok=%v", single, ok)
	}
}

// TestDotenvParserContractPinnedToV151 records the godotenv v1.5.1 behaviors
// the diagnostics below depend on, so a dependency bump that changes any of them
// fails loudly instead of silently changing what is reported. Each case states
// the exact contract and how the code relies on it.
func TestDotenvParserContractPinnedToV151(t *testing.T) {
	// A bare CR after a value leaves the trailing fragment as a new statement.
	// With no trailing newline the fragment parses as a bare (empty-named)
	// assignment, which the importer refuses separately. With a trailing
	// newline the newline becomes the first byte of the fragment's key scan,
	// so the parse fails -- the shape carriageReturnFault diagnoses.
	if _, err := godotenv.UnmarshalBytes([]byte("K=bad\rreturn")); err != nil {
		t.Errorf("contract: a bare CR at EOF must parse into a stray assignment: %v", err)
	}
	if _, err := godotenv.UnmarshalBytes([]byte("K=bad\rreturn\n")); err == nil {
		t.Error("contract: a trailing newline after a bare-CR fragment must fail the parse")
	}
	// CRLF pairs are normalized before parsing, not treated as a fault.
	if v, err := godotenv.UnmarshalBytes([]byte("A=\"x\r\ny\"\n")); err != nil || v["A"] != "x\ny" {
		t.Errorf("contract: CRLF inside a quoted value is normalized, not a fault: %v %q", err, v["A"])
	}
	// A bare CR between assignments is a legal separator.
	if v, err := godotenv.UnmarshalBytes([]byte("A=1\rB=2\n")); err != nil || v["A"] != "1" || v["B"] != "2" {
		t.Errorf("contract: a bare CR between assignments must split them legally: %v %q", err, v)
	}
	// A bare CR inside quotes is value content, not a terminator.
	if _, err := godotenv.UnmarshalBytes([]byte("A=\"ok\rstill\"\n")); err != nil {
		t.Errorf("contract: a bare CR inside quotes is value content, not a terminator: %v", err)
	}
	// isUnterminatedQuote keys on this exact error prefix when the walk re-cuts
	// a fragment at the quote that closes its value (project_dotenv.go). A
	// godotenv bump that rewords the error would stop the re-cut from firing
	// and turn quoted CRs back into budget-charged candidates.
	_, err := godotenv.UnmarshalBytes([]byte("A=\"unterminated\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "unterminated quoted value") {
		t.Errorf("contract: an unterminated quote must fail with %q, got %v", "unterminated quoted value", err)
	}
}
