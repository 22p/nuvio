package jellyfin

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"nuvio/internal/model"
)

// User DTO ---------------------------------------------------------------

type userPolicy struct {
	IsAdministrator                  bool     `json:"IsAdministrator"`
	IsHidden                         bool     `json:"IsHidden"`
	IsDisabled                       bool     `json:"IsDisabled"`
	EnableCollectionManagement       bool     `json:"EnableCollectionManagement"`
	EnableAllFolders                 bool     `json:"EnableAllFolders"`
	EnabledFolders                   []string `json:"EnabledFolders"`
	EnableContentDownloading         bool     `json:"EnableContentDownloading"`
	EnablePublicSharing              bool     `json:"EnablePublicSharing"`
	EnableRemoteAccess               bool     `json:"EnableRemoteAccess"`
	EnableRemoteControlOfOtherUsers  bool     `json:"EnableRemoteControlOfOtherUsers"`
	EnableSharedDeviceControl        bool     `json:"EnableSharedDeviceControl"`
	EnableLiveTvManagement           bool     `json:"EnableLiveTvManagement"`
	EnableLiveTvAccess               bool     `json:"EnableLiveTvAccess"`
	EnableMediaPlayback              bool     `json:"EnableMediaPlayback"`
	EnableAudioPlaybackTranscoding   bool     `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding   bool     `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing           bool     `json:"EnablePlaybackRemuxing"`
	ForceRemoteSourceTranscoding     bool     `json:"ForceRemoteSourceTranscoding"`
	EnableMediaConversion            bool     `json:"EnableMediaConversion"`
	EnableContentDeletion            bool     `json:"EnableContentDeletion"`
	EnableContentDeletionFromFolders []string `json:"EnableContentDeletionFromFolders"`
	EnableSyncTranscoding            bool     `json:"EnableSyncTranscoding"`
	EnableSubtitleManagement         bool     `json:"EnableSubtitleManagement"`
	EnableSubtitleDownloading        bool     `json:"EnableSubtitleDownloading"`
	EnableLyricManagement            bool     `json:"EnableLyricManagement"`
	EnableUserPreferenceAccess       bool     `json:"EnableUserPreferenceAccess"`
	EnableAllDevices                 bool     `json:"EnableAllDevices"`
	EnabledDevices                   []string `json:"EnabledDevices"`
	EnableAllChannels                bool     `json:"EnableAllChannels"`
	EnabledChannels                  []string `json:"EnabledChannels"`
	BlockedChannels                  []string `json:"BlockedChannels"`
	BlockedMediaFolders              []string `json:"BlockedMediaFolders"`
	BlockedTags                      []string `json:"BlockedTags"`
	AllowedTags                      []string `json:"AllowedTags"`
	MaxActiveSessions                int      `json:"MaxActiveSessions"`
	RemoteClientBitrateLimit         int      `json:"RemoteClientBitrateLimit"`
	AuthenticationProviderId         string   `json:"AuthenticationProviderId"`
	PasswordResetProviderId          string   `json:"PasswordResetProviderId"`
	SyncPlayAccess                   string   `json:"SyncPlayAccess"`
	InvalidLoginAttemptCount         int      `json:"InvalidLoginAttemptCount"`
	LoginAttemptsBeforeLockout       int      `json:"LoginAttemptsBeforeLockout"`
}

type userConfiguration struct {
	AudioLanguagePreference    string   `json:"AudioLanguagePreference,omitempty"`
	PlayDefaultAudioTrack      bool     `json:"PlayDefaultAudioTrack"`
	SubtitleLanguagePreference string   `json:"SubtitleLanguagePreference,omitempty"`
	DisplayMissingEpisodes     bool     `json:"DisplayMissingEpisodes"`
	GroupedFolders             []string `json:"GroupedFolders"`
	SubtitleMode               string   `json:"SubtitleMode"`
	DisplayCollectionsView     bool     `json:"DisplayCollectionsView"`
	EnableLocalPassword        bool     `json:"EnableLocalPassword"`
	OrderedViews               []string `json:"OrderedViews"`
	LatestItemsExcludes        []string `json:"LatestItemsExcludes"`
	MyMediaExcludes            []string `json:"MyMediaExcludes"`
	HidePlayedInLatest         bool     `json:"HidePlayedInLatest"`
	RememberAudioSelections    bool     `json:"RememberAudioSelections"`
	RememberSubtitleSelections bool     `json:"RememberSubtitleSelections"`
	EnableNextEpisodeAutoPlay  bool     `json:"EnableNextEpisodeAutoPlay"`
	CastReceiverId             string   `json:"CastReceiverId,omitempty"`
}

