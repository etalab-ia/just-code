package justcode

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/joho/godotenv"
)

const MaxProjectDotenvBytes = 1 << 20

// faultTokenPrefix and faultMaskPrefix label the markers the fault-attribution
// parse injects. They are named constants because the hostile-input test builds
// its input from the prefix: a test that hardcoded the string would silently
// stop testing anything the day the prefix changed, and would still pass.
const (
	faultTokenPrefix = "JUSTCODE_FLT_"
	faultMaskPrefix  = "JUSTCODE_MSK_"
)

// maxFaultReparseBytes bounds the total input re-parsed while attributing a
// fault. Each candidate confirmation re-parses the whole file, and a file can
// carry about one candidate per byte, so the uncapped scan is quadratic: a
// 1 MiB file whose every statement is malformed would re-parse 1 MiB a million
// times. Measured, an 8 KB file with 2000 malformed fragments already spends
// 1.04 s. The budget is expressed in re-parsed bytes rather than a number of
// attempts so that it adapts to size: a small file gets hundreds of attempts,
// while a 1 MiB file gets four. Measured, a 1 MiB file whose every fragment is
// malformed now spends 422 ms and returns the generic message. Past the budget the caller
// falls back to naming the file rather than the variable, which is a less
// specific message, never a wrong one.
//
// A plain "try the first N candidates" bound was tried and rejected twice over:
// it discarded the only candidate that mattered when a legitimate file carried
// more than N separators before the fault, and fuzzing shows the successful
// candidate can sit arbitrarily far down the list (a file with twenty quoted
// values containing a carriage return needs twenty failed confirmations before
// the real fault). A budget that spends itself only on genuinely malformed
// candidates — legal separators are skipped without re-parsing the file — keeps
// the diagnosis for every file that is not adversarially broken.
const maxFaultReparseBytes = 4 << 20

// maxFaultScanBytes bounds the bytes the separator filter examines before it
// gives up and lets the generic message be used.
//
// The filter is meant to make the scan linear: it skips a carriage return that
// begins a statement the parser accepts on its own, since such a CR cannot have
// split a value. That holds when the statements partition the input — one
// statement per CR, disjoint. It does not hold when many carriage returns sit on
// one long physical line: every one of them then opens a statement running to
// the same distant line end, so k CRs on a line of length L cost k*L instead of
// L. Measured, that is genuinely quadratic: 2000 CRs 13 ms, 4000 45 ms, 8000
// 171 ms, 16000 687 ms, 32000 2.8 s, 64000 11.3 s. Extrapolated to the 1 MiB
// import limit the single scan would run for tens of minutes.
//
// The bound is on bytes scanned, not on candidates, for the same reason the
// re-parse budget is: a count is too generous on a small file and too tight on a
// large one. Past it the filter stops and the caller falls back to the
// file-level message, which is less specific but never wrong.
//
// It cannot bite a file that is not adversarially shaped. When the statements do
// partition the input, each one ends at the next CR or LF, so the scanned total
// is at most len(data) -- and the import limit caps that at 1 MiB, well under
// this budget. Only overlapping statements (CRs stacked on one line) can exceed
// it, which is exactly the case worth giving up on.
const maxFaultScanBytes = 4 << 20

// observeFaultReparseBytes is a test seam: it is called with the size of each
// whole-file re-parse the fault attribution spends. Tests assert the work budget
// through it instead of through wall-clock time, because a timing assertion is
// only meaningful against the machine it was written on — this package's CI runs
// `go test -race`, which instrumented execution ~23x slower than a plain build
// (the same 4000-fragment input took 7.3 s instead of 0.3 s) and pushed a
// 3-second budget past its own bound. Re-parsed bytes are the contract the code
// actually holds, and they do not vary with the runner.
var observeFaultReparseBytes = func(int) {}

// observeFaultScanBytes is the same seam for the separator filter, so a test can
// assert the scan stays bounded without measuring wall-clock time.
var observeFaultScanBytes = func(int) {}

