package assessment

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	links     = regexp.MustCompile(`(?i)\b(https?://|www\.)\S+`)
	tags      = regexp.MustCompile(`<[^>]*>`)
	invisible = regexp.MustCompile(`[\x{200B}-\x{200F}\x{2060}-\x{2064}\x{FEFF}\x{E0000}-\x{E007F}]`)
)

// CleanComment removes HTML, links and invisible characters, turns long dashes into hyphens and limits the length
// .
func CleanComment(s string) string {
	s = invisible.ReplaceAllString(tags.ReplaceAllString(links.ReplaceAllString(s, ""), ""), "")
	s = strings.NewReplacer("—", "-", "–", "-", "−", "-").Replace(s)
	return clip(strings.TrimSpace(s), 1500)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	for n > 0 && !utf8Start(s[n]) {
		n--
	}

	return s[:n] + " …"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// Late reports whether a work was uploaded after the deadline.
func Late(submitted, due *time.Time) bool {
	return submitted != nil && due != nil && submitted.After(*due)
}

// WithLateNotice adds to the student's comment that points were taken off for the deadline, when they were. The
// model never sees the penalty, so the notice is added here. notice is the course's text; {points} in it becomes the
// points in Russian, e.g. "1 балл", "0,5 балла".
func WithLateNotice(comment string, penalty float64, notice string) string {
	if penalty <= 0 || strings.TrimSpace(notice) == "" {
		return comment
	}

	points := strings.Replace(strconv.FormatFloat(penalty, 'f', -1, 64), ".", ",", 1) + " " + pointsWord(penalty)
	notice = strings.ReplaceAll(strings.TrimSpace(notice), "{points}", points)
	if strings.Contains(comment, notice) {
		return comment
	}

	return strings.TrimSpace(comment + "\n\n" + notice)
}

// pointsWord is "балл" in the form that goes with n.
func pointsWord(n float64) string {
	if n != math.Trunc(n) {
		return "балла"
	}

	i := int64(n)
	switch {
	case i%10 == 1 && i%100 != 11:
		return "балл"
	case i%10 >= 2 && i%10 <= 4 && (i%100 < 12 || i%100 > 14):
		return "балла"
	default:
		return "баллов"
	}
}
