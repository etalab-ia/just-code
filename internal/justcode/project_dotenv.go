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

type ProjectDotenv struct {
	Values     map[string]string
	Duplicates []string
}

// ParseProjectDotenv parses a bounded dotenv file as data. Dollar expansion
// in godotenv is disabled with a fresh sentinel so references remain literal;
// no environment is read and no command is evaluated. Duplicate assignments
// use godotenv's last-definition-wins rule and are reported for preview.
func ParseProjectDotenv(data []byte) (ProjectDotenv, error) {
	if len(data) > MaxProjectDotenvBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return ProjectDotenv{}, fmt.Errorf("dotenv file is too large or contains invalid text")
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
		// The parser's syntax errors include source fragments; do not return
		// them because a malformed line may contain a secret value.
		return ProjectDotenv{}, fmt.Errorf("dotenv syntax is invalid or unsupported")
	}
	duplicates := map[string]bool{}
	seen := map[string]bool{}
	for key := range values {
		if !validSecretIdentifier(key) {
			return ProjectDotenv{}, fmt.Errorf("dotenv contains an unsupported variable name")
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
