package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// The BSD licences of some SVD files ask for their notices to be
// reproduced with binary distributions; they are in the comments at the
// top of the files and in <licenseText>. NOTICES.txt collects them, each
// text once with the devices it came with.

var (
	xmlComment  = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	licenseText = regexp.MustCompile(`(?s)<licenseText>(.*?)</licenseText>`)
)

// notice is the licence text of a source file; "" when it has none
func notice(data []byte) string {
	var parts []string
	// The comments before the root element
	head := data
	if i := bytes.Index(data, []byte("<device")); i > 0 {
		head = data[:i]
	}
	for _, m := range xmlComment.FindAllSubmatch(head, -1) {
		if t := cleanNotice(string(m[1])); looksLikeLicence(t) {
			parts = append(parts, t)
		}
	}
	if m := licenseText.FindSubmatch(data); m != nil {
		t := strings.NewReplacer(`\n`, "\n", "&quot;", `"`, "&apos;", "'", "&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(string(m[1]))
		if t = cleanNotice(t); looksLikeLicence(t) {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

func looksLikeLicence(t string) bool {
	l := strings.ToLower(t)
	return strings.Contains(l, "copyright") || strings.Contains(l, "licen")
}

// cleanNotice drops the comment decoration: leading " * ", rulers of stars
func cleanNotice(s string) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		line = strings.TrimRight(line, " \t")
		t := strings.TrimLeft(line, " \t")
		t = strings.TrimPrefix(t, "*")
		t = strings.TrimPrefix(t, " ")
		if strings.Trim(t, "*=-/ \t") == "" && t != "" {
			continue // a ruler
		}
		lines = append(lines, strings.TrimRight(t, " \t*"))
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out
}

// writeNotices writes the notices, each with the devices it covers
func writeNotices(path string, byText map[string][]string) error {
	texts := make([]string, 0, len(byText))
	for t := range byText {
		texts = append(texts, t)
	}
	sort.Slice(texts, func(i, j int) bool { return byText[texts[i]][0] < byText[texts[j]][0] })
	var b strings.Builder
	b.WriteString("Notices of the register descriptions (CMSIS-SVD) the peripheral database is made of.\n")
	b.WriteString("Their licences ask for these notices to be reproduced with binary distributions.\n")
	for _, t := range texts {
		devs := byText[t]
		sort.Strings(devs)
		fmt.Fprintf(&b, "\n%s\n%s\n\n%s\n", strings.Repeat("-", 78), strings.Join(devs, ", "), t)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
