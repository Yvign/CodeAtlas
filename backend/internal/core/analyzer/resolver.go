package analyzer

import (
	"path"
	"strings"
)

var knownExts = []string{".ts", ".tsx", ".js", ".jsx"}

func resolve(importingFile, rawImport string, pathSet map[string]bool) string {
	// Only resolve relative imports
	if !strings.HasPrefix(rawImport, ".") {
		return ""
	}

	dir := path.Dir(importingFile)
	joined := path.Join(dir, rawImport)

	// 1. Import already has a known extension
	for _, ext := range knownExts {
		if strings.HasSuffix(rawImport, ext) {
			if pathSet[joined] {
				return joined
			}
			return ""
		}
	}

	// 2. Try appending known extensions
	for _, ext := range knownExts {
		candidate := joined + ext
		if pathSet[candidate] {
			return candidate
		}
	}

	// 3. Try /index + known extensions
	for _, ext := range knownExts {
		candidate := path.Join(joined, "index"+ext)
		if pathSet[candidate] {
			return candidate
		}
	}

	return ""
}
