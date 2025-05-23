package main

import (
	"fmt"
	"testing"
)

func TestParseFile(t *testing.T) {
	type testCase struct {
		have   string
		expect IntroID
	}
	tests := []testCase{
		{
			have:   "Int 1234-2020",
			expect: "1234-2020",
		},
		{
			have:   "Res 1234-2020",
			expect: "res-1234-2020",
		},
		{
			have:   "Int 1234-2020-A",
			expect: "1234-2020",
		},
	}
	for i, tc := range tests {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			got, _ := ParseFile(tc.have)
			if got != tc.expect {
				t.Errorf("ParseFile(%q) = %q, want %q", tc.have, got, tc.expect)
			}
		})
	}
}
