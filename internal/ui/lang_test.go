package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var placeholder = regexp.MustCompile(`\{[a-z]+\}`)

func placeholders(s string) string {
	p := placeholder.FindAllString(s, -1)
	sort.Strings(p)
	return strings.Join(p, ",")
}

func keys(m map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

// Every language has exactly the keys of English, the same {placeholders}, and
// no empty text: a missing translation is a test failure, not a blank in the UI.
func TestLanguageFiles(t *testing.T) {
	langs, err := loadLanguages()
	if err != nil {
		t.Fatal(err)
	}
	en, ok := langs["en"]
	if !ok {
		t.Fatal("lang/en.json (the reference) is missing")
	}
	for _, want := range []string{"fr", "es", "de"} {
		if _, ok := langs[want]; !ok {
			t.Errorf("lang/%s.json is missing", want)
		}
	}
	for code, table := range langs {
		if fmt.Sprint(keys(table)) != fmt.Sprint(keys(en)) {
			t.Errorf("%s: keys differ from en: missing %v, extra %v", code, diff(keys(en), keys(table)), diff(keys(table), keys(en)))
			continue
		}
		for k, v := range table {
			switch x := v.(type) {
			case string:
				if strings.TrimSpace(x) == "" {
					t.Errorf("%s.%s is empty", code, k)
				}
				if placeholders(x) != placeholders(en[k].(string)) {
					t.Errorf("%s.%s: placeholders %q, en has %q", code, k, placeholders(x), placeholders(en[k].(string)))
				}
			case []any: // units
				if len(x) != len(en[k].([]any)) {
					t.Errorf("%s.%s has %d items, en has %d", code, k, len(x), len(en[k].([]any)))
				}
			case map[string]any: // server messages
				if fmt.Sprint(keys(x)) != fmt.Sprint(keys(en[k].(map[string]any))) {
					t.Errorf("%s.%s: messages differ from en", code, k)
				}
				for msg, local := range x {
					if strings.TrimSpace(local.(string)) == "" {
						t.Errorf("%s.%s[%q] is empty", code, k, msg)
					}
				}
			}
		}
	}
	for msg, v := range en["_server"].(map[string]any) {
		if v != msg {
			t.Errorf("en._server[%q] must map to itself", msg)
		}
	}
}

func diff(a, b []string) []string {
	in := map[string]bool{}
	for _, k := range b {
		in[k] = true
	}
	var out []string
	for _, k := range a {
		if !in[k] {
			out = append(out, k)
		}
	}
	return out
}

// Every text the page asks for by name exists.
func TestPageUsesExistingKeys(t *testing.T) {
	langs, _ := loadLanguages()
	en := langs["en"]
	html := string(indexHTML)
	var used []string
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`\bt\('([A-Za-z0-9_.]+)'`),
		regexp.MustCompile(`data-i18n(?:-html|-ph|-title)?="([A-Za-z0-9_.]+)"`),
	} {
		for _, m := range re.FindAllStringSubmatch(html, -1) {
			used = append(used, m[1])
		}
	}
	if len(used) < 50 {
		t.Fatalf("only %d translation keys found in the page: the scan is broken", len(used))
	}
	for _, k := range used {
		if _, ok := en[k]; !ok {
			t.Errorf("the page uses the text %q, which lang/en.json does not have", k)
		}
	}
}

func TestPageIsAssembled(t *testing.T) {
	p := string(page())
	if strings.Contains(p, i18nMarker) {
		t.Fatal("the language marker was not replaced")
	}
	for _, want := range []string{`"navHome"`, `"Tableau de bord"`, `"Panel"`, `"Übersicht"`} {
		if !strings.Contains(p, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	// the tables cannot break out of the <script> element
	if strings.Contains(p[strings.Index(p, "const I18N"):strings.Index(p, "const LANGS")], "</script>") {
		t.Fatal("</script> inside the language tables")
	}
}