type userDTO struct {
	Name                      string            `json:"Name"`
	ServerId                  string            `json:"ServerId"`
	ServerName                string            `json:"ServerName"`
	Id                        string            `json:"Id"`
	HasPassword               bool              `json:"HasPassword"`
	HasConfiguredPassword     bool              `json:"HasConfiguredPassword"`
	HasConfiguredEasyPassword bool              `json:"HasConfiguredEasyPassword"`
	EnableAutoLogin           bool              `json:"EnableAutoLogin"`
	LastLoginDate             *time.Time        `json:"LastLoginDate,omitempty"`
	LastActivityDate          *time.Time        `json:"LastActivityDate,omitempty"`
	Configuration             userConfiguration `json:"Configuration"`
	Policy                    userPolicy        `json:"Policy"`
	PrimaryImageTag           string            `json:"PrimaryImageTag,omitempty"`
}

func buildUserDTO(u *model.User, serverID, serverName string) userDTO {
	folders := u.AllowedLibraries
	if folders == nil {
		folders = []string{}
	}
	p := userPolicy{
		IsAdministrator:                  u.IsAdmin,
		IsDisabled:                       false,
		EnableCollectionManagement:       u.IsAdmin,
		EnableAllFolders:                 len(folders) == 0,
		EnabledFolders:                   folders,
		EnableContentDownloading:         true,
		EnablePublicSharing:              false,
		EnableRemoteAccess:               true,
		EnableRemoteControlOfOtherUsers:  u.IsAdmin,
		EnableSharedDeviceControl:        true,
		EnableMediaPlayback:              true,
		EnableAudioPlaybackTranscoding:   u.AllowTranscode,
		EnableVideoPlaybackTranscoding:   u.AllowTranscode,
		EnablePlaybackRemuxing:           u.AllowRemux,
		EnableMediaConversion:            u.AllowTranscode,
		EnableContentDeletion:            u.IsAdmin,
		EnableContentDeletionFromFolders: []string{},
		EnableSyncTranscoding:            true,
		EnableSubtitleManagement:         true,
		EnableSubtitleDownloading:        true,
		EnableLyricManagement:            false,
		EnableUserPreferenceAccess:       true,
		EnableAllDevices:                 true,
		EnabledDevices:                   []string{},
		EnableAllChannels:                true,
		EnabledChannels:                  []string{},
		BlockedChannels:                  []string{},
		BlockedMediaFolders:              []string{},
		BlockedTags:                      []string{},
		AllowedTags:                      []string{},
		MaxActiveSessions:                0,
		RemoteClientBitrateLimit:         0,
		AuthenticationProviderId:         "Default",
		PasswordResetProviderId:          "Default",
		SyncPlayAccess:                   "None",
		LoginAttemptsBeforeLockout:       -1,
	}
	return userDTO{
		Name:                      u.Name,
		ServerId:                  serverID,
		ServerName:                serverName,
		Id:                        u.ID,
		HasPassword:               u.HasPassword,
		HasConfiguredPassword:     u.HasPassword,
		HasConfiguredEasyPassword: false,
		Configuration: userConfiguration{
			PlayDefaultAudioTrack:      true,
			GroupedFolders:             []string{},
			SubtitleMode:               "Default",
			OrderedViews:               []string{},
			LatestItemsExcludes:        []string{},
			MyMediaExcludes:            []string{},
			HidePlayedInLatest:         true,
			RememberAudioSelections:    true,
			RememberSubtitleSelections: true,
			EnableNextEpisodeAutoPlay:  true,
		},
		Policy: p,
	}
}

// Item DTO ----------------------------------------------------------------

type userItemData struct {
	ItemId                string     `json:"ItemId"`
	PlaybackPositionTicks int64      `json:"PlaybackPositionTicks"`
	PlayCount             int        `json:"PlayCount"`
	IsFavorite            bool       `json:"IsFavorite"`
	Played                bool       `json:"Played"`
	Key                   string     `json:"Key"`
	LastPlayedDate        *time.Time `json:"LastPlayedDate,omitempty"`
	PlayedPercentage      float64    `json:"PlayedPercentage,omitempty"`
	UnplayedItemCount     int        `json:"UnplayedItemCount,omitempty"`
}

