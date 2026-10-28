// Package model contains the domain types shared by the catalog, the media
// scanner and the Jellyfin-compatible API layer.
package model

import "time"

// Item types. The values are part of the Jellyfin wire format, keep them stable.
const (
	TypeMovie   = "Movie"
	TypeSeries  = "Series"
	TypeSeason  = "Season"
	TypeEpisode = "Episode"
)

// Library types.
const (
	LibraryMovies = "movies"
	LibraryShows  = "tvshows"
	LibraryMixed  = "mixed"
)

// Item is a single catalog entry. Movies and episodes carry a Media payload,
// series and seasons are virtual folders.
type Item struct {
	ID             string   `json:"id"`
	Type           string   `json:"type"`
	Name           string   `json:"name"`
	SortName       string   `json:"sortName"`
	Overview       string   `json:"overview"`
	Year           int      `json:"year"`
	PremiereDate   string   `json:"premiereDate,omitempty"`
	Rating         float64  `json:"rating,omitempty"`
	OfficialRating string   `json:"officialRating,omitempty"`
	RuntimeTicks   int64    `json:"runtimeTicks"`
	Genres         []string `json:"genres,omitempty"`
	Taglines       []string `json:"taglines,omitempty"`

	LibraryID     string `json:"libraryId"`
	ParentID      string `json:"parentId"`
	SeriesID      string `json:"seriesId,omitempty"`
	SeriesName    string `json:"seriesName,omitempty"`
	SeasonID      string `json:"seasonId,omitempty"`
	SeasonName    string `json:"seasonName,omitempty"`
	SeasonNumber  int    `json:"seasonNumber,omitempty"`
	EpisodeNumber int    `json:"episodeNumber,omitempty"`
	ChildCount    int    `json:"childCount,omitempty"`

	Path      string     `json:"path,omitempty"`
	Container string     `json:"container,omitempty"`
	Size      int64      `json:"size,omitempty"`
	AddedAt   time.Time  `json:"addedAt"`
	Media     *MediaInfo `json:"media,omitempty"`

	// PosterPath/BackdropPath point at sidecar artwork on disk. When empty the
	// image endpoint renders deterministic procedural artwork instead.
	PosterPath   string   `json:"posterPath,omitempty"`
	BackdropPath string   `json:"backdropPath,omitempty"`
	LogoPath     string   `json:"logoPath,omitempty"`
	ThumbPath    string   `json:"thumbPath,omitempty"`
	PosterURL    string   `json:"posterUrl,omitempty"`
	BackdropURL  string   `json:"backdropUrl,omitempty"`
	ArtSeed      string   `json:"artSeed,omitempty"`
	Demo         bool     `json:"demo,omitempty"`
	People       []Person `json:"people,omitempty"`

	TMDBID           int    `json:"tmdbId,omitempty"`
	MetadataSource   string `json:"metadataSource,omitempty"`
	MetadataLanguage string `json:"metadataLanguage,omitempty"`
}

// Person is a cast or crew member attached to an item. ImageURL is an optional
// remote portrait (TMDb) used by the web UI.
type Person struct {
	Name     string `json:"name"`
	Role     string `json:"role,omitempty"`
	Type     string `json:"type,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
}

// MediaInfo describes the single video file backing a movie or episode.
type MediaInfo struct {
	Container     string       `json:"container"`
	DurationTicks int64        `json:"durationTicks"`
	Size          int64        `json:"size"`
	Bitrate       int64        `json:"bitrate,omitempty"`
	VideoCodec    string       `json:"videoCodec"`
	AudioCodec    string       `json:"audioCodec"`
	Width         int          `json:"width,omitempty"`
	Height        int          `json:"height,omitempty"`
	VideoProfile  string       `json:"videoProfile,omitempty"`
	FrameRate     float64      `json:"frameRate,omitempty"`
	AudioChannels int          `json:"audioChannels,omitempty"`
	AudioTracks   []AudioTrack `json:"audioTracks,omitempty"`
	Subtitles     []Subtitle   `json:"subtitles,omitempty"`
}

// AudioTrack is an audio stream exposed to clients.
type AudioTrack struct {
	Index    int    `json:"index"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Codec    string `json:"codec,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Default  bool   `json:"default,omitempty"`
}

// Subtitle describes an external (sidecar) or embedded subtitle stream.
type Subtitle struct {
	Index       int    `json:"index"`
	Language    string `json:"language,omitempty"`
	Title       string `json:"title,omitempty"`
	Codec       string `json:"codec,omitempty"`
	Path        string `json:"path,omitempty"`        // sidecar file
	Embedded    bool   `json:"embedded,omitempty"`    // needs ffmpeg extraction
	StreamIndex int    `json:"streamIndex,omitempty"` // ffmpeg stream index
	Forced      bool   `json:"forced,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// Library is a configured media folder.
type Library struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Type    string    `json:"type"`
	Paths   []string  `json:"paths"`
	Demo    bool      `json:"demo,omitempty"`
	AddedAt time.Time `json:"addedAt"`
}

// Progress tracks a user's position inside an item.
type Progress struct {
	PositionTicks int64     `json:"positionTicks"`
	Played        bool      `json:"played"`
	PlayCount     int       `json:"playCount"`
	LastPlayed    time.Time `json:"lastPlayed"`
}

// User is a local server account. The web UI and native Jellyfin clients share
// the same accounts.
type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"passwordHash"`
	Salt         string    `json:"salt"`
	IsAdmin      bool      `json:"isAdmin"`
	HasPassword  bool      `json:"hasPassword"`
	CreatedAt    time.Time `json:"createdAt"`
	// AllowedLibraries limits which libraries a user can browse. Empty means
	// every library; administrators always see everything.
	AllowedLibraries []string             `json:"allowedLibraries,omitempty"`
	AllowTranscode   bool                 `json:"allowTranscode"`
	AllowRemux       bool                 `json:"allowRemux"`
	Played           map[string]bool      `json:"played,omitempty"`
	Progress         map[string]*Progress `json:"progress,omitempty"`
	Favorites        map[string]bool      `json:"favorites,omitempty"`
}

// ServerSettings are administrative knobs persisted with the store.
type ServerSettings struct {
	ServerName       string `json:"serverName"`
	TranscodeEnabled bool   `json:"transcodeEnabled"`
	RemuxEnabled     bool   `json:"remuxEnabled"`
	MaxBitrateMbps   int    `json:"maxBitrateMbps"`
	TranscodePreset  string `json:"transcodePreset"`
	TranscodeCodec   string `json:"transcodeCodec"`
	HardwareAccel    string `json:"hardwareAccel"`
	FFmpegPath       string `json:"ffmpegPath,omitempty"`
	FFprobePath      string `json:"ffprobePath,omitempty"`
	TMDBAPIKey       string `json:"tmdbApiKey,omitempty"`
	MetadataLanguage string `json:"metadataLanguage,omitempty"`
	// ScannerVersion tracks metadata/scan changes that require a catalog
	// refresh when upgrading.
	ScannerVersion int `json:"scannerVersion,omitempty"`
}

// DefaultServerSettings returns the out-of-the-box configuration.
func DefaultServerSettings(name string) ServerSettings {
	return ServerSettings{
		ServerName:       name,
		TranscodeEnabled: true,
		RemuxEnabled:     true,
		MaxBitrateMbps:   20,
		TranscodePreset:  "veryfast",
		TranscodeCodec:   "h264",
		HardwareAccel:    "auto",
		MetadataLanguage: "zh-CN",
	}
}
