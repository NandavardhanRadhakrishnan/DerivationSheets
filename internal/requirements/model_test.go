package requirements

import (
	"regexp"
	"testing"
)

func TestMakeIDStable(t *testing.T) {
	a := MakeID(Compute, "Shipping team", "checkout", "shipping_fee")
	if !regexp.MustCompile(`^R-[0-9a-f]{6}$`).MatchString(a) {
		t.Fatalf("id %q does not look like R-xxxxxx", a)
	}
	if b := MakeID(Compute, "Shipping team", "checkout", "shipping_fee"); a != b {
		t.Errorf("same inputs gave %s and %s", a, b)
	}
	if b := MakeID(Compute, "Shipping team", "checkout", "surcharge"); a == b {
		t.Errorf("different subjects gave the same id %s", a)
	}
}

func TestSortOrder(t *testing.T) {
	rs := []Requirement{
		{Kind: Receive, Team: "B", Subject: "x"},
		{Kind: Send, Team: "A", Subject: "x"},
		{Kind: Carry, Team: "A", Subject: "x"},
		{Kind: Compute, Team: "A", Subject: "x"},
		{Kind: Schema, Team: "A", Subject: "x"},
	}
	Sort(rs)
	want := []Kind{Schema, Compute, Carry, Send, Receive}
	for i, k := range want {
		if rs[i].Kind != k {
			t.Fatalf("position %d is %s, want %s", i, rs[i].Kind, k)
		}
	}
}
