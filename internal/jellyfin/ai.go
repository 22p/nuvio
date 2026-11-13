package jellyfin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nuvio/internal/ai"
	"nuvio/internal/media"
	"nuvio/internal/model"
)

// translatedNameRe matches <itemID>-<sourceIndex>.<language>.vtt.
var translatedNameRe = regexp.MustCompile(`^([0-9a-f]+)-(\d+)\.([A-Za-z0-9-]+)\.vtt$`)

// ValidLanguageCode accepts the language tags used by the settings page and
// the translated subtitle URL. It deliberately allows only a dotted-free
// sequence of short alphanumeric groups, so a tag cannot become a path.
func ValidLanguageCode(code string) bool {
	if code == "" || len(code) > 35 {
		return false
	}
	for _, part := range strings.Split(code, "-") {
		if len(part) < 2 || len(part) > 8 {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
				continue
			}
			return false
		}
	}
	return true
}

func (a *API) registerAI(mux *http.ServeMux) {
	mux.HandleFunc("GET /Items/AI/Lists", a.handleAILists)
	mux.HandleFunc("POST /Items/{itemId}/Subtitles/{index}/Translate", a.handleTranslateSubtitle)
	for _, prefix := range []string{"/Videos", "/videos"} {
		mux.HandleFunc("GET "+prefix+"/{itemId}/TranslatedSubtitles/{lang}/Stream.vtt", a.handleTranslatedSubtitle)
	}
}

func (a *API) aiClient() *ai.Client {
	st := a.Store.Settings()
	if !st.AIEnabled || st.AIAPIKey == "" {
		return nil
	}
	return ai.New(st.AIBaseURL, st.AIAPIKey, st.AIModel)
}

// handleAILists returns the themed lists saved by the assistant.
func (a *API) handleAILists(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	type list struct {
		Title string    `json:"title"`
		Items []itemDTO `json:"items"`
	}
	out := []list{}
	for _, saved := range a.Store.Settings().AILists {
		items := []itemDTO{}
		for _, id := range saved.ItemIDs {
			it, ok := a.Catalog.Get(normalizeID(id))
			if !ok || !canSeeItem(u, it) {
				continue
			}
			items = append(items, a.buildItem(it, u.ID, false, ""))
		}
		if len(items) >= 2 {
			out = append(out, list{Title: saved.Title, Items: items})
		}
	}
	writeJSON(w, 200, out)
}

