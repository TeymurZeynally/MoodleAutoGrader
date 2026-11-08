package work

import (
	"html"
	"regexp"
	"strings"
)

var (
	htmlTag   = regexp.MustCompile(`<[^>]*>`)
	blockEnd  = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|ul|ol|table|pre|blockquote)>|<br\s*/?>`)
	listItem  = regexp.MustCompile(`(?i)<li[^>]*>`)
	heading   = regexp.MustCompile(`(?i)<h[1-6][^>]*>`)
	manyLines = regexp.MustCompile(`\n[ \t]*\n([ \t]*\n)+`)
	spaces    = regexp.MustCompile(`[ \t]+`)
)

// DescriptionText turns the Moodle assignment description (HTML) into readable text: paragraphs, list items and
// headings stay on their own lines. Shown to the teacher; never rendered as HTML.
func DescriptionText(s string) string {
	s = heading.ReplaceAllString(s, "\n")
	s = listItem.ReplaceAllString(s, "\n• ")
	s = blockEnd.ReplaceAllString(s, "\n")
	s = htmlTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaces.ReplaceAllString(l, " "))
	}

	return strings.TrimSpace(manyLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
