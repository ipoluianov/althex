package forms

import "testing"

// Every language has all the texts: a new field in Strings without a
// translation fails here instead of silently showing English
func TestAllTranslated(t *testing.T) {
	for lang, fields := range catalog.Missing() {
		t.Errorf("%s: not translated: %v", lang, fields)
	}
}

// Every translation is offered in the settings
func TestLanguagesOffered(t *testing.T) {
	offered := make(map[string]bool)
	for _, l := range languages {
		offered[l.tag] = true
	}
	for _, lang := range catalog.Languages() {
		if !offered[lang] {
			t.Errorf("%s: not in the languages list", lang)
		}
	}
}
