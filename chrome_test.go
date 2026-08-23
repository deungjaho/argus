package main

import (
	"testing"
)

func TestChromeHeadlessArgs(t *testing.T) {
	// chromeHeadlessArgs trims and lowercases the env value, returning
	// --headless=new only for "1" or "true". Empty/0/false/no yield no args.
	cases := []struct {
		name string
		env  string // value to set via t.Setenv
		want int    // number of args returned
	}{
		{name: "empty", env: "", want: 0},
		{name: "zero", env: "0", want: 0},
		{name: "false", env: "false", want: 0},
		{name: "false_upper", env: "FALSE", want: 0},
		{name: "no", env: "no", want: 0},
		{name: "one", env: "1", want: 1},
		{name: "true", env: "true", want: 1},
		{name: "true_upper", env: "TRUE", want: 1},
		{name: "true_spaces", env: "  true  ", want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARGUS_CHROME_HEADLESS", tc.env)
			got := chromeHeadlessArgs()
			if len(got) != tc.want {
				t.Fatalf("len = %d, want %d (env=%q)", len(got), tc.want, tc.env)
			}
			if tc.want == 1 && got[0] != "--headless=new" {
				t.Errorf("arg = %q, want --headless=new", got[0])
			}
		})
	}
}