type itemDTO struct {
	Name                     string            `json:"Name"`
	ServerId                 string            `json:"ServerId"`
	Id                       string            `json:"Id"`
	DateCreated              string            `json:"DateCreated"`
	Container                string            `json:"Container,omitempty"`
	SortName                 string            `json:"SortName"`
	PremiereDate             string            `json:"PremiereDate,omitempty"`
	ProductionYear           int               `json:"ProductionYear,omitempty"`
	IndexNumber              *int              `json:"IndexNumber"`
	ParentIndexNumber        *int              `json:"ParentIndexNumber"`
	IsFolder                 bool              `json:"IsFolder"`
	ParentId                 string            `json:"ParentId,omitempty"`
	Type                     string            `json:"Type"`
	CollectionType           string            `json:"CollectionType,omitempty"`
	Overview                 string            `json:"Overview,omitempty"`
	Taglines                 []string          `json:"Taglines,omitempty"`
	Genres                   []string          `json:"Genres,omitempty"`
	CommunityRating          float64           `json:"CommunityRating,omitempty"`
	OfficialRating           string            `json:"OfficialRating,omitempty"`
	RunTimeTicks             int64             `json:"RunTimeTicks,omitempty"`
	Size                     int64             `json:"Size,omitempty"`
	Path                     string            `json:"Path,omitempty"`
	FileName                 string            `json:"FileName,omitempty"`
	MediaType                string            `json:"MediaType,omitempty"`
	VideoType                string            `json:"VideoType,omitempty"`
	PlayAccess               string            `json:"PlayAccess"`
	CanDownload              bool              `json:"CanDownload"`
	CanDelete                bool              `json:"CanDelete"`
	ChildCount               int               `json:"ChildCount,omitempty"`
	RecursiveItemCount       int               `json:"RecursiveItemCount,omitempty"`
	SeriesId                 string            `json:"SeriesId,omitempty"`
	SeriesName               string            `json:"SeriesName,omitempty"`
	SeriesPrimaryImageTag    string            `json:"SeriesPrimaryImageTag,omitempty"`
	ParentPrimaryImageItemId string            `json:"ParentPrimaryImageItemId,omitempty"`
	ParentBackdropItemId     string            `json:"ParentBackdropItemId,omitempty"`
	SeasonId                 string            `json:"SeasonId,omitempty"`
	SeasonName               string            `json:"SeasonName,omitempty"`
	ImageTags                map[string]string `json:"ImageTags"`
	BackdropImageTags        []string          `json:"BackdropImageTags"`
	ParentBackdropImageTags  []string          `json:"ParentBackdropImageTags"`
	PrimaryImageAspectRatio  float64           `json:"PrimaryImageAspectRatio,omitempty"`
	UserData                 *userItemData     `json:"UserData"`
	MediaSources             []mediaSourceDTO  `json:"MediaSources,omitempty"`
	LocationType             string            `json:"LocationType"`
	IsHD                     bool              `json:"IsHD"`
	MediaStreams             []mediaStreamDTO  `json:"MediaStreams,omitempty"`
	Chapters                 []any             `json:"Chapters,omitempty"`
	People                   []personDTO       `json:"People,omitempty"`
	Studios                  []any             `json:"Studios,omitempty"`
}

// personDTO mirrors Jellyfin's BaseItemPerson. ImageUrl is an extra field used
// by the bundled web client because Nuvio has no person library items.
type personDTO struct {
	Name            string `json:"Name"`
	Id              string `json:"Id"`
	Role            string `json:"Role,omitempty"`
	Type            string `json:"Type"`
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
	ImageUrl        string `json:"ImageUrl,omitempty"`
}

