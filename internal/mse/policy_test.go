package mse

import "testing"

func TestParsePolicyRoundTripsThroughString(t *testing.T) {
	for _, p := range []Policy{PolicyDisabled, PolicyPrefer, PolicyRequired} {
		got, err := ParsePolicy(p.String())
		if err != nil {
			t.Fatalf("ParsePolicy(%q): %v", p.String(), err)
		}
		if got != p {
			t.Fatalf("ParsePolicy(%q) = %v, want %v", p.String(), got, p)
		}
	}
}

func TestParsePolicyEmptyStringIsDisabled(t *testing.T) {
	got, err := ParsePolicy("")
	if err != nil {
		t.Fatalf("ParsePolicy(\"\"): %v", err)
	}
	if got != PolicyDisabled {
		t.Fatalf("ParsePolicy(\"\") = %v, want PolicyDisabled", got)
	}
}

func TestParsePolicyRejectsGarbage(t *testing.T) {
	if _, err := ParsePolicy("yes-please"); err == nil {
		t.Fatal("ParsePolicy accepted an unknown value")
	}
}
