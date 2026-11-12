package media

import (
	"fmt"
	"os"
	"regexp"
	"sort"
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
	"pl": "Polish", "nl": "Dutch", "sv": "Swedish", "no": "Norwegian",
	"da": "Danish", "fi": "Finnish", "tr": "Turkish", "he": "Hebrew",
	"el": "Greek", "cs": "Czech", "hu": "Hungarian", "ro": "Romanian",
	"uk": "Ukrainian", "id": "Indonesian", "ms": "Malay", "ta": "Tamil",
	"te": "Telugu", "bn": "Bengali", "ur": "Urdu", "fa": "Persian",
	"sr": "Serbian", "hr": "Croatian", "sk": "Slovak", "bg": "Bulgarian",
	"ca": "Catalan", "eu": "Basque", "gl": "Galician", "is": "Icelandic",
	"cy": "Welsh", "et": "Estonian", "lv": "Latvian", "lt": "Lithuanian",
	"sl": "Slovenian", "sq": "Albanian", "mk": "Macedonian", "ka": "Georgian",
	"hy": "Armenian", "az": "Azerbaijani", "kk": "Kazakh", "uz": "Uzbek",
	"mn": "Mongolian", "ne": "Nepali", "si": "Sinhala", "km": "Khmer",
	"lo": "Lao", "my": "Burmese", "tl": "Filipino", "sw": "Swahili",
	"af": "Afrikaans", "la": "Latin", "eo": "Esperanto", "yue": "Cantonese",
}

// langAliases maps ISO 639-2/B and 639-2/T codes (used by ffprobe and many
// release groups) onto the two letter codes used internally. Fansub suffixes
// such as "chi" and "chs" are included.
var langAliases = map[string]string{
	"chi": "zh", "zho": "zh", "chs": "zh-cn", "cht": "zh-tw",
	"eng": "en", "jpn": "ja", "kor": "ko",
	"fre": "fr", "fra": "fr", "ger": "de", "deu": "de",
	"spa": "es", "ita": "it", "rus": "ru", "por": "pt",
	"ara": "ar", "hin": "hi", "tha": "th", "vie": "vi",
	"pol": "pl", "dut": "nl", "nld": "nl", "swe": "sv",
	"nor": "no", "nob": "no", "nno": "no", "dan": "da",
	"fin": "fi", "tur": "tr", "heb": "he", "gre": "el", "ell": "el",
	"cze": "cs", "ces": "cs", "hun": "hu", "rum": "ro", "ron": "ro",
	"ukr": "uk", "ind": "id", "may": "ms", "msa": "ms",
	"tam": "ta", "tel": "te", "ben": "bn", "urd": "ur",
	"per": "fa", "fas": "fa", "srp": "sr", "hrv": "hr",
	"slo": "sk", "slk": "sk", "bul": "bg", "cat": "ca",
	"baq": "eu", "eus": "eu", "glg": "gl", "ice": "is", "isl": "is",
	"wel": "cy", "cym": "cy", "est": "et", "lav": "lv", "lit": "lt",
	"slv": "sl", "alb": "sq", "sqi": "sq", "mac": "mk", "mkd": "mk",
	"geo": "ka", "kat": "ka", "arm": "hy", "hye": "hy",
	"aze": "az", "kaz": "kk", "uzb": "uz", "mon": "mn",
	"nep": "ne", "sin": "si", "khm": "km", "lao": "lo",
	"mya": "my", "bur": "my", "tgl": "tl", "fil": "tl",
	"swa": "sw", "afr": "af", "lat": "la", "epo": "eo",
}

func normalizeLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	c = strings.ReplaceAll(c, "_", "-")
	if a, ok := langAliases[c]; ok {
		return a
	}
	return c
}

