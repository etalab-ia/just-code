package main

import (
	"errors"
	"testing"
)

// The huh form itself needs a TTY, so the regression tests pin the decision
// logic around it: which fields the form must omit so a hidden or failed
// input can never clobber an existing project selection.

func TestShouldSkipSkillsField(t *testing.T) {
	cases := []struct {
		name         string
		skillsFlag   bool
		catalogueErr error
		want         bool
	}{
		{"flag supplied", true, nil, true},
		{"catalogue ok", false, nil, false},
		{"catalogue failed", false, errors.New("offline"), true},
		{"flag supplied even on catalogue failure", true, errors.New("offline"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := initOptions{Set: map[string]bool{"skills": tc.skillsFlag}}
			if got := shouldSkipSkillsField(opts, tc.catalogueErr); got != tc.want {
				t.Fatalf("shouldSkipSkillsField = %v, want %v", got, tc.want)
			}
		})
	}
}
