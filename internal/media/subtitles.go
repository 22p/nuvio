package media

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var langNames = map[string]string{
	"zh": "Chinese", "zh-cn": "Chinese (Simplified)", "zh-hans": "Chinese (Simplified)", "zh-sg": "Chinese (Simplified)",
	"zh-tw": "Chinese (Traditional)", "zh-hk": "Chinese (Traditional)", "zh-hant": "Chinese (Traditional)",
	"en": "English", "ja": "Japanese", "ko": "Korean", "fr": "French",
	"de": "German", "es": "Spanish", "it": "Italian", "ru": "Russian",
	"pt": "Portuguese", "pt-br": "Portuguese (Brazil)", "ar": "Arabic",
	"hi": "Hindi", "th": "Thai", "vi": "Vietnamese", "und": "Unknown",
}

// LanguageName returns a human readable label for a language code.
func LanguageName(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if n, ok := langNames[c]; ok {
		return n
	}
	if i := strings.IndexAny(c, "-_"); i > 0 {
		if n, ok := langNames[c[:i]]; ok {
			return n
		}
	}
	if c == "" {
		return "Unknown"
	}
	return c
}

var langToken = regexp.MustCompile(`(?i)(^|[ ._\-])(zh[-_]?(?:cn|sg|tw|hk|hans|hant)?|en|ja|jp|ko|kr|fr|de|es|it|ru|pt[-_]?br|pt|ar|hi|th|vi|und)([ ._\-]|$)`)

// DetectSubtitleLanguage extracts a language code from a sidecar filename.
func DetectSubtitleLanguage(filename, base string) string {
	stem := strings.TrimSuffix(filename, filepathExt(filename))
	stem = strings.TrimPrefix(stem, base)
	stem = strings.Trim(stem, " ._-")
	stem = strings.ReplaceAll(stem, "_", "-")
	if stem == "" {
		return "und"
	}
	lower := strings.ToLower(stem)
	if _, ok := langNames[lower]; ok {
		return lower
	}
	// Prefer the beginning of the suffix ("en.forced" -> "en").
	first := strings.FieldsFunc(lower, func(r rune) bool { return r == '.' || r == '-' })
	if len(first) > 0 {
		if _, ok := langNames[first[0]]; ok {
			return first[0]
		}
	}
	if m := langToken.FindStringSubmatch(" " + lower + " "); len(m) > 1 {
		return strings.ToLower(strings.ReplaceAll(m[2], "_", "-"))
	}
	return "und"
}

func filepathExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

func subtitleCodec(ext string) string {
	switch strings.ToLower(ext) {
	case ".vtt":
		return "vtt"
	case ".ass", ".ssa":
		return "ass"
	default:
		return "srt"
	}
}

// SubtitleToVTT loads a sidecar subtitle file and converts it to WebVTT.
func SubtitleToVTT(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw = stripBOM(raw)
	switch strings.ToLower(filepathExt(path)) {
	case ".vtt":
		if !strings.HasPrefix(strings.TrimSpace(string(raw)), "WEBVTT") {
			return append([]byte("WEBVTT\n\n"), raw...), nil
		}
		return raw, nil
	case ".ass", ".ssa":
		return ASSBytesToVTT(raw), nil
	default:
		return SRTBytesToVTT(raw), nil
	}
}

func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

var srtTime = regexp.MustCompile(`(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})`)

// SRTBytesToVTT converts SubRip to WebVTT.
func SRTBytesToVTT(raw []byte) []byte {
	text := strings.ReplaceAll(string(stripBOM(raw)), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	blocks := strings.Split(strings.TrimSpace(text), "\n\n")
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 {
			continue
		}
		// Optional numeric counter line.
		if _, err := strconv.Atoi(strings.TrimSpace(lines[0])); err == nil {
			lines = lines[1:]
			if len(lines) == 0 {
				continue
			}
		}
		if !strings.Contains(lines[0], "-->") {
			continue
		}
		stamp := strings.ReplaceAll(lines[0], ",", ".")
		// Drop SRT positioning info after the end timestamp.
		if i := strings.Index(stamp, "-->"); i >= 0 {
			rest := stamp[i+3:]
			parts := strings.SplitN(strings.TrimSpace(rest), " ", 2)
			if len(parts) == 2 {
				if t, ok := parseSRTTime(parts[0]); ok {
					stamp = strings.TrimSpace(stamp[:i+3]) + " " + t
				}
			}
		}
		body := strings.Join(lines[1:], "\n")
		body = strings.ReplaceAll(body, "&", "&amp;")
		body = strings.ReplaceAll(body, "<", "&lt;")
		body = strings.ReplaceAll(body, ">", "&gt;")
		out.WriteString(stamp)
		out.WriteString("\n")
		out.WriteString(body)
		out.WriteString("\n\n")
	}
	return []byte(out.String())
}