// LanguageName returns a human readable label for a language code.
func LanguageName(code string) string {
	c := normalizeLang(code)
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

var langToken = regexp.MustCompile(`(?i)(^|[ ._\-])(zh[-_]?(?:cn|sg|tw|hk|hans|hant)?|en|ja|jp|ko|kr|fr|de|es|it|ru|pt[-_]?br|pt|ar|hi|th|vi|und|chi|zho|chs|cht|eng|jpn|kor|fre|fra|ger|deu|spa|ita|rus|por)([ ._\-]|$)`)

func knownLangToken(token string) string {
	c := normalizeLang(token)
	if c == "" {
		return ""
	}
	if c == "und" {
		return "und"
	}
	if _, ok := langNames[c]; ok {
		return c
	}
	return ""
}

// DetectSubtitleLanguage extracts a language code from a sidecar filename.
func DetectSubtitleLanguage(filename, base string) string {
	stem := strings.TrimSuffix(filename, filepathExt(filename))
	if base != "" && strings.HasPrefix(strings.ToLower(stem), strings.ToLower(base)) {
		stem = stem[len(base):]
	}
	stem = strings.Trim(stem, " ._-")
	stem = strings.ReplaceAll(stem, "_", "-")
	if stem == "" {
		return "und"
	}
	lower := strings.ToLower(stem)
	if code := knownLangToken(lower); code != "" {
		return code
	}
	// Prefer the beginning of the suffix ("en.forced" -> "en").
	tokens := strings.FieldsFunc(lower, func(r rune) bool { return r == '.' || r == '-' || r == ' ' })
	for _, token := range tokens {
		if code := knownLangToken(token); code != "" {
			return code
		}
	}
	if m := langToken.FindStringSubmatch(" " + lower + " "); len(m) > 1 {
		if code := knownLangToken(m[2]); code != "" {
			return code
		}
	}
	return "und"
}

// DetectSubtitleTextLanguage guesses the language of a text subtitle from the
// scripts it uses. Kana and Hangul are unambiguous; Han without kana is
// treated as Chinese. It returns "" when there is not enough text.
func DetectSubtitleTextLanguage(data []byte) string {
	var han, kana, hangul int
	for _, r := range string(data) {
		switch {
		case r >= 0x3040 && r <= 0x30FF:
			kana++
		case r >= 0xAC00 && r <= 0xD7AF:
			hangul++
		case r >= 0x4E00 && r <= 0x9FFF:
			han++
		}
	}
	switch {
	case kana >= 20:
		return "ja"
	case hangul >= 20:
		return "ko"
	case han >= 20:
		return "zh"
	}
	return ""
}

// DetectSubtitleFileLanguage reads the head of a text subtitle file and
// guesses its language. Non-text formats return "".
func DetectSubtitleFileLanguage(path string) string {
	switch strings.ToLower(filepathExt(path)) {
	case ".ass", ".ssa", ".srt", ".vtt":
	default:
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 128<<10)
	n, _ := f.Read(buf)
	return DetectSubtitleTextLanguage(buf[:n])
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
	var cues []vttCue
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
		parts := strings.SplitN(lines[0], "-->", 2)
		if len(parts) != 2 {
			continue
		}
		startStamp, ok1 := parseSRTTime(strings.TrimSpace(parts[0]))
		endFields := strings.Fields(strings.TrimSpace(parts[1]))
		if len(endFields) == 0 {
			continue
		}
		endStamp, ok2 := parseSRTTime(endFields[0])
		if !ok1 || !ok2 {
			continue
		}
		body := srtTextToVTT(strings.Join(lines[1:], "\n"))
		cues = append(cues, vttCue{
			start: srtDuration(startStamp),
			body:  startStamp + " --> " + endStamp + "\n" + body + "\n\n",
		})
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].start < cues[j].start })
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, cue := range cues {
		out.WriteString(cue.body)
	}
	return []byte(out.String())
}

var (
	srtTagRe     = regexp.MustCompile(`(?i)</?(?:i|b|u)>`)
	srtFontTagRe = regexp.MustCompile(`(?i)</?font[^>]*>`)
)

// srtTextToVTT keeps the inline SRT tags WebVTT understands and drops font
// colour tags, which have no direct VTT equivalent.
func srtTextToVTT(s string) string {
	s = srtFontTagRe.ReplaceAllString(s, "")
	var b strings.Builder
	last := 0
	for _, loc := range srtTagRe.FindAllStringIndex(s, -1) {
		b.WriteString(escapeCueText(s[last:loc[0]]))
		b.WriteString(strings.ToLower(s[loc[0]:loc[1]]))
		last = loc[1]
	}
	b.WriteString(escapeCueText(s[last:]))
	return b.String()
}

