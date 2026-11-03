package sources

import (
	"runtime"
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