func parseSRTTime(s string) (string, bool) {
	m := srtTime.FindStringSubmatch(s)
	if len(m) != 5 {
		return "", false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	se, _ := strconv.Atoi(m[3])
	ms := m[4]
	for len(ms) < 3 {
		ms += "0"
	}
	return fmt.Sprintf("%02d:%02d:%02d.%s", h, mi, se, ms), true
}

var assTime = regexp.MustCompile(`^\s*(\d+):(\d{2}):(\d{2})[.:](\d{1,3})\s*$`)
var assTag = regexp.MustCompile(`\{[^}]*\}`)

var (
	assPosRe   = regexp.MustCompile(`\\pos\(\s*(-?[0-9.]+)\s*,\s*(-?[0-9.]+)\s*\)`)
	assMoveRe  = regexp.MustCompile(`\\move\(\s*(-?[0-9.]+)\s*,\s*(-?[0-9.]+)`)
	assAlignRe = regexp.MustCompile(`\\an([1-9])`)
	assDrawRe  = regexp.MustCompile(`\\p[1-9]`)
)

type assStyle struct {
	alignment                 int
	marginL, marginR, marginV float64
}

// ASSBytesToVTT converts an ASS/SSA script to WebVTT. Styling that WebVTT can
// express is kept: alignment and position are mapped to cue settings so signs
// stay where the typesetter put them instead of collapsing to the bottom
// centre. Karaoke and drawing commands cannot be represented and are dropped.
func ASSBytesToVTT(raw []byte) []byte {
	text := strings.ReplaceAll(string(stripBOM(raw)), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	playW, playH := 384.0, 288.0
	section := ""
	styles := map[string]assStyle{}
	var styleFormat, eventFormat []string
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			section = strings.ToLower(strings.Trim(trimmed, "[]"))
			continue
		}
		lower := strings.ToLower(trimmed)
		switch section {
		case "script info":
			switch {
			case strings.HasPrefix(lower, "playresx:"):
				playW = parseFloatDefault(trimmed[len("playresx:"):], playW)
			case strings.HasPrefix(lower, "playresy:"):
				playH = parseFloatDefault(trimmed[len("playresy:"):], playH)
			}
		case "v4+ styles", "v4 styles":
			if strings.HasPrefix(lower, "format:") {
				styleFormat = splitCSV(trimmed[len("format:"):])
				continue
			}
			if !strings.HasPrefix(lower, "style:") {
				continue
			}
			fields := splitCSVLimit(trimmed[len("style:"):], len(styleFormat))
			field := assField(styleFormat, fields)
			styles[field("Name")] = assStyle{
				alignment: atoiDefault(field("Alignment"), 2),
				marginL:   parseFloatDefault(field("MarginL"), 10),
				marginR:   parseFloatDefault(field("MarginR"), 10),
				marginV:   parseFloatDefault(field("MarginV"), 10),
			}
		case "events":
			if strings.HasPrefix(lower, "format:") {
				eventFormat = splitCSV(trimmed[len("format:"):])
				continue
			}
			if !strings.HasPrefix(lower, "dialogue:") {
				continue
			}
			if cue, ok := assCueVTT(trimmed[len("dialogue:"):], eventFormat, styles, playW, playH); ok {
				out.WriteString(cue)
			}
		}
	}
	return []byte(out.String())
}

func assField(format, fields []string) func(string) string {
	return func(name string) string {
		for i, f := range format {
			if strings.EqualFold(strings.TrimSpace(f), name) && i < len(fields) {
				return strings.TrimSpace(fields[i])
			}
		}
		return ""
	}
}