func escapeCueText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func srtDuration(stamp string) time.Duration {
	m := srtTime.FindStringSubmatch(stamp)
	if len(m) != 5 {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	se, _ := strconv.Atoi(m[3])
	ms := m[4]
	for len(ms) < 3 {
		ms += "0"
	}
	msInt, _ := strconv.Atoi(ms)
	return time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute +
		time.Duration(se)*time.Second + time.Duration(msInt)*time.Millisecond
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
var blankLineRe = regexp.MustCompile(`\n{2,}`)

var (
	assPosRe     = regexp.MustCompile(`\\pos\(\s*(-?[0-9.]+)\s*,\s*(-?[0-9.]+)\s*\)`)
	assMoveRe    = regexp.MustCompile(`\\move\(\s*(-?[0-9.]+)\s*,\s*(-?[0-9.]+)`)
	assAlignRe   = regexp.MustCompile(`\\an([1-9])`)
	assDrawRe    = regexp.MustCompile(`\\p[1-9]`)
	assKaraokeRe = regexp.MustCompile(`\\k[fot]?`)
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
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	playW, playH := 384.0, 288.0
	section := ""
	styles := map[string]assStyle{}
	var styleFormat, eventFormat []string
	var cues []vttCue

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
			if cue, start, ok := assCueVTT(trimmed[len("dialogue:"):], eventFormat, styles, playW, playH); ok {
				cues = append(cues, vttCue{start: start, body: cue})
			}
		}
	}

	// WebVTT cues must be listed in start-time order; ASS events are usually
	// sorted but the format does not require it.
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].start < cues[j].start })
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, cue := range cues {
		out.WriteString(cue.body)
	}
	return []byte(out.String())
}

type vttCue struct {
	start time.Duration
	body  string
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

func assCueVTT(raw string, format []string, styles map[string]assStyle, playW, playH float64) (string, time.Duration, bool) {
	if len(format) == 0 {
		format = []string{"Layer", "Start", "End", "Style", "Name", "MarginL", "MarginR", "MarginV", "Effect", "Text"}
	}
	field := assField(format, splitCSVLimit(raw, len(format)))
	start, ok1 := parseASSTime(field("Start"))
	end, ok2 := parseASSTime(field("End"))
	if !ok1 || !ok2 || end <= start {
		return "", 0, false
	}
	body := field("Text")
	if body == "" || assDrawRe.MatchString(body) {
		// Vector drawings cannot be rendered as text.
		return "", 0, false
	}
	if assCreditStyle(field("Style")) {
		// Staff rolls are credits, not subtitles. Screen text, titles and
		// readable typesetting are kept as positioned cues; per-syllable
		// fragments are filtered by assKaraoke below.
		return "", 0, false
	}
	if assKaraoke(field("Style"), field("Effect"), body, (end - start).Seconds()) {
		// Karaoke syllables and animated typesetting fragments become piles
		// of overlapping one-character cues in WebVTT.
		return "", 0, false
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
		return "", 0, false
	}
	// A blank line terminates a WebVTT cue. Keep intentional line spacing
	// with a non-breaking space instead of emitting two consecutive newlines.
	body = blankLineRe.ReplaceAllString(body, "\n\u00a0\n")
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
	return fmt.Sprintf("%s --> %s line:%.1f%%,%s position:%.1f%% align:%s\n%s\n\n",
		vttTime(start), vttTime(end), clampPercent(y/playH*100), lineAlign, clampPercent(x/playW*100), alignName, body), start, true
}

// assKaraoke reports whether an event is a karaoke syllable or a typesetting
// fragment. Such events are one or two characters shown for a fraction of a
// second and cannot be represented in WebVTT.
// assCreditStyle reports whether a style carries credits rather than dialogue
// or on-screen text.
func assCreditStyle(style string) bool {
	return strings.EqualFold(strings.TrimSpace(style), "staff")
}

func assKaraoke(style, effect, body string, seconds float64) bool {
	name := strings.ToLower(strings.TrimSpace(style))
	if strings.Contains(name, "syl") || strings.Contains(name, "karaoke") || strings.Contains(name, "karo") {
		return true
	}
	text := assTag.ReplaceAllString(body, "")
	text = strings.ReplaceAll(text, `\N`, "")
	text = strings.ReplaceAll(text, `\n`, "")
	text = strings.TrimSpace(text)
	// A \k tag alone is normal for song lyrics: the whole line is readable
	// once the tags are stripped. Only syllable-sized pieces are dropped.
	if assKaraokeRe.MatchString(body) && (seconds < 0.6 || utf8.RuneCountInString(text) <= 2) {
		return true
	}
	if seconds > 0 && seconds < 0.35 && utf8.RuneCountInString(text) <= 1 {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(effect), "fx") && seconds < 0.6 {
		return true
	}
	return false
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
