package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"nuvio/internal/ai"
	"nuvio/internal/media"
	"nuvio/internal/model"
)

// translatedNameRe matches <itemID>-<sourceIndex>.<language>.vtt.
var translatedNameRe = regexp.MustCompile(`^([0-9a-f]+)-(\d+)\.([A-Za-z-]+)\.vtt$`)

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
			url += "?api_key=" + token
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
	paths, _ := filepath.Glob(filepath.Join(a.translatedDir(), it.ID+"-*."+lang+".vtt"))
	if len(paths) == 0 {
		http.Error(w, "subtitle unavailable", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(paths[0])
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
		url += "?api_key=" + urlQueryEscape(token)
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

// translateVTT translates the text lines of a WebVTT file, keeping cue timing
// untouched. Cues are sent in small batches so a long film stays within the
// model's context window.
func translateVTT(ctx context.Context, client *ai.Client, vtt, lang string) (string, error) {
	blocks := strings.Split(strings.ReplaceAll(vtt, "\r\n", "\n"), "\n\n")
	type job struct {
		block int
		line  int
		text  string
	}
	jobs := []job{}
	for bi, block := range blocks {
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
		for i := timing + 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			jobs = append(jobs, job{block: bi, line: i, text: lines[i]})
		}
	}
	if len(jobs) == 0 {
		return "", fmt.Errorf("subtitle has no text")
	}
	for start := 0; start < len(jobs); start += 30 {
		end := start + 30
		if end > len(jobs) {
			end = len(jobs)
		}
		payload := map[string]string{}
		for i := start; i < end; i++ {
			payload[fmt.Sprintf("%d", i)] = jobs[i].text
		}
		raw, _ := json.Marshal(payload)
		reply, err := client.Chat(ctx,
			"You translate subtitles. Keep names, tone and line breaks. Reply with JSON only: an object with the same keys and the translated text as values.",
			"Target language: "+lang+"\n"+string(raw), true)
		if err != nil {
			return "", fmt.Errorf("translation failed: %w", err)
		}
		var out map[string]string
		if err := json.Unmarshal([]byte(stripJSONFence(reply)), &out); err != nil {
			return "", fmt.Errorf("translation reply was not valid JSON")
		}
		for i := start; i < end; i++ {
			if translated := strings.TrimSpace(out[fmt.Sprintf("%d", i)]); translated != "" {
				jobs[i].text = translated
			}
		}
	}
	for _, j := range jobs {
		lines := strings.Split(blocks[j.block], "\n")
		lines[j.line] = j.text
		blocks[j.block] = strings.Join(lines, "\n")
	}
	return strings.Join(blocks, "\n\n"), nil
}

func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
