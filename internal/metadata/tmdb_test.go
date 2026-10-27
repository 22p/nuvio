package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nuvio/internal/model"
)

func TestEnrichMovieAndInheritSeriesArt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "key" {
			t.Errorf("missing api key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{
				"id": 603, "title": "The Matrix", "original_title": "The Matrix",
				"overview": "A hacker discovers reality.", "poster_path": "/poster.jpg",
				"backdrop_path": "/backdrop.jpg", "release_date": "1999-03-31",
				"vote_average": 8.2, "genre_ids": []int{28, 878},
			}},
		})
	})
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{
				"id": 95396, "name": "Severance", "original_name": "Severance",
				"overview": "Office workers sever work memories.", "poster_path": "/severance.jpg",
				"backdrop_path": "/severance-bg.jpg", "first_air_date": "2022-02-18",
				"vote_average": 8.4, "genre_ids": []int{18, 9648},
			}},
		})
	})
	mux.HandleFunc("/movie/603/credits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cast": []map[string]any{
				{"name": "Keanu Reeves", "character": "Neo", "profile_path": "/keanu.jpg"},
				{"name": "Carrie-Anne Moss", "character": "Trinity", "profile_path": "/trinity.jpg"},
			},
		})
	})
	mux.HandleFunc("/tv/95396/credits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cast": []map[string]any{
				{"name": "Adam Scott", "character": "Mark Scout", "profile_path": "/mark.jpg"},
			},
		})
	})
	mux.HandleFunc("/genre/movie/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"genres": []map[string]any{
			{"id": 28, "name": "Action"}, {"id": 878, "name": "Science Fiction"},
		}})
	})
	mux.HandleFunc("/genre/tv/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"genres": []map[string]any{
			{"id": 18, "name": "Drama"}, {"id": 9648, "name": "Mystery"},
		}})
	})
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	seriesID := "series1"
	series := &model.Item{ID: seriesID, Type: model.TypeSeries, Name: "Severance", Year: 2022, LibraryID: "lib"}
	episode := &model.Item{ID: "ep1", Type: model.TypeEpisode, Name: "Good News", SeriesID: seriesID, ParentID: "s1", LibraryID: "lib"}
	season := &model.Item{ID: "s1", Type: model.TypeSeason, Name: "Season 1", SeriesID: seriesID, LibraryID: "lib"}
	movie := &model.Item{ID: "matrix", Type: model.TypeMovie, Name: "The Matrix", Year: 1999, LibraryID: "lib"}
	demo := &model.Item{ID: "demo", Type: model.TypeMovie, Name: "Demo", Demo: true, LibraryID: "lib"}

	e := New("key", "zh-CN")
	e.BaseURL = server.URL
	e.ImageBase = server.URL + "/img"
	if err := e.Enrich(context.Background(), []*model.Item{movie, series, season, episode, demo}); err != nil {
		t.Fatal(err)
	}
	if movie.TMDBID != 603 || movie.Overview == "" || movie.Rating != 8.2 {
		t.Fatalf("movie metadata: %+v", movie)
	}
	if len(movie.Genres) != 2 || movie.Genres[1] != "Science Fiction" {
		t.Fatalf("movie genres: %v", movie.Genres)
	}
	if len(movie.People) != 2 || movie.People[0].Name != "Keanu Reeves" || movie.People[0].Role != "Neo" {
		t.Fatalf("movie cast: %+v", movie.People)
	}
	if !strings.Contains(movie.People[0].ImageURL, "/w185/keanu.jpg") {
		t.Fatalf("cast image: %q", movie.People[0].ImageURL)
	}
	if len(episode.People) != 1 || episode.People[0].Name != "Adam Scott" {
		t.Fatalf("series cast not inherited: %+v", episode.People)
	}
	if !strings.HasSuffix(movie.PosterURL, "/w780/poster.jpg") || !strings.Contains(movie.BackdropURL, "backdrop.jpg") {
		t.Fatalf("movie art: %q %q", movie.PosterURL, movie.BackdropURL)
	}
	if series.TMDBID != 95396 || episode.PosterURL != series.PosterURL || season.BackdropURL != series.BackdropURL {
		t.Fatalf("series inheritance failed: series=%q ep=%q season=%q", series.PosterURL, episode.PosterURL, season.BackdropURL)
	}
	if demo.TMDBID != 0 || demo.PosterURL != "" {
		t.Fatal("demo item should not be enriched")
	}
}

func TestNoConfidentMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/search/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"id": 1, "title": "Completely Different", "release_date": "1999-01-01",
			}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"genres": []any{}})
	}))
	defer server.Close()
	e := New("key", "zh-CN")
	e.BaseURL = server.URL
	it := &model.Item{ID: "x", Type: model.TypeMovie, Name: "My Movie", Year: 1999}
	if err := e.Enrich(context.Background(), []*model.Item{it}); err != nil {
		t.Fatal(err)
	}
	if it.TMDBID != 0 || it.PosterURL != "" {
		t.Fatalf("wrong match accepted: %+v", it)
	}
}
