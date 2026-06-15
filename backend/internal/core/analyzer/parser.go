package analyzer

import (
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const importQuery = `
(import_statement source: (string (string_fragment) @import))
(export_statement source: (string (string_fragment) @import))
`

// ParseFile parses the file content and returns raw import path strings
// (quotes already stripped). filePath is used for language detection only.
func ParseFile(content, filePath string) []string {
	bt, err := grammars.ParseFilePooled(filePath, []byte(content))
	if err != nil {
		return nil
	}
	defer bt.Release()

	lang := bt.Language()
	if lang == nil {
		return nil
	}

	q, err := gotreesitter.NewQuery(importQuery, lang)
	if err != nil {
		return nil
	}

	matches := q.ExecuteNode(bt.RootNode(), lang, bt.Source())

	var imports []string
	for _, m := range matches {
		for _, cap := range m.Captures {
			if cap.Name == "import" {
				text := cap.Text(bt.Source())
				if text != "" {
					imports = append(imports, text)
				}
			}
		}
	}
	return imports
}