func assCueVTT(raw string, format []string, styles map[string]assStyle, playW, playH float64) (string, bool) {
	if len(format) == 0 {
		format = []string{"Layer", "Start", "End", "Style", "Name", "MarginL", "MarginR", "MarginV", "Effect", "Text"}
	}
	field := assField(format, splitCSVLimit(raw, len(format)))
	start, ok1 := parseASSTime(field("Start"))
	end, ok2 := parseASSTime(field("End"))
	if !ok1 || !ok2 || end <= start {
		return "", false
	}
	body := field("Text")
	if body == "" || assDrawRe.MatchString(body) {
		// Vector drawings cannot be rendered as text.
		return "", false
	}

	st := styles[field("Style")]
	if st.alignment == 0 {
		st = assStyle{alignment: 2, marginL: 10, marginR: 10, marginV: 10}
	}
	align := st.alignment
	if m := assAlignRe.FindStringSubmatch(body); m != nil {
		align = int(m[1][0] - '0')
	}
	ml, mr, mv := st.marginL, st.marginR, st.marginV
	if v := parseFloatDefault(field("MarginL"), 0); v > 0 {
		ml = v
	}
	if v := parseFloatDefault(field("MarginR"), 0); v > 0 {
		mr = v
	}
	if v := parseFloatDefault(field("MarginV"), 0); v > 0 {
		mv = v
	}

	x, y, positioned := 0.0, 0.0, false
	if m := assPosRe.FindStringSubmatch(body); m != nil {
		x, y, positioned = parseFloatDefault(m[1], 0), parseFloatDefault(m[2], 0), true
	} else if m := assMoveRe.FindStringSubmatch(body); m != nil {
		x, y, positioned = parseFloatDefault(m[1], 0), parseFloatDefault(m[2], 0), true
	}
	col := (align - 1) % 3
	row := (align - 1) / 3
	if !positioned {
		switch col {
		case 0:
			x = ml
		case 1:
			x = playW / 2
		case 2:
			x = playW - mr
		}
		switch row {
		case 0:
			y = playH - mv
		case 1:
			y = playH / 2
		default:
			y = mv
		}
	}

	body = strings.ReplaceAll(body, `\N`, "\n")
	body = strings.ReplaceAll(body, `\n`, "\n")
	body = strings.ReplaceAll(body, `\h`, " ")
	body = assTag.ReplaceAllString(body, "")
	body = strings.TrimSpace(body)
	if body == "" {
		return "", false
	}
	body = strings.ReplaceAll(body, "&", "&amp;")
	body = strings.ReplaceAll(body, "<", "&lt;")
	body = strings.ReplaceAll(body, ">", "&gt;")

	alignName := "center"
	switch col {
	case 0:
		alignName = "start"
	case 2:
		alignName = "end"
	}
	// The anchor point the ASS alignment refers to: bottom rows sit above the
	// line, top rows below it, middle rows are centred.
	lineAlign := "center"
	switch row {
	case 0:
		lineAlign = "end"
	case 2:
		lineAlign = "start"
	}
	return fmt.Sprintf("%s --> %s line:%.1f%% line-align:%s position:%.1f%% align:%s\n%s\n\n",
		vttTime(start), vttTime(end), clampPercent(y/playH*100), lineAlign, clampPercent(x/playW*100), alignName, body), true
}

func parseFloatDefault(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return v
	}
	return def
}

func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func splitCSVLimit(s string, n int) []string {
	if n <= 0 {
		return splitCSV(s)
	}
	parts := strings.SplitN(s, ",", n)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func parseASSTime(s string) (time.Duration, bool) {
	m := assTime.FindStringSubmatch(s)
	if len(m) != 5 {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	se, _ := strconv.Atoi(m[3])
	ms := m[4]
	for len(ms) < 3 {
		ms += "0"
	}
	cs, _ := strconv.Atoi(ms)
	return time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute +
		time.Duration(se)*time.Second + time.Duration(cs)*time.Millisecond, true
}

func vttTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d / time.Millisecond)
	ms := total % 1000
	total /= 1000
	se := total % 60
	total /= 60
	mi := total % 60
	h := total / 60
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, mi, se, ms)
}

// ValidUTF8 helps the subtitle handler pick an encoding fallback.
func ValidUTF8(b []byte) bool { return utf8.Valid(b) }
