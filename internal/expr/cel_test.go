package expr

import (
	"reflect"
	"testing"
	"time"
)

func TestCELEval(t *testing.T) {
	e, err := NewCEL([]string{"base_premium", "loading", "net_premium", "gst_rate", "age", "dob", "request_time", "has_ped"})
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := time.Parse(time.RFC3339, "2026-11-02T10:00:00+05:30")
	dob := time.Date(1980, 4, 12, 0, 0, 0, 0, time.UTC)
	vars := map[string]any{
		"base_premium": 15800.0, "loading": 0.35, "net_premium": 21330.0, "gst_rate": 0.18,
		"age": 46.0, "dob": dob, "request_time": rt, "has_ped": true,
	}
	ctx := Context{RequestTime: rt, HasRequestTime: true}
	cases := []struct {
		src  string
		want any
	}{
		{"round(base_premium * (1.0 + loading))", 21330.0},
		{"round(net_premium * gst_rate)", 3839.0},
		{"years_between(dob, request_time)", 46.0},
		{"age > 65", false},
		{"age >= 46", true},
		{"base_premium * 2", 31600.0},
		{"2 + loading", 2.35},
		{"base_premium - 800", 15000.0},
		{`"a1" + "2" == "a12"`, true},
		{"has_ped == true", true},
		{"years_between(dob, today())", 46.0},
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
	got, err := e.Identifiers("round(net_premium * gst_rate) + [1,2].filter(x, x > age).size()")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"net_premium", "gst_rate", "age"}
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
