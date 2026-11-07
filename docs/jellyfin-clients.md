# 第三方客户端

Nuvio 对客户端声明 Jellyfin API 版本 **12.4.0**，实现的是核心子集。登录、浏览、播放、进度和收藏不依赖未实现的功能；依赖高级能力的入口会按 404 或空结果降级。

## 连接

在 Jellyfin 手机、Android TV、桌面客户端里添加服务器：

```
http://<服务器地址>:8096
```

用 Nuvio 网页里创建的同一账号登录。没有单独的「客户端密码」，也不支持 Quick Connect。

如果前面有反向代理：

- 保留 `Authorization`、`X-Emby-Token`、`X-Emby-Authorization` 请求头
- 保留 `Range` 并正确返回 `206`，否则拖动进度会失效
- 不要把 HLS 分片缓冲到内存；边下载边发即可

## 认证

| 接口 | 说明 |
| --- | --- |
| `POST /Users/AuthenticateByName` | 用户名 + 密码登录，返回 `AccessToken` |
| `GET /Users/Me` | 当前用户 |
| `GET /Users/Public` | 登录页可用的公开用户列表 |
| `GET /Users/{id}` | 用户详情与策略 |

令牌可以通过以下任一方式发送：

```
Authorization: MediaBrowser Token="<token>"
X-Emby-Token: <token>
?api_key=<token>
```

令牌是持久化的，重启服务不会失效；在「设置 → 登录设备」撤销后需要重新登录。

## 已实现的接口

| 领域 | 接口 |
| --- | --- |
| 系统 | `/System/Info/Public`、`/System/Info`、`/Branding/Configuration` |
| 认证 | `/Users/AuthenticateByName`、`/Users/Me`、`/Users/Public`、`/Users/{id}` |
| 浏览 | `/UserViews`、`/Items`、`/Items/Latest`、`/UserItems/Resume`、`/Items/{id}`、`/Items/{id}/Similar`、`/Genres`、`/Persons` |
| 剧集 | `/Shows/{id}/Seasons`、`/Shows/{id}/Episodes`、`/Shows/NextUp` |
| 播放 | `/Items/{id}/PlaybackInfo`、`/Videos/{id}/stream`、`/Videos/{id}/master.m3u8`、HLS 分片、字幕 |
| 进度 | `/Sessions/Playing`、`/Sessions/Playing/Progress`、`/Sessions/Playing/Stopped`、`/Sessions/Capabilities/Full` |
| 用户数据 | `/UserPlayedItems/{itemId}`、`/UserFavoriteItems/{itemId}`、`/UserItems/{itemId}/UserData` |
| 图片 | `/Items/{id}/Images/{Primary|Backdrop|Logo|Thumb}` |
| 花絮 | `/Items/{id}/SpecialFeatures` |
| AI 片单 | `/Items/AI/Lists`（Nuvio 自有，不是 Jellyfin 标准） |

兼容别名：

- `/Users/{id}/Views`
- `/Users/{id}/Items`
- `/Users/{id}/PlayedItems`
- `/Users/{id}/FavoriteItems`

路由大小写不敏感：`/items/{id}/Images/Primary` 与 `/Items/...` 等价。未实现的路径返回 404 JSON，而不是网页。

## 播放

`PlaybackInfo` 返回的 `MediaSources` 会包含：

- 容器、大小、码率、时长
- 视频 / 音频 / 字幕流
- `SupportsDirectPlay`、`SupportsDirectStream`、`SupportsTranscoding`
- 直连时的 `DirectStreamUrl`
- 需要转码/转封装时的 `TranscodingUrl`

服务端按客户端提交的 `DeviceProfile` 决定直连、转封装或转码，见 [播放与转码](playback.md)。

`/Videos/{id}/stream` 允许匿名访问：客户端会把 URL 直接交给系统播放器，Findroid 等不会附带令牌。知道条目 ID 就能拉流，公网部署请自行加访问控制。

`/Videos/{id}/master.m3u8` 和字幕仍然需要鉴权。

## 下载与离线

下载**没有实现**，因此客户端不会显示下载按钮：

- 用户策略 `EnableContentDownloading = false`
- 用户策略 `EnableSyncTranscoding = false`
- 条目 `CanDownload = false`
- 没有 `/Items/{id}/Download` 路由

字幕文件仍然可以下载（`EnableSubtitleDownloading = true`），它们通过标准的字幕流地址提供。

如果某个客户端自己忽略这些标志、仍然调用下载接口，会得到 404。

## 未实现的功能

| 功能 | 表现 |
| --- | --- |
| 插件系统 | 不实现 |
| Live TV / 录制 | 404 |
| 音乐库 | 不支持音乐类型 |
| Quick Connect | 404，请用账号密码登录 |
| 远程控制 / 投屏 | 仅上报播放会话，不实现控制命令 |
| MediaSegments / 跳过片头 | 空结果或 404，客户端不显示按钮 |
| Trickplay 缩略图 | 404，进度条没有预览图 |
| Collections / BoxSet | 「合集」页为空 |
| Playlists | 无 `/Playlists` 路由 |
| 多版本 | 一个文件一个条目，不提供版本选择 |
| 高级筛选 | `studioIds`、`tags`、`officialRatings` 等参数被忽略 |
| 下载 / 离线同步 | 见上一节 |

客户端通常会对这些情况降级，不影响播放。如果某个客户端把某个接口当作硬性依赖，表现可能是登录后空白页或播放按钮不可用；这时换一个客户端或反馈具体接口路径。

## 已知差异

- 剧集进度只汇总「已看 / 未看」和未看集数，不汇总播放位置；首页「继续观看」对剧集可能不显示进度条。
- 章节（`Chapters`）字段为空，客户端不会显示章节导航。
- 片尾没有「下一集」倒计时，自动连播由 Web 播放器自己的设置控制。
- 图形字幕（PGS/VobSub）没有 OCR，网页端不能显示；支持内嵌图形字幕的客户端可以直接解码。
- 同一个 HLS 会话不按用户共享缓存；多人看同一集会有多份转码缓存。
