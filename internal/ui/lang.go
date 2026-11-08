package ui

import (
	"bytes"
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

// The interface texts live in lang/<code>.json (one file per language, en is the
// reference) so translators never touch the code. The server injects them into
// the page.
//
//go:embed lang/*.json
var langFS embed.FS

const i18nMarker = "/*I18N*/ {}"

var (
	pageOnce sync.Once
	pageHTML []byte
)

// loadLanguages reads every embedded language file.
func loadLanguages() (map[string]map[string]any, error) {
	entries, err := langFS.ReadDir("lang")
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, e := range entries {
		data, err := langFS.ReadFile("lang/" + e.Name())
		if err != nil {
			return nil, err
		}
		var table map[string]any
		if err := json.Unmarshal(data, &table); err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(e.Name(), ".json")] = table
	}
	return out, nil
}

// page returns index.html with the language tables filled in. json.Marshal
// escapes <, > and & so the tables cannot close the <script> tag.
func page() []byte {
	pageOnce.Do(func() {
		langs, err := loadLanguages()
		if err != nil {
			panic("ui: language files: " + err.Error()) // embedded: a build-time mistake
		}
		j, err := json.Marshal(langs)
		if err != nil {
			panic("ui: language files: " + err.Error())
		}
		pageHTML = bytes.Replace(indexHTML, []byte(i18nMarker), j, 1)
	})
	return pageHTML
}