type ProjectDotenv struct {
	Values     map[string]string
	Duplicates []string
}

// ParseProjectDotenv parses a bounded dotenv file as data. Dollar expansion
// in godotenv is disabled with a fresh sentinel so references remain literal;
// no environment is read and no command is evaluated. Duplicate assignments
// use godotenv's last-definition-wins rule and are reported for preview.
func ParseProjectDotenv(data []byte) (ProjectDotenv, error) {
	// The three checks below used to share one message ("too large or contains
	// invalid text"), which named neither the variable nor the rule and sent
	// the user looking for a size problem in a file whose actual fault was an
	// encoding one (#208). Each says what it found, and the two that have a
	// culpable line name its variable.
	if len(data) > MaxProjectDotenvBytes {
		return ProjectDotenv{}, fmt.Errorf("dotenv file is %d bytes, over the %d-byte import limit", len(data), MaxProjectDotenvBytes)
	}
	if !utf8.Valid(data) {
		const reason = "the value is not valid UTF-8; dotenv files must be UTF-8"
		if name := attributeFaultByte(data, firstInvalidUTF8Byte(data)); name != "" {
			return ProjectDotenv{}, fmt.Errorf("%s: %s", name, reason)
		}
		return ProjectDotenv{}, fmt.Errorf("the dotenv file contains text no loader can decode: %s", reason)
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		if name := attributeFaultByte(data, i); name != "" {
			return ProjectDotenv{}, fmt.Errorf("%s: the value contains a NUL byte, which no dotenv loader accepts", name)
		}
		return ProjectDotenv{}, fmt.Errorf("the dotenv file contains a NUL byte, which no dotenv loader accepts")
	}
	markerBytes := make([]byte, 16)
	if _, err := rand.Read(markerBytes); err != nil {
		return ProjectDotenv{}, fmt.Errorf("prepare literal dotenv parser")
	}
	marker := "JUSTCODE_LITERAL_DOLLAR_" + hex.EncodeToString(markerBytes) + "_"
	source := string(data)
	for strings.Contains(source, marker) {
		if _, err := rand.Read(markerBytes); err != nil {
			return ProjectDotenv{}, fmt.Errorf("prepare literal dotenv parser")
		}
		marker = "JUSTCODE_LITERAL_DOLLAR_" + hex.EncodeToString(markerBytes) + "_"
	}
	values, err := godotenv.Unmarshal(strings.ReplaceAll(source, "$", marker))
	if err != nil {
		// The parser's own error quotes the source around the fault ("near
		// %q"), and a malformed line may contain a secret value — so it is
		// never returned. Instead the one fault the importer can name without
		// the value is diagnosed here: a carriage return that is not part of
		// a CRLF pair ends the assignment where godotenv reads it, and the
		// rest of the line is then parsed as a new statement (#208).
		if name := carriageReturnFault(data); name != "" {
			// The remedy has to match the fault: converting the CR to LF
			// would leave the same trailing fragment on its own line, where
			// it is still not a valid statement. What repairs the file is
			// removing the CR from the value or making the remainder a real
			// assignment.
			//
			// The wording says the remainder is not a valid statement rather
			// than that it has no assignment: `tail="` does contain a
			// delimiter and is rejected because its quote is unterminated,
			// and a remainder can also be malformed for other reasons. Only
			// the first CR is asserted as the cause, which is what the
			// confirmation actually proved.
			return ProjectDotenv{}, fmt.Errorf("%s: the value contains a carriage return, which ends the assignment early, so the rest of the line is read as a separate statement that is not a valid assignment; remove the carriage return from the value, or make the remainder a complete assignment", name)
		}
		// Anything else stays deliberately opaque: the reason cannot be given
		// without either quoting the source or guessing, and a wrong guess is
		// worse than an honest "unsupported". The message says what to do.
		return ProjectDotenv{}, fmt.Errorf("dotenv syntax is invalid or unsupported; the offending line is not quoted here because it may contain a secret value")
	}
	duplicates := map[string]bool{}
	seen := map[string]bool{}
	for key := range values {
		if !validSecretIdentifier(key) {
			// The name is safe to print — it is an identifier, not a value —
			// and printing it is the point: the qualification protocol requires
			// each refused class to name the variable, and a file with many
			// variables gives the user nothing to act on without it.
			return ProjectDotenv{}, fmt.Errorf("variable name %q is not supported; names may use letters, digits and underscore, may not start with a digit, and are limited to 128 characters", key)
		}
	}
	// Record repeated assignment lines without mistaking KEY=value text inside
	// a multiline quoted value for another assignment.
	var openQuote byte
	for _, line := range strings.Split(source, "\n") {
		if openQuote != 0 {
			if closesQuote(line, openQuote) {
				openQuote = 0
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "export ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, rawValue, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, exists := values[key]; !exists {
			continue
		}
		if seen[key] {
			duplicates[key] = true
		}
		seen[key] = true
		value := strings.TrimSpace(rawValue)
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') && !closesQuote(value[1:], value[0]) {
			openQuote = value[0]
		}
	}
	for key, value := range values {
		values[key] = strings.ReplaceAll(value, marker, "$")
	}
	dupList := make([]string, 0, len(duplicates))
	for key := range duplicates {
		dupList = append(dupList, key)
	}
	sort.Strings(dupList)
	return ProjectDotenv{Values: values, Duplicates: dupList}, nil
}

