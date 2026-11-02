package expr

import (
	"reflect"
	"testing"
	"time"
)

func TestCELEval(t *testing.T) {
	e, err := NewCEL([]string{"subtotal", "discount_rate", "net_amount", "tax_rate", "years", "signup_date", "request_time", "is_member"})
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := time.Parse(time.RFC3339, "2026-11-02T10:00:00+05:30")
	signup := time.Date(2020, 3, 15, 0, 0, 0, 0, time.UTC)
	vars := map[string]any{
		"subtotal": 80.0, "discount_rate": 0.1, "net_amount": 72.0, "tax_rate": 0.08,
		"years": 6.0, "signup_date": signup, "request_time": rt, "is_member": true,
	}
	ctx := Context{RequestTime: rt, HasRequestTime: true}
	cases := []struct {
		src  string
		want any
	}{
		{"round(subtotal * (1.0 - discount_rate))", 72.0},
		{"round(net_amount * tax_rate)", 6.0},
		{"years_between(signup_date, request_time)", 6.0},
		{"years > 10", false},
		{"years >= 6", true},
		{"subtotal * 2", 160.0},
		{"2 + discount_rate", 2.1},
		{"subtotal - 8", 72.0},
		{`"a1" + "2" == "a12"`, true},
		{"is_member == true", true},
		{"years_between(signup_date, today())", 6.0},
	}
	for _, c := range cases {
		p, err := e.Compile(c.src)
		if err != nil {
			t.Fatalf("%s: compile: %v", c.src, err)
		}
		got, err := p.Eval(vars, ctx)
		if err != nil {
			t.Fatalf("%s: eval: %v", c.src, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %v (%T), want %v", c.src, got, got, c.want)
		}
	}
}

func TestCELIdentifiers(t *testing.T) {
	e, _ := NewCEL(nil)
	got, err := e.Identifiers("round(net_amount * tax_rate) + [1,2].filter(x, x > years).size()")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"net_amount", "tax_rate", "years"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if _, err := e.Identifiers("a +"); err == nil {
		t.Error("expected syntax error")
	}
}

func TestYearsBetween(t *testing.T) {
	d := func(s string) time.Time { x, _ := time.Parse("2006-01-02", s); return x }
	if got := YearsBetween(d("1980-04-12"), d("2026-04-11")); got != 45 {
		t.Errorf("day before birthday: %d", got)
	}
	if got := YearsBetween(d("1980-04-12"), d("2026-04-12")); got != 46 {
		t.Errorf("on birthday: %d", got)
	}
}
