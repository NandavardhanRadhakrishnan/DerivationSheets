package render

import "path/filepath"

func filepathRel(base, p string) (string, error) {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(r), nil
}
