export interface UserData {
  PlaybackPositionTicks: number
  Played: boolean
  PlayedPercentage?: number
  UnplayedItemCount?: number
  IsFavorite: boolean
  PlayCount: number
  LastPlayedDate?: string
}

export interface MediaStream {
  Type: 'Video' | 'Audio' | 'Subtitle' | string
  Index: number
  Codec?: string
  Language?: string
  DisplayTitle?: string
  DisplayLanguage?: string
  Width?: number
  Height?: number
  BitRate?: number
  Channels?: number
  ChannelLayout?: string
  IsDefault?: boolean
  IsForced?: boolean
  IsExternal?: boolean
  DeliveryUrl?: string
  DeliveryMethod?: string
  Profile?: string
  RealFrameRate?: number
  AverageFrameRate?: number
}

export interface MediaSource {
  Id: string
  Container: string
  Name?: string
  Path?: string
  Size?: number
  RunTimeTicks?: number
  Bitrate?: number
  SupportsDirectPlay?: boolean
  SupportsDirectStream?: boolean
  SupportsTranscoding?: boolean
  DirectStreamUrl?: string
  TranscodingUrl?: string
  MediaStreams?: MediaStream[]
}

export interface Person {
  Name: string
  Id?: string
  Role?: string
  Type?: string
  ImageUrl?: string
  PrimaryImageTag?: string
}

export interface JFItem {
  Id: string
  Name: string
  Type: 'Movie' | 'Series' | 'Season' | 'Episode' | 'CollectionFolder' | string
  CollectionType?: string
  Overview?: string
  Taglines?: string[]
  ProductionYear?: number
  PremiereDate?: string
  RunTimeTicks?: number
  CommunityRating?: number
  OfficialRating?: string
  Genres?: string[]
  ImageTags?: Record<string, string>
  BackdropImageTags?: string[]
  PrimaryImageAspectRatio?: number
  ParentId?: string
  SeriesId?: string
  SeriesName?: string
  SeasonId?: string
  SeasonName?: string
  IndexNumber?: number | null
  ParentIndexNumber?: number | null
  ChildCount?: number
  RecursiveItemCount?: number
  UserData?: UserData
  MediaSources?: MediaSource[]
  MediaStreams?: MediaStream[]
  People?: Person[]
  Container?: string
  Path?: string
  LocationType?: string
  IsFolder?: boolean
  Size?: number
}

export interface QueryResult<T> {
  Items: T[]
  TotalRecordCount: number
  StartIndex: number
}

export interface PlaybackInfo {
  MediaSources: MediaSource[]
  PlaySessionId: string
}

export interface JFUser {
  Id: string
  Name: string
  Policy?: { IsAdministrator: boolean }
  HasPassword?: boolean
}

export interface AuthResult {
  User: JFUser
  AccessToken: string
  ServerId: string
}

export interface AppConfig {
  serverName: string
  version: string
  apiVersion: string
  setupComplete: boolean
  ffmpeg: boolean
  ffprobe: boolean
  demo: boolean
  tmdb?: boolean
  scanning: boolean
  itemCount: number
  serverId: string
}

export interface ScanStatus {
  scanning: boolean
  lastScan: string
  lastError?: string
  itemCount: number
}

export interface LibraryInfo {
  id: string
  name: string
  type: 'movies' | 'tvshows' | 'mixed'
  paths: string[]
  demo: boolean
  itemCount: number
}

export interface AdminUser {
  id: string
  name: string
  isAdmin: boolean
  hasPassword?: boolean
  allowedLibraries: string[]
  allowTranscode: boolean
  allowRemux: boolean
  createdAt: string
}

export interface AdminOverview {
  serverName: string
  version: string
  apiVersion: string
  dataPath: string
  dbPath?: string
  demo: boolean
  tmdb?: boolean
  ffmpeg: string
  ffprobe: string
  encoder?: string
  hardware?: string
  scan: ScanStatus
  counts: Record<string, number>
  libraries: LibraryInfo[]
  users: AdminUser[]
  settings: {
    serverName: string
    transcodeEnabled: boolean
    remuxEnabled: boolean
    maxBitrateMbps: number
    transcodePreset: string
    hardwareAccel: string
    ffmpegPath?: string
    ffprobePath?: string
    tmdbApiKey?: string
    metadataLanguage?: string
  }
}