func closesQuote(line string, quote byte) bool {
	for i := 0; i < len(line); i++ {
		if line[i] == quote && (i == 0 || line[i-1] != '\\') {
			return true
		}
	}
	return false
}

// attributeFaultByte returns the name of the variable whose value contains the
// byte at offset, by asking the parser rather than imitating it.
//
// The byte is replaced with an unambiguous ASCII token and the file is parsed
// again; the token surfaces in whichever value the parser assigned it to, and
// that value's key is the answer. A byte belonging to no value — inside a
// comment, or in a file that still fails to parse — yields no name, and the
// caller falls back to naming only the file.
//
// Delegating to the parser is deliberate. The first versions of this diagnostic
// walked the grammar by hand, and three review rounds each found a fresh
// divergence from it: quoted values spanning physical lines, comment blocks,
// escape parity, carriage returns inside quotes. A hand-written walker has to
// reproduce every rule the parser applies, and misses are silent; the parser
// applies them by construction and cannot drift.
func attributeFaultByte(data []byte, offset int) string {
	if offset < 0 || offset >= len(data) {
		return ""
	}
	token := faultMarker(data, faultTokenPrefix)
	if token == "" {
		return ""
	}
	// The fault byte becomes the token, and the other bare CRs that continue
	// the same physical line are dropped with it. A value split by several CRs
	// — `K=bad\rtail\rend` — is not repaired by neutralising any single one, so
	// confirming them one at a time attributed nothing and fell back to the
	// file-level message. Suppressing the rest of the run repairs the line as a
	// whole, which is what the remedy ("remove the carriage return from the
	// value") actually asks for. Only bare CRs before the line's end are
	// dropped, so a fault on another line is untouched and the parse guard still
	// rejects an attribution that would not stand on its own.
	rest := data[offset+1:]
	if j := bytes.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	candidate := make([]byte, 0, len(data)+len(token))
	candidate = append(candidate, data[:offset]...)
	candidate = append(candidate, token...)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\r' {
			continue // another split in the same value: drop it with the first
		}
		candidate = append(candidate, rest[i])
	}
	candidate = append(candidate, data[offset+1+len(rest):]...)
	// Parse with the same literal-dollar masking the main parse applies.
	// Without it the injected token is expanded away whenever it lands just
	// after a '$', because godotenv then reads it as a variable reference —
	// `K=secret$<NUL>` would lose the token and lose the attribution with it.
	mask := faultMarker(candidate, faultMaskPrefix)
	if mask == "" {
		return ""
	}
	name, parsed := keyHoldingMarker(candidate, mask, token)
	if !parsed {
		// Neutralising this byte did not make the file parse, so the byte is
		// not what decides the outcome. Naming a variable here would accuse a
		// line that may be innocent. This guard is what keeps a legal bare-CR
		// separator from being blamed for an unrelated failure elsewhere.
		return ""
	}
	if name != "" {
		return name
	}
	// The file parses, but the marker is in no value: a later duplicate
	// assignment overwrote it, because godotenv resolves duplicates
	// last-definition-wins. Cutting the input just after the marker removes
	// the overwriting assignment, so the same parser can report the key the
	// marker belongs to. This runs only on an input that already parsed, so it
	// cannot reintroduce the accusation the guard above prevents.
	if end := offset + len(token); end <= len(candidate) {
		// Cutting mid-value can leave a quote open — `A="bad<token>` does not
		// parse — so the cut is also offered with a quote closed. Both quote
		// kinds godotenv recognises are tried: it treats `"` and `'` as quotes
		// but a backtick as an ordinary character. The plain cut is tried first
		// and each form is handed to the parser: the repair closes a quote, it
		// does not re-implement quoting.
		for _, tail := range []string{"\n", "\"\n", "'\n"} {
			truncated := make([]byte, 0, end+len(tail))
			truncated = append(truncated, candidate[:end]...)
			truncated = append(truncated, tail...)
			if name, ok := keyHoldingMarker(truncated, mask, token); ok && name != "" {
				return name
			}
		}
	}
	return ""
}