type mediaSourceDTO struct {
	Protocol                   string           `json:"Protocol"`
	Id                         string           `json:"Id"`
	Path                       string           `json:"Path"`
	Type                       string           `json:"Type"`
	Container                  string           `json:"Container"`
	Size                       int64            `json:"Size,omitempty"`
	Name                       string           `json:"Name"`
	IsRemote                   bool             `json:"IsRemote"`
	ETag                       string           `json:"ETag,omitempty"`
	RunTimeTicks               int64            `json:"RunTimeTicks,omitempty"`
	ReadAtNativeFramerate      bool             `json:"ReadAtNativeFramerate"`
	IgnoreDts                  bool             `json:"IgnoreDts"`
	IgnoreIndex                bool             `json:"IgnoreIndex"`
	GenPtsInput                bool             `json:"GenPtsInput"`
	SupportsTranscoding        bool             `json:"SupportsTranscoding"`
	SupportsDirectStream       bool             `json:"SupportsDirectStream"`
	SupportsDirectPlay         bool             `json:"SupportsDirectPlay"`
	IsInfiniteStream           bool             `json:"IsInfiniteStream"`
	RequiresOpening            bool             `json:"RequiresOpening"`
	RequiresClosing            bool             `json:"RequiresClosing"`
	RequiresLooping            bool             `json:"RequiresLooping"`
	SupportsProbing            bool             `json:"SupportsProbing"`
	HasSegments                bool             `json:"HasSegments"`
	MediaStreams               []mediaStreamDTO `json:"MediaStreams"`
	Bitrate                    int64            `json:"Bitrate,omitempty"`
	DefaultAudioStreamIndex    int              `json:"DefaultAudioStreamIndex,omitempty"`
	DefaultSubtitleStreamIndex int              `json:"DefaultSubtitleStreamIndex"`
	DirectStreamUrl            string           `json:"DirectStreamUrl,omitempty"`
	TranscodingUrl             string           `json:"TranscodingUrl,omitempty"`
	TranscodingSubProtocol     string           `json:"TranscodingSubProtocol"`
	TranscodingContainer       string           `json:"TranscodingContainer,omitempty"`
	Formats                    []string         `json:"Formats,omitempty"`
}

type mediaStreamDTO struct {
	Type                   string  `json:"Type"`
	Index                  int     `json:"Index"`
	Codec                  string  `json:"Codec,omitempty"`
	Title                  string  `json:"Title,omitempty"`
	Language               string  `json:"Language,omitempty"`
	DisplayTitle           string  `json:"DisplayTitle"`
	DisplayLanguage        string  `json:"DisplayLanguage,omitempty"`
	IsInterlaced           bool    `json:"IsInterlaced"`
	IsDefault              bool    `json:"IsDefault"`
	IsForced               bool    `json:"IsForced"`
	IsHearingImpaired      bool    `json:"IsHearingImpaired"`
	IsOriginal             bool    `json:"IsOriginal"`
	IsExternal             bool    `json:"IsExternal"`
	Height                 int     `json:"Height,omitempty"`
	Width                  int     `json:"Width,omitempty"`
	BitRate                int64   `json:"BitRate,omitempty"`
	Channels               int     `json:"Channels,omitempty"`
	ChannelLayout          string  `json:"ChannelLayout,omitempty"`
	VideoRange             string  `json:"VideoRange,omitempty"`
	DeliveryMethod         string  `json:"DeliveryMethod,omitempty"`
	DeliveryUrl            string  `json:"DeliveryUrl,omitempty"`
	SupportsExternalStream bool    `json:"SupportsExternalStream"`
	IsTextSubtitleStream   bool    `json:"IsTextSubtitleStream"`
	PixelFormat            string  `json:"PixelFormat,omitempty"`
	RealFrameRate          float64 `json:"RealFrameRate,omitempty"`
	Profile                string  `json:"Profile,omitempty"`
	AverageFrameRate       float64 `json:"AverageFrameRate,omitempty"`
}

// QueryResult is the Jellyfin paged envelope.
type queryResult struct {
	Items            []itemDTO `json:"Items"`
	TotalRecordCount int       `json:"TotalRecordCount"`
	StartIndex       int       `json:"StartIndex"`
}

// Builders ----------------------------------------------------------------

func imageTag(it *model.Item, kind string) string {
	seed := it.ArtSeed
	if seed == "" {
		seed = it.ID
	}
	k := strings.ToLower(kind)
	switch k {
	case "primary":
		if it.PosterPath != "" {
			seed += ":poster-file"
		}
		if it.PosterURL != "" {
			seed += ":url:" + it.PosterURL
		}
	case "backdrop":
		if it.BackdropPath == "" {
			seed += ":backdrop"
		}
		if it.BackdropURL != "" {
			seed += ":url:" + it.BackdropURL
		}
	case "logo":
		if it.LogoPath != "" {
			seed += ":logo-file"
		}
	case "thumb":
		if it.ThumbPath != "" {
			seed += ":thumb-file"
		} else if it.BackdropURL != "" {
			seed += ":url:" + it.BackdropURL
		}
	}
	sum := md5.Sum([]byte("image:" + seed + ":" + k))
	return hex.EncodeToString(sum[:])[:16]
}

