package sources

import (
	"fmt"
	"path"
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
	host, p, _ := strings.Cut(u, "/")
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i] // a port
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	return strings.ToLower(host + "/" + p)
}

// ParseGitmodules reads a registry's .gitmodules. Each submodule's name is an
// alias; url is where to fetch it, branch the branch to follow ("." or none:
// the default branch), and the dsheets key the folder that holds its sheets.
// base is the registry's own clone URL or folder, which relative URLs such as
// "../orders-sheets.git" are resolved against. The commit git records for
// each submodule is not used: references follow the branch.
func ParseGitmodules(text, base string) (map[string]Source, error) {
	out := map[string]Source{}
	name := ""
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") {
			m := sectionLine.FindStringSubmatch(line)
			name = ""
			if m != nil {
				name = m[1]
				out[name] = Source{Alias: name}
			}
			continue
		}
		if name == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf(".gitmodules:%d: expected key = value", n+1)
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		s := out[name]
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "url":
			s.Git = resolveURL(base, value)
		case "branch":
			if value != "." {
				s.Ref = value
			}
		case "dsheets":
			s.Dir = strings.Trim(value, "/")
		}
		out[name] = s
	}
	for n, s := range out {
		if s.Git == "" {
			return nil, fmt.Errorf(".gitmodules: submodule %q has no url", n)
		}
	}
	return out, nil
}

var sectionLine = regexp.MustCompile(`^\[\s*submodule\s+"([^"]+)"\s*\]$`)

// resolveURL resolves a relative submodule URL ("./x", "../x") against the
// registry's URL or folder, as git does: the registry's URL counts as a
// folder, so "../x" is a sibling repository.
func resolveURL(base, u string) string {
	if !strings.HasPrefix(u, "./") && !strings.HasPrefix(u, "../") {
		return u
	}
	base = strings.TrimRight(filepath.ToSlash(base), "/")
	prefix := ""
	if loc := scheme.FindStringIndex(base); loc != nil {
		rest := base[loc[1]:]
		host, p, _ := strings.Cut(rest, "/")
		prefix, base = base[:loc[1]]+host, "/"+p
	} else if m := scpLike.FindStringSubmatch(base); m != nil && !winDrive.MatchString(base) {
		i := strings.Index(base, ":")
		prefix, base = base[:i+1], base[i+1:]
	}
	return prefix + path.Join(base, u)
}