// keyHoldingMarker parses the masked input and returns the single key whose
// value contains marker, plus whether the input parsed at all. A name is ""
// when no value holds the marker, or when more than one does (ambiguous, so
// naming one would be a guess).
func keyHoldingMarker(input []byte, mask, marker string) (string, bool) {
	values, err := godotenv.UnmarshalBytes([]byte(strings.ReplaceAll(string(input), "$", mask)))
	if err != nil {
		return "", false
	}
	name := ""
	for key, value := range values {
		if !strings.Contains(value, marker) {
			continue
		}
		if name != "" {
			return "", true // reached more than one value: ambiguous, so name none
		}
		name = key
	}
	if !validSecretIdentifier(name) {
		return "", true
	}
	return name, true
}

// faultMarker returns a fixed-length random ASCII token, with the given
// prefix, that does not occur in data. It returns "" if no such token could be
// produced in a few attempts.
//
// The length is fixed and the search bounded on purpose. Growing the token
// until it is absent costs quadratic time on hostile input: a permitted file
// containing the prefix followed by a long run of the extension byte makes
// every progressively longer candidate occur, so the loop copies an
// increasingly large string before parsing even begins. A random token of
// constant length is absent with overwhelming probability, and re-rolling a few
// times bounds the work.
//
// The two prefixes differ so that one marker can never be found as a substring
// of the other, which would make a masked dollar look like an injected token.
func faultMarker(data []byte, prefix string) string {
	for attempt := 0; attempt < 8; attempt++ {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return ""
		}
		token := prefix + hex.EncodeToString(buf)
		if !bytes.Contains(data, []byte(token)) {
			return token
		}
	}
	return ""
}

// firstInvalidUTF8Byte returns the offset of the first byte that is not valid
// UTF-8, or -1 when the whole slice is valid.
func firstInvalidUTF8Byte(data []byte) int {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return -1
}

