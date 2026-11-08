// Package checkout finds the repository checkout the installer operates on.
package checkout

import (
	"errors"
	"os"
	"path/filepath"
)

// Find walks up from the working directory, then from the program's own location
// (a window opened from Finder starts in "/"), to the first directory that holds
// platform.yaml next to an installer/ folder. ADAETUM_ROOT overrides the search.
func Find() (string, error) {
	if root := os.Getenv("ADAETUM_ROOT"); root != "" {
		if isCheckout(root) {
			return root, nil
		}
		return "", errors.New("ADAETUM_ROOT is not an Adaetum checkout: " + root)
	}
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, dir := range starts {
		for {
			if isCheckout(dir) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", errors.New("could not find the Adaetum checkout; run the installer from inside it or set ADAETUM_ROOT")
}

func isCheckout(dir string) bool {
	_, a := os.Stat(filepath.Join(dir, "platform.yaml"))
	info, b := os.Stat(filepath.Join(dir, "installer"))
	return a == nil && b == nil && info.IsDir()
}
