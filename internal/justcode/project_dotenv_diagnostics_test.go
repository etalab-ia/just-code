package justcode

import (
	"strings"
	"testing"
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
