package vectors

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"nuvio/internal/model"
)

// Document builds the text that represents an item in the embedding index.
// It returns an empty string for types that are not searchable.
func Document(it *model.Item) string {
	if it == nil {
		return ""
	}
	kind := ""
	switch it.Type {
	case model.TypeMovie:
		kind = "电影"
	case model.TypeSeries:
		kind = "剧集"
	case model.TypeEpisode:
		kind = "单集"
	default:
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "标题: %s\n", it.Name)
	if it.Type == model.TypeEpisode {
		if it.SeriesName != "" {
			fmt.Fprintf(&b, "剧集: %s\n", it.SeriesName)
		}
		if it.SeasonNumber > 0 || it.EpisodeNumber > 0 {
			fmt.Fprintf(&b, "集数: S%02dE%02d\n", it.SeasonNumber, it.EpisodeNumber)
		}
	}
	if it.Year > 0 {
		fmt.Fprintf(&b, "年份: %d\n", it.Year)
	}
	fmt.Fprintf(&b, "类型: %s\n", kind)
	if len(it.Genres) > 0 {
		fmt.Fprintf(&b, "分类: %s\n", strings.Join(it.Genres, ", "))
	}
	if it.OfficialRating != "" {
		fmt.Fprintf(&b, "分级: %s\n", it.OfficialRating)
	}
	if len(it.Taglines) > 0 {
		fmt.Fprintf(&b, "宣传语: %s\n", strings.Join(it.Taglines, " / "))
	}
	if it.Overview != "" {
		fmt.Fprintf(&b, "简介: %s\n", truncateRunes(it.Overview, 1200))
	}
	if len(it.People) > 0 {
		names := make([]string, 0, 10)
		for _, p := range it.People {
			if p.Name == "" {
				continue
			}
			names = append(names, p.Name)
			if len(names) == 10 {
				break
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "主演: %s\n", strings.Join(names, ", "))
		}
	}
	return strings.TrimSpace(b.String())
}

// Hash returns a stable digest of a document.
func Hash(doc string) string {
	sum := sha256.Sum256([]byte(doc))
	return hex.EncodeToString(sum[:])
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
