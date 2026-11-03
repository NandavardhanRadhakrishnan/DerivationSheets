package sources

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// SameRepo reports whether two clone URLs name the same repository, however
// they are spelled: https or ssh, with or without user, ".git" or a
// trailing slash.
func SameRepo(a, b string) bool {
	na, nb := repoKey(a), repoKey(b)
	return na != "" && na == nb
}

var (
	scheme   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
	scpLike  = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`) // git@host:path
	winDrive = regexp.MustCompile(`^/?[a-zA-Z]:[/\\]`)
)

// repoKey normalises a clone URL to host/path, or a cleaned local path.
func repoKey(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	local := ""
	switch {
	case strings.HasPrefix(strings.ToLower(u), "file://"):
		local = u[len("file://"):]
	case winDrive.MatchString(u) || strings.HasPrefix(u, "/") || strings.HasPrefix(u, "."):
		local = u
	}
	if local != "" {
		if winDrive.MatchString(local) {
			local = strings.TrimPrefix(local, "/")
		}
		local = strings.TrimSuffix(filepath.Clean(filepath.FromSlash(local)), ".git")
		if runtime.GOOS == "windows" {
			local = strings.ToLower(local)
		}
		return "file:" + local
	}
	if loc := scheme.FindStringIndex(u); loc != nil {
		u = u[loc[1]:]
		if i := strings.Index(u, "/"); i >= 0 {
			if at := strings.LastIndex(u[:i], "@"); at >= 0 {
				u = u[at+1:]
			}
		}
	} else if m := scpLike.FindStringSubmatch(u); m != nil {
		u = m[1] + "/" + m[2]
	}
	host, path, _ := strings.Cut(u, "/")
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i] // a port
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return strings.ToLower(host + "/" + path)
}
