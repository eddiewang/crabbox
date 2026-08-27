package runnerfs

import "path/filepath"

// Resolve existing parents before cleaning: alias/.. names the physical parent
// of the alias target. Keep the final component untouched for Lstat's link check.
func resolvePathParent(name string) (string, error) {
	volume := filepath.VolumeName(name)
	for len(name) > len(volume)+1 && (name[len(name)-1] == '/' || filepath.Separator == '\\' && name[len(name)-1] == '\\') {
		name = name[:len(name)-1]
	}
	parent, leaf := filepath.Split(name)
	if parent == "" {
		parent = "."
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, leaf), nil
}