func (a *API) translatedDir() string {
	dir := filepath.Join(a.DataDir, "subtitles")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func (a *API) translatedPath(it *model.Item, srcIndex int, lang string) string {
	return filepath.Join(a.translatedDir(), fmt.Sprintf("%s-%d.%s.vtt", it.ID, srcIndex, lang))
}

type translatedFile struct {
	Index int
	Lang  string
	Path  string
	Src   int
}

// translatedFiles lists the generated tracks of an item, sorted so the stream
// index stays the same between a translation and a later request.
func (a *API) translatedFiles(it *model.Item) []translatedFile {
	paths, _ := filepath.Glob(filepath.Join(a.translatedDir(), it.ID+"-*.vtt"))
	sort.Strings(paths)
	out := make([]translatedFile, 0, len(paths))
	for _, p := range paths {
		m := translatedNameRe.FindStringSubmatch(filepath.Base(p))
		if len(m) != 4 {
			continue
		}
		src := 0
		fmt.Sscanf(m[2], "%d", &src)
		out = append(out, translatedFile{Index: 9000 + len(out), Lang: m[3], Path: p, Src: src})
	}
	return out
}

// translatedStreams appends the AI tracks to the media streams.
func (a *API) translatedStreams(it *model.Item, token string) []mediaStreamDTO {
	files := a.translatedFiles(it)
	if len(files) == 0 {
		return nil
	}
	out := make([]mediaStreamDTO, 0, len(files))
	for _, f := range files {
		title := media.LanguageName(f.Lang) + " (AI)"
		url := fmt.Sprintf("/Videos/%s/TranslatedSubtitles/%s/Stream.vtt", it.ID, f.Lang)
		if token != "" {
			url += "?ApiKey=" + token
		}
		out = append(out, mediaStreamDTO{
			Type: "Subtitle", Index: f.Index, Codec: "webvtt", Language: f.Lang,
			Title: title, DisplayTitle: title, DisplayLanguage: media.LanguageName(f.Lang),
			IsExternal: true, DeliveryMethod: "External", DeliveryUrl: url,
			SupportsExternalStream: true, IsTextSubtitleStream: true,
		})
	}
	return out
}

func (a *API) handleTranslatedSubtitle(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, "")
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || !canSeeItem(u, it) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	lang := r.PathValue("lang")
	if !ValidLanguageCode(lang) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var track *translatedFile
	for _, f := range a.translatedFiles(it) {
		if strings.EqualFold(f.Lang, lang) {
			found := f
			track = &found
			break
		}
	}
	if track == nil {
		http.Error(w, "subtitle unavailable", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(track.Path)
	if err != nil {
		http.Error(w, "subtitle unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=600")
	_, _ = w.Write(data)
}

// handleTranslateSubtitle converts one existing track into the configured
// language and caches the result next to the extracted subtitles.
func (a *API) handleTranslateSubtitle(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, "")
	if !ok {
		return
	}
	if !u.IsAdmin {
		writeError(w, 403, "Only administrators can translate subtitles")
		return
	}
	client := a.aiClient()
	if client == nil {
		writeError(w, 400, "AI is not configured")
		return
	}
	// The whole track is translated in one request, which can take minutes.
	client.HTTP.Timeout = 5 * time.Minute
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || !canSeeItem(u, it) || it.Media == nil {
		writeError(w, 404, "Item does not exist")
		return
	}
	index := parseIndex(r.PathValue("index"))
	var sub *model.Subtitle
	for i := range it.Media.Subtitles {
		if it.Media.Subtitles[i].Index == index {
			sub = &it.Media.Subtitles[i]
			break
		}
	}
	if sub == nil {
		writeError(w, 404, "Subtitle not found")
		return
	}
	lang := strings.TrimSpace(r.URL.Query().Get("language"))
	if lang == "" {
		lang = a.Store.Settings().AITargetLanguage
	}
	if lang == "" {
		lang = "zh-CN"
	}
	if !ValidLanguageCode(lang) {
		writeError(w, 400, "Invalid subtitle language")
		return
	}
	data, err := a.loadSubtitle(r, it, sub)
	if err != nil {
		writeError(w, 404, "Subtitle unavailable")
		return
	}
	translated, err := translateVTT(r.Context(), client, string(data), lang)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	path := a.translatedPath(it, sub.Index, lang)
	if err := os.WriteFile(path, []byte(translated), 0o644); err != nil {
		writeError(w, 500, "Could not save the translation")
		return
	}
	// Find the index the new track gets in the stream list.
	indexOut := 9000
	for _, f := range a.translatedFiles(it) {
		if f.Path == path {
			indexOut = f.Index
			break
		}
	}
	title := media.LanguageName(lang) + " (AI)"
	url := fmt.Sprintf("/Videos/%s/TranslatedSubtitles/%s/Stream.vtt", it.ID, lang)
	if token := a.tokenFromRequest(r); token != "" {
		url += "?ApiKey=" + urlQueryEscape(token)
	}
	writeJSON(w, 200, map[string]any{
		"index": indexOut, "language": lang, "title": title, "deliveryUrl": url,
	})
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

func parseIndex(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	if s == "" {
		return -1
	}
	return n
}

var (
	assTagRe     = regexp.MustCompile(`\{\\[^}]*\}`)
	vttTagRe     = regexp.MustCompile(`<[^>]{1,40}>`)
	translatedRe = regexp.MustCompile(`^\s*(\d+)\|[^|]*\|[^|]*\|(.*)$`)
)

// translateVTT translates a track in a compact line format:
//
//	1|321.2|323.5|Hello, how are you?
//
// Only text is sent to the model. Cue identifiers and cue settings (position,
// line, alignment, region) stay local and are written back unchanged, so a
// translated sign keeps the place and shape of the original cue.
func translateVTT(ctx context.Context, client *ai.Client, vtt, lang string) (string, error) {
	cues := parseCues(vtt)
	if len(cues) == 0 {
		return "", fmt.Errorf("subtitle has no text")
	}
	if err := translateJobs(ctx, client, cues, lang); err != nil {
		return "", err
	}
	// WebVTT requires cues ordered by start time; a translated ASS-derived
	// track can arrive out of order.
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].startSec < cues[j].startSec })
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	for _, header := range vttHeaderBlocks(vtt) {
		b.WriteString("\n" + header + "\n")
	}
	for _, c := range cues {
		if c.text == "" {
			continue
		}
		b.WriteString("\n")
		if c.id != "" {
			b.WriteString(c.id + "\n")
		}
		b.WriteString(c.start + " --> " + c.end)
		if c.settings != "" {
			b.WriteString(" " + c.settings)
		}
		b.WriteString("\n" + escapeVTTText(c.text) + "\n")
	}
	return b.String(), nil
}

type cueJob struct {
	id       string
	start    string
	end      string
	settings string
	startSec float64
	endSec   float64
	text     string
}

// vttHeaderBlocks returns STYLE and REGION blocks verbatim. They describe how
// cues are painted, so translating the cue text must not drop them.
func vttHeaderBlocks(vtt string) []string {
	blocks := strings.Split(strings.ReplaceAll(vtt, "\r\n", "\n"), "\n\n")
	out := []string{}
	for _, block := range blocks {
		first := strings.ToUpper(strings.TrimSpace(strings.SplitN(block, "\n", 2)[0]))
		if first == "STYLE" || first == "REGION" {
			out = append(out, strings.TrimSpace(block))
		}
	}
	return out
}

