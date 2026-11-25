package main

import "testing"

func TestSatisfies(t *testing.T) {
	cases := []struct {
		constraint string
		version    string
		want       bool
	}{
		{">=8.0.2", "7.4.32", false},
		{">=8.0.2", "8.1.0", true},
		{"^8.1", "8.3.4", true},
		{"^8.1", "9.0.0", false},
		{"^8.1", "8.0.30", false},
		{"^7.3|^8.0", "8.2.1", true},
		{"^7.3 || ^8.0", "7.2.0", false},
		{">=7.4 <8.3", "8.2.9", true},
		{">=7.4 <8.3", "8.3.0", false},
		{">=7.4,<8.3", "8.3.0", false},
		{">= 8.1", "8.1.0", true},
		{"~8.1.0", "8.1.9", true},
		{"~8.1.0", "8.2.0", false},
		{"~8.1", "8.9.0", true},
		{"~8.1", "9.0.0", false},
		{"8.2.*", "8.2.7", true},
		{"8.2.*", "8.3.0", false},
		{"*", "5.6.0", true},
		{"^0.3", "0.3.9", true},
		{"^0.3", "0.4.0", false},
		{"1.0 - 2.0", "2.0.5", true},
		{"1.0 - 2.0", "2.1.0", false},
		{"<=8.2", "8.2.30", true},
		{"!=8.1.2", "8.1.2", false},
		{"^18.0.0 || ^20.0.0 || >=22.0.0", "16.20.2", false},
		{"^18.0.0 || ^20.0.0 || >=22.0.0", "20.11.1", true},
		{"^18.0.0 || ^20.0.0 || >=22.0.0", "21.0.0", false},
		{">=18", "v22.1.0", true},
		{"20", "20.3.1", true},
		{"^8.2@dev", "8.2.0", true},
	}
	for _, c := range cases {
		v, ok := parseVersion(c.version)
		if !ok {
			t.Fatalf("bad version %q", c.version)
		}
		if got := satisfies(c.constraint, v); got != c.want {
			t.Errorf("satisfies(%q, %s) = %v, want %v", c.constraint, c.version, got, c.want)
		}
	}
}