func personID(name string) string {
	sum := md5.Sum([]byte("person:" + strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(sum[:])
}

func (a *API) buildItem(it *model.Item, userID string, includeMedia bool, token string) itemDTO {
	childIDs := a.Catalog.Children(it.ID)
	isFolder := it.Type == model.TypeSeries || it.Type == model.TypeSeason
	dto := itemDTO{
		Name:                    it.Name,
		ServerId:                a.Store.ServerID(),
		Id:                      it.ID,
		DateCreated:             it.AddedAt.UTC().Format("2006-01-02T15:04:05.0000000Z"),
		Container:               it.Container,
		SortName:                it.SortName,
		PremiereDate:            it.PremiereDate,
		ProductionYear:          it.Year,
		IsFolder:                isFolder,
		ParentId:                it.ParentID,
		Type:                    it.Type,
		Overview:                it.Overview,
		Taglines:                it.Taglines,
		Genres:                  it.Genres,
		CommunityRating:         it.Rating,
		OfficialRating:          it.OfficialRating,
		RunTimeTicks:            it.RuntimeTicks,
		Size:                    it.Size,
		Path:                    it.Path,
		PlayAccess:              "Full",
		CanDownload:             it.Media != nil,
		SeriesId:                it.SeriesID,
		SeriesName:              it.SeriesName,
		SeasonId:                it.SeasonID,
		SeasonName:              it.SeasonName,
		ImageTags:               map[string]string{},
		BackdropImageTags:       []string{},
		ParentBackdropImageTags: []string{},
		LocationType:            "FileSystem",
		UserData:                a.buildUserData(it, userID),
	}
	if it.Demo {
		dto.LocationType = "Virtual"
	}
	if it.PosterPath != "" || it.PosterURL != "" || it.Type == model.TypeMovie || it.Type == model.TypeSeries ||
		it.Type == model.TypeSeason || it.Type == model.TypeEpisode || it.Type == "Person" || it.Type == "Genre" {
		dto.ImageTags["Primary"] = imageTag(it, "Primary")
		dto.PrimaryImageAspectRatio = 1
		if it.Type == model.TypeMovie || it.Type == model.TypeSeries || it.Type == model.TypeSeason || it.Type == model.TypeEpisode || it.Type == "Genre" {
			dto.PrimaryImageAspectRatio = 0.6667
		}
	}
	if it.BackdropPath != "" || it.Type == model.TypeMovie || it.Type == model.TypeSeries {
		dto.BackdropImageTags = append(dto.BackdropImageTags, imageTag(it, "Backdrop"))
	}
	if it.LogoPath != "" {
		dto.ImageTags["Logo"] = imageTag(it, "Logo")
	}
	if it.ThumbPath != "" {
		dto.ImageTags["Thumb"] = imageTag(it, "Thumb")
	}
	// Seasons and episodes inherit the series artwork so native clients can
	// fall back to it when the episode has no dedicated image.
	if it.SeriesID != "" && (it.Type == model.TypeSeason || it.Type == model.TypeEpisode) {
		if series, ok := a.Catalog.Get(it.SeriesID); ok {
			dto.SeriesPrimaryImageTag = imageTag(series, "Primary")
			dto.ParentPrimaryImageItemId = series.ID
			dto.ParentBackdropItemId = series.ID
			if series.BackdropPath != "" || series.Type == model.TypeSeries {
				dto.ParentBackdropImageTags = append(dto.ParentBackdropImageTags, imageTag(series, "Backdrop"))
			}
		}
	}
	if includeMedia && len(it.People) > 0 {
		dto.People = make([]personDTO, 0, len(it.People))
		for _, p := range it.People {
			typ := p.Type
			if typ == "" {
				typ = "Actor"
			}
			dto.People = append(dto.People, personDTO{
				Name:     p.Name,
				Id:       personID(p.Name),
				Role:     p.Role,
				Type:     typ,
				ImageUrl: p.ImageURL,
			})
		}
	}
	if it.Type == model.TypeMovie || it.Type == model.TypeEpisode {
		dto.MediaType = "Video"
		dto.VideoType = "VideoFile"
		dto.IsHD = it.Media != nil && it.Media.Height >= 720
		if it.Media != nil {
			dto.FileName = fileName(it.Path)
		}
	}
	if it.Type == model.TypeSeries || it.Type == model.TypeSeason {
		dto.ChildCount = len(childIDs)
		dto.RecursiveItemCount = len(childIDs)
	}
	if it.SeasonNumber > 0 || it.Type == model.TypeSeason {
		v := it.SeasonNumber
		dto.IndexNumber = &v
	}
	if it.Type == model.TypeEpisode {
		v := it.EpisodeNumber
		dto.IndexNumber = &v
		sv := it.SeasonNumber
		dto.ParentIndexNumber = &sv
	}
	if includeMedia && it.Media != nil {
		var viewer *model.User
		if userID != "" {
			viewer, _ = a.Store.FindUser(userID)
		}
		src := a.buildMediaSource(it, token, nil, "", viewer)
		dto.MediaSources = []mediaSourceDTO{src}
		dto.MediaStreams = src.MediaStreams
	}
	return dto
}

func (a *API) buildUserData(it *model.Item, userID string) *userItemData {
	if userID == "" {
		return &userItemData{ItemId: it.ID, Key: it.ID}
	}
	ud := a.Store.GetUserData(userID, it.ID)
	out := &userItemData{
		ItemId:                it.ID,
		PlaybackPositionTicks: ud.PositionTicks,
		PlayCount:             ud.PlayCount,
		IsFavorite:            ud.Favorite,
		Played:                ud.Played,
		Key:                   it.ID,
	}
	if !ud.LastPlayed.IsZero() {
		t := ud.LastPlayed
		out.LastPlayedDate = &t
	}
	duration := it.RuntimeTicks
	if it.Media != nil && it.Media.DurationTicks > 0 {
		duration = it.Media.DurationTicks
	}
	if duration > 0 && ud.PositionTicks > 0 {
		out.PlayedPercentage = float64(ud.PositionTicks) / float64(duration) * 100
	}
	if it.Type == model.TypeSeries || it.Type == model.TypeSeason {
		total, played := 0, 0
		for _, child := range a.Catalog.Children(it.ID) {
			eps := []*model.Item{}
			if child.Type == model.TypeEpisode {
				eps = append(eps, child)
			} else if child.Type == model.TypeSeason {
				for _, e := range a.Catalog.Children(child.ID) {
					if e.Type == model.TypeEpisode {
						eps = append(eps, e)
					}
				}
			}
			for _, ep := range eps {
				total++
				if a.Store.GetUserData(userID, ep.ID).Played {
					played++
				}
			}
		}
		out.Played = total > 0 && played == total
		out.UnplayedItemCount = total - played
	}
	return out
}

// viewItem is a synthetic catalog entry for a library view. Jellyfin clients
// treat CollectionFolders as regular items for details and artwork, so both
// endpoints resolve them through this helper.
func viewItem(id, name string) *model.Item {
	return &model.Item{
		ID: id, Type: "CollectionFolder", Name: name, SortName: name,
		ArtSeed: "view:" + id, LibraryID: id,
	}
}

// rootViewItem is the virtual root ("My Media") shown above the libraries.
func rootViewItem(name string) *model.Item {
	return &model.Item{
		ID: rootItemID, Type: "CollectionFolder", Name: name, SortName: name,
		ArtSeed: "view:" + rootItemID,
	}
}

func buildViewDTO(lib *model.Library, count int, serverID, userID string) itemDTO {
	collection := lib.Type
	if collection == model.LibraryMixed {
		// "mixed" is a Nuvio concept, not a Jellyfin CollectionType. Omit it
		// so native clients do not receive an unknown enum value.
		collection = ""
	}
	dto := itemDTO{
		Name:                    lib.Name,
		ServerId:                serverID,
		Id:                      lib.ID,
		DateCreated:             lib.AddedAt.UTC().Format("2006-01-02T15:04:05.0000000Z"),
		SortName:                lib.Name,
		IsFolder:                true,
		Type:                    "CollectionFolder",
		CollectionType:          collection,
		ChildCount:              count,
		RecursiveItemCount:      count,
		ImageTags:               map[string]string{"Primary": imageTag(viewItem(lib.ID, lib.Name), "Primary")},
		BackdropImageTags:       []string{},
		ParentBackdropImageTags: []string{},
		UserData:                &userItemData{ItemId: lib.ID, Key: lib.ID},
		PlayAccess:              "Full",
		CanDownload:             false,
		LocationType:            "FileSystem",
	}
	return dto
}

func fileName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (a *API) mediaSourceID(it *model.Item) string {
	sum := md5.Sum([]byte("ms:" + it.ID))
	return hex.EncodeToString(sum[:])
}

func (a *API) subtitleURL(it *model.Item, index int, codec, token string) string {
	format := "srt"
	if codec == "webvtt" {
		format = "vtt"
	}
	u := fmt.Sprintf("/Videos/%s/%s/Subtitles/%d/Stream.%s", it.ID, a.mediaSourceID(it), index, format)
	if token != "" {
		u += "?api_key=" + url.QueryEscape(token)
	}
	return u
}