// carriageReturnFault names the variable a fatal carriage return belongs to,
// or "" when none can be tied to the failure.
//
// A bare carriage return is a line ending to godotenv, so it is not itself a
// fault; it explains a failure only when it split a value, letting the text
// after it be read as a new statement. Substituting it must therefore make the
// file parse for the attribution to stand — otherwise the real fault lies
// elsewhere and blaming the carriage return would send the user to the wrong
// line. A CRLF pair is an ordinary line ending and is never considered.
//
// Candidates are pre-filtered because the confirming substitution re-parses the
// whole input. A carriage return in front of a statement the parser accepts on
// its own — an assignment, a comment, or a blank line — is a legal separator and
// cannot have split a value, so it is skipped after a parse of that statement
// alone, never of the file. Because consecutive separators partition the input,
// those fragment parses total linear work, and the whole-file parse is spent
// only on a genuine candidate. A plain "try the first N" bound was tried first
// and rejected: a legitimate file can carry many legal separators before the
// faulty one, and the bound discarded the only candidate that mattered.
func carriageReturnFault(data []byte) string {
	spent := 0
	scanned := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\r' {
			continue
		}
		if i+1 < len(data) && data[i+1] == '\n' {
			i++ // CRLF: an ordinary line ending.
			continue
		}
		end := firstStatementLen(data[i+1:])
		if scanned+end > maxFaultScanBytes {
			return "" // scan budget exhausted: name the file, never a wrong variable
		}
		scanned += end
		observeFaultScanBytes(end)
		if statementStandalone(data[i+1 : i+1+end]) {
			continue // a legal separator: assignment, comment, or blank line
		}
		if spent+len(data) > maxFaultReparseBytes {
			return "" // budget exhausted: name the file, never a wrong variable
		}
		// Charged per attempt rather than per parse: attributeFaultByte does at
		// least one whole-file parse and at most a few, so len(data) per attempt
		// is the natural unit and keeps the budget in bytes of input re-read.
		spent += len(data)
		observeFaultReparseBytes(len(data))
		name := attributeFaultByte(data, i)
		if name != "" {
			return name
		}
	}
	return ""
}

// firstStatementLen returns the length of the first statement in rest, as the
// parser consumes it, or 0 when rest is empty.
//
// The boundary is not simply the next carriage return. To godotenv a CR is
// whitespace while it scans a variable name, so a statement runs to the next
// LF unless an assignment delimiter appears first — and only then does the
// following CR end it, because a CR terminates an unquoted *value*. Getting
// this wrong is what hid a fault: cutting at the first CR turned `tail\rend`
// into `tail\r`, which parses, so the filter called a broken statement legal
// and skipped the carriage return that caused the failure.
func firstStatementLen(rest []byte) int {
	end := len(rest)
	if j := bytes.IndexByte(rest, '\n'); j >= 0 {
		end = j + 1
	}
	line := rest[:end]
	if d := bytes.IndexAny(line, "=:"); d >= 0 {
		if cr := bytes.IndexByte(line[d:], '\r'); cr >= 0 {
			return d + cr + 1
		}
	}
	return end
}

// statementStandalone reports whether statement, already cut to its length by
// firstStatementLen, is accepted by the parser on its own — an assignment, a
// comment, or a blank line. A carriage return in front of such a statement is a
// legal separator and cannot have split a value.
//
// The parser decides whether the statement is well formed, rather than a
// hand-written classifier. Earlier versions read the identifier and delimiter
// themselves, and review rounds each found a class they misjudged: comments and
// blank lines were treated as candidates (so a file full of them was re-parsed
// once per CR, the quadratic cost the filter exists to remove), malformed values
// such as `tail="` were accepted because only the identifier was checked, and a
// statement reaching across a whitespace CR was cut short. Delegating costs one
// parse per CR, but only for statements that are not legal separators — and it
// cannot drift from the grammar the way a copy does.
func statementStandalone(statement []byte) bool {
	// A blank or whitespace-only statement is legal.
	if len(bytes.TrimSpace(statement)) == 0 {
		return true
	}
	_, err := godotenv.UnmarshalBytes(statement)
	return err == nil
}