// parseCues reads the timing and layout of every cue and reduces its text to
// plain characters: override blocks, HTML tags, entities and line breaks are
// gone. The cue identifier and the settings after the end timestamp are kept
// so callers can rebuild the original layout around the translated text.
func parseCues(vtt string) []cueJob {
	blocks := strings.Split(strings.ReplaceAll(vtt, "\r\n", "\n"), "\n\n")
	out := []cueJob{}
	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		timing := -1
		for i, line := range lines {
			if strings.Contains(line, "-->") {
				timing = i
				break
			}
		}
		if timing < 0 {
			continue
		}
		// WEBVTT, STYLE, REGION and NOTE blocks are not cues.
		first := strings.ToUpper(strings.TrimSpace(lines[0]))
		if strings.HasPrefix(first, "WEBVTT") || first == "STYLE" || first == "REGION" ||
			first == "NOTE" || strings.HasPrefix(first, "NOTE ") {
			continue
		}
		parts := strings.SplitN(lines[timing], "-->", 2)
		start := strings.TrimSpace(parts[0])
		end, settings := splitVTTSettings(strings.TrimSpace(parts[1]))
		id := ""
		if timing > 0 {
			id = strings.TrimSpace(lines[timing-1])
		}
		text := cleanCueText(strings.Join(lines[timing+1:], " "))
		if text == "" {
			continue
		}
		startSec, endSec := parseTimestamp(start), parseTimestamp(end)
		// A single character on screen for a fraction of a second is a
		// karaoke syllable, not dialogue; the ASS converter drops most of
		// them, and VTT sidecars need the same rule.
		if duration := endSec - startSec; duration > 0 && duration < 0.4 && utf8.RuneCountInString(text) <= 1 {
			continue
		}
		out = append(out, cueJob{
			id: id, start: start, end: end, settings: settings,
			startSec: startSec, endSec: endSec,
			text: text,
		})
	}
	return out
}

// splitVTTSettings separates the end timestamp from the cue settings that
// follow it, for example "00:00:04.000 line:9% position:50% align:center".
func splitVTTSettings(s string) (end, settings string) {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

// escapeVTTText keeps subtitle text from being read as WebVTT markup.
func escapeVTTText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func cleanCueText(s string) string {
	s = assTagRe.ReplaceAllString(s, "")
	s = vttTagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "　", " ",
	).Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", "｜")
	return strings.TrimSpace(s)
}

func parseTimestamp(ts string) float64 {
	parts := strings.Split(ts, ":")
	seconds := 0.0
	for _, p := range parts {
		v, _ := strconv.ParseFloat(strings.ReplaceAll(p, ",", "."), 64)
		seconds = seconds*60 + v
	}
	return seconds
}

// translateJobs sends one contiguous run of cues as pipe separated lines and
// fills the translated text back. A truncated reply is retried in halves, and
// a missing tail is retried on its own.
func translateJobs(ctx context.Context, client *ai.Client, jobs []cueJob, lang string) error {
	if len(jobs) == 0 {
		return nil
	}
	lines := make([]string, len(jobs))
	for i := range jobs {
		lines[i] = fmt.Sprintf("%d|%.1f|%.1f|%s", i+1, jobs[i].startSec, jobs[i].endSec, jobs[i].text)
	}
	maxTokens := len(jobs)*48 + 512
	if maxTokens < 4096 {
		maxTokens = 4096
	}
	if maxTokens > 16000 {
		maxTokens = 16000
	}
	target := lang
	if name := media.LanguageName(lang); name != "" && !strings.EqualFold(name, lang) {
		target = name + " (" + lang + ")"
	}
	reply, err := client.ChatWith(ctx,
		"You translate subtitles. The input is one cue per line in the form index|start|end|text. Reply with exactly the same lines, keeping index and timestamps unchanged and translating only the text. Never split a cue across lines, never merge two cues. No commentary, no extra lines, no markdown fences.",
		"Target language: "+target+"\n"+strings.Join(lines, "\n"), false, maxTokens, true)
	if err != nil {
		return fmt.Errorf("translation failed: %w", err)
	}
	translated := map[int]string{}
	last := 0
	for _, raw := range strings.Split(reply, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		if m := translatedRe.FindStringSubmatch(line); len(m) == 3 {
			idx, err := strconv.Atoi(m[1])
			if err != nil || idx < 1 || idx > len(jobs) {
				continue
			}
			if text := strings.TrimSpace(m[2]); text != "" {
				translated[idx-1] = text
				last = idx
			}
			continue
		}
		// The model wrapped a cue over two lines; keep it as one cue instead
		// of dropping the continuation.
		if last > 0 {
			if text, ok := translated[last-1]; ok {
				translated[last-1] = text + " " + line
			}
		}
	}
	if len(translated) == 0 {
		if len(jobs) > 1 {
			mid := len(jobs) / 2
			if err := translateJobs(ctx, client, jobs[:mid], lang); err != nil {
				return err
			}
			return translateJobs(ctx, client, jobs[mid:], lang)
		}
		return fmt.Errorf("translation reply was not in the expected format")
	}
	firstMissing := -1
	for i := range jobs {
		if text, ok := translated[i]; ok {
			jobs[i].text = text
		} else if firstMissing < 0 {
			firstMissing = i
		}
	}
	if firstMissing > 0 {
		return translateJobs(ctx, client, jobs[firstMissing:], lang)
	}
	return nil
}

func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
