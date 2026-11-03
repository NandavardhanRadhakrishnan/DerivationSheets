package sources

import (
	"runtime"
	"strings"
	"testing"
)

func TestSameRepo(t *testing.T) {
	same := [][2]string{
		{"https://github.com/example/shipping.git", "git@github.com:example/shipping"},
		{"https://GitHub.com/example/shipping/", "ssh://git@github.com/example/shipping.git"},
		{"https://x-access-token:abc@github.com/example/shipping", "https://github.com/example/shipping"},
		{"file:///tmp/x/repo", "file:///tmp/x/repo/"},
	}
	if runtime.GOOS == "windows" {
		same = append(same, [2]string{"file:///D:/x/repo", "file://d:/x/repo/"})
	}
	for _, p := range same {
		if !SameRepo(p[0], p[1]) {
			t.Errorf("SameRepo(%q, %q) = false", p[0], p[1])
		}
	}
	different := [][2]string{
		{"https://github.com/example/shipping.git", "https://github.com/example/shipping-service.git"},
		{"https://github.com/example/shipping", "https://gitlab.com/example/shipping"},
		{"", ""},
	}
	for _, p := range different {
		if SameRepo(p[0], p[1]) {
			t.Errorf("SameRepo(%q, %q) = true", p[0], p[1])
		}
	}
}

func TestParseGitmodules(t *testing.T) {
	text := `# teams
[submodule "shipping"]
    path = shipping
    url = https://github.com/example/shipping-service.git
    branch = main
    dsheets = sheets
[submodule "orders"]
	path = teams/orders
	URL = ../orders-sheets.git
	branch = .
`
	got, err := ParseGitmodules(text, "https://github.com/example/dsheets-registry.git")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Source{
		"shipping": {Alias: "shipping", Git: "https://github.com/example/shipping-service.git", Ref: "main", Dir: "sheets"},
		"orders":   {Alias: "orders", Git: "https://github.com/example/orders-sheets.git"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries: %v", len(got), got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %+v; want %+v", k, got[k], w)
		}
	}
}

func TestParseGitmodulesRelativeToFolder(t *testing.T) {
	got, err := ParseGitmodules("[submodule \"x\"]\n\turl = ../x\n", "/repos/registry")
	if err != nil {
		t.Fatal(err)
	}
	if got["x"].Git != "/repos/x" {
		t.Errorf("relative url = %q; want /repos/x", got["x"].Git)
	}
}

func TestParseGitmodulesErrors(t *testing.T) {
	if _, err := ParseGitmodules("[submodule \"shipping\"]\n\tpath = shipping\n", ""); err == nil || !strings.Contains(err.Error(), "shipping") {
		t.Errorf("missing url error = %v", err)
	}
}
