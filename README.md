# Nuvio

一个用 Go 编写的、**Jellyfin 兼容**的自托管流媒体服务器。自带现代响应式 Web 客户端，同时把核心 Jellyfin HTTP 接口开放给现有的手机、电视和桌面客户端使用。

不实现插件系统。目标很明确：**扫描你自己的媒体、用浏览器或官方 Jellyfin 客户端登录、看片、记住进度。**

## 功能

- **现代 Web 界面**：桌面与移动端响应式布局，首页推荐、继续观看、接下来、片库、收藏、搜索、剧集详情、播放器、设置。收藏页集中展示所有已收藏的电影与剧集。
- **多语言界面**：English、简体中文、繁體中文、日本語、한국어。英文为源语言，所有文案通过 `t()` 查表；可在「设置 → 界面语言」或登录页切换，浏览器语言会自动作为初始值（`zh-Hant` / `zh-TW` 自动使用繁體中文）。
- **英文优先的服务端**：API 错误、扫描日志、演示库名称、语言名称等响应一律使用英文原文，前端词典直接以这些英文 key 翻译，无需在客户端做中文替换。
- **Jellyfin 兼容接口**：按 Jellyfin 12.4.0 接口实现登录、用户视图（`/UserViews`）、媒体库浏览（`/Items`）、最近添加（`/Items/Latest`）、继续观看（`/UserItems/Resume`）、搜索、详情、图片、`PlaybackInfo`、直连串流（HTTP Range）、HLS 转码、外挂字幕、播放进度上报、已看/收藏（`/UserPlayedItems`、`/UserFavoriteItems`）、`/Genres`、`/Persons` 与显示偏好持久化。
- **媒体扫描**：电影目录、剧集 `Show/Season 01/S01E01`、扁平目录、电影/剧集混合库；从文件名解析标题与年份。读取同目录下的 `movie.nfo` / `剧集名.nfo`（标题、简介、评分、分级、类型、演员）以及 `folder.jpg`、`backdrop.jpg`、`landscape.jpg`、`logo.png`，本地元数据与图片优先于在线数据。扫描结果写入 SQLite，重启后直接读取，不会重复扫描。
- **在线元数据（可选）**：填写 TMDb API Key 后，重新扫描会补全海报、背景图、简介、评分、类型与演员表；剧集海报和演员会继承给季和单集，图片经服务器代理并缓存到磁盘。
- **字幕**：自动发现同名字幕 `.srt` / `.vtt` / `.ass` / `.ssa`，统一转换为 WebVTT；ffmpeg 可提取内嵌字幕；支持语言、字号、颜色、背景、描边等偏好。
- **播放**：浏览器兼容格式直连播放；容器不兼容但编码兼容（H.264 + AAC/MP3）时优先**转封装**（`-c copy`，不重新编码）；只有编码或码率不兼容时才转码为 HLS（输出编码可选 H.264、HEVC、AV1、VP9）。可选用 VAAPI（Intel / AMD）或 QSV（Intel）硬件解码与编码，失败自动回退软件。播放器支持音轨切换、字幕、0.5×–2× 倍速，以及实时播放信息（源/输出分辨率、编解码器、帧率、码率、缓冲、丢帧、HLS 档位、会话）。
- **账户与权限**：管理员/普通用户、PBKDF2-SHA256 密码哈希、持久化访问令牌；独立「用户」页面可为每个账号设置可见媒体库、转码、转封装，并支持重置密码与删除账号。设置页的「登录设备」会记录每个令牌的设备名、客户端、IP 与最近活跃时间，可撤销其他设备——被撤销的设备需要重新登录，当前设备保留。
- **自生成封面**：没有海报的条目会生成确定性的抽象封面与背景图，媒体库开箱即用。
- **SQLite 存储**：用户、令牌、媒体库、片库索引与观看进度统一存放在 `data/nuvio.db`（WAL 模式，纯 Go 驱动，无需 CGO）。
- **演示片库**：首次运行自动创建，可随时删除。

## 快速开始

要求：Go 1.24+；构建 Web 界面需要 Node.js 20+。播放时可选安装 `ffmpeg` / `ffprobe`。

```bash
make build          # 安装前端依赖、构建 Web、编译单文件二进制
./nuvio             # 默认 http://localhost:8096，数据写入 ./data
```

如果 8096 已被 Jellyfin 占用，换一个端口即可：

```bash
./nuvio -addr :8097
```

首次打开网页会进入初始化页，创建管理员账号后即可进入内置演示片库。

界面语言可在设置页切换。管理员还可以打开 `http://localhost:8096/users` 管理账号与权限；旧的 `/settings#users` 会自动跳转到该页面。

开发模式：

```bash
go run ./cmd/nuvio -addr :8096          # 终端 1
cd web/ui && npm run dev                # 终端 2（默认代理到 :8096）
```

### Docker

```bash
docker build -t nuvio .
docker run -d --name nuvio -p 8096:8096 -v nuvio-data:/data nuvio
```

### 硬件转码（VAAPI / QSV）

Docker 镜像内置 Intel（`intel-media-driver` / `libva-intel-driver`）与 AMD（`mesa-va-gallium`）的 VAAPI 驱动，以及 Intel QSV 运行时 `libvpl` 和 `onevpl-intel-gpu`（仅 x86_64）。把宿主机的渲染节点传入容器即可：

```bash
docker run -d --name nuvio -p 8096:8096 \
  --device /dev/dri:/dev/dri \
  -v nuvio-data:/data nuvio
```

启动时会实际编码/解码一帧来验证能力，而不是只看设备节点是否存在：日志会打印 `Hardware acceleration: vaapi (/dev/dri/renderD128) encode h264, hevc, av1 / decode h264, hevc, av1, vp9`，设置 → 服务器 → 转码中也会逐后端展示可用的编码与解码格式。硬件加速可选 `Auto`、`VAAPI (Intel / AMD)`、`QSV (Intel)` 或 `Off`；输出编码可选 H.264、HEVC、AV1 和 VP9（仅列出当前 ffmpeg 与硬件确实支持的项目）。非 H.264 输出使用分片 MP4（fMP4）HLS，客户端必须支持对应编码；所选编码无可用硬件编码器时会回退到该编码的软件编码器，再回退到 H.264。硬件初始化失败或某个文件无法硬解时会自动回退到软件编码。转码会话 90 秒内没有收到播放列表或分片请求时会自动停止 ffmpeg，客户端再次请求时从上次分片继续，避免关闭播放器后进程常驻。宿主机需要安装对应的内核驱动（Intel `i915`/`xe`、AMD `amdgpu`）和 VAAPI 驱动（Debian/Ubuntu 可安装 `mesa-va-drivers`、`intel-media-va-driver-non-free`），裸机运行时把运行用户加入 `render` 组。

## 连接官方 Jellyfin 客户端

在 Jellyfin 手机、Android TV 或桌面客户端中添加服务器：

```
http://<服务器地址>:8096
```

使用在 Nuvio 网页里创建的同一账号登录即可浏览和播放。Nuvio 对外声明的接口版本为 `12.4.0`（对齐官方最新稳定版），核心播放链路使用与官方客户端完全相同的 `PlaybackInfo` / `stream` / `Sessions` 接口，没有网页专用后门。这个版本号只是客户端的功能门槛：Nuvio 实现的是下表里的核心子集，12.x 新增的 MediaSegments、Trickplay、插件化搜索/推荐等没有实现，客户端会按 404 或空结果降级。

已实现的核心接口范围：

| 领域 | 接口 |
| --- | --- |
| 系统 | `/System/Info/Public`、`/System/Info`、`/Branding/Configuration` |
| 认证 | `/Users/AuthenticateByName`、`/Users/Me`、`/Users/Public`、`/Users/{id}` |
| 浏览 | `/UserViews`、`/Items`、`/Items/Latest`、`/UserItems/Resume`、`/Items/{id}`、`/Items/{id}/Similar`、`/Genres`、`/Persons` |
| 剧集 | `/Shows/{id}/Seasons`、`/Shows/{id}/Episodes`、`/Shows/NextUp` |
| 播放 | `/Items/{id}/PlaybackInfo`、`/Videos/{id}/stream`、`/Videos/{id}/master.m3u8`、HLS 分片、字幕 |
| 进度 | `/Sessions/Playing`、`/Sessions/Playing/Progress`、`/Sessions/Playing/Stopped`、`/Sessions/Capabilities/Full` |
| 用户数据 | `/UserPlayedItems/{itemId}`、`/UserFavoriteItems/{itemId}`、`/UserItems/{itemId}/UserData` |

接口以 Jellyfin **12.4.0** 路由为主，同时保留 10.x 的 `/Users/{id}/Views`、`/Users/{id}/Items`、`/Users/{id}/PlayedItems`、`/Users/{id}/FavoriteItems` 等兼容别名，旧版客户端（Filebar、JellyWatch 等）可继续使用；内置 Web 客户端使用新路径。

路由大小写不敏感（`/items/{id}/Images/Primary` 与 `/Items/...` 等价），未实现的 API 路径返回 404 JSON 而不是网页。与官方一致，`/Videos/{id}/stream` 允许匿名访问（客户端会把 URL 直接交给播放器，Findroid 等不会附带 token），`/Videos/{id}/master.m3u8` 和字幕仍需鉴权；知道条目 ID 即可拉流，公网部署请自行加访问控制。

**未实现**：插件、Live TV、音乐库、Quick Connect、远程控制/投屏、部分高级筛选参数（如 `studioIds`、`tags`、`officialRatings`）。如果你的客户端依赖其中某项，会得到 404 或空结果，但登录、浏览和视频播放不依赖它们。

## 媒体目录约定

电影库：

```
Movies/
  Arrival (2016)/
    Arrival (2016).mkv
    poster.jpg
    fanart.jpg
    Arrival (2016).zh.srt
```

剧集库：

```
Shows/
  Severance/
    poster.jpg
    Season 01/
      Severance S01E01.mkv
      Severance S01E01.zh-CN.srt
      Severance S01E02.mkv
```

也支持扁平结构 `Shows/Show.Name.S01E01.mkv`，以及 `Season 01/03 Episode Title.mkv` 这类不带 `SxxExx` 的写法。

- 海报文件名：`poster` / `folder` / `cover` / `default` / `movie`（`.jpg` `.png` `.webp`）。
- 背景图文件名：`fanart` / `backdrop` / `background`。
- 字幕语言从后缀识别，如 `.zh.srt`、`.en.forced.srt`、`.zh-CN.ass`。
- 没有图片时 Nuvio 会自动生成艺术封面；配置 TMDb 后优先使用在线海报（本地图片仍然优先）。

## 转码与 ffmpeg

直连播放永远优先。客户端无法直接播放时按以下顺序降级：

1. **转封装**：视频为 H.264、音频为 AAC/MP3，只是容器不被客户端支持（如 MKV）时，用 `-c copy` 直接打成 HLS，几乎不占 CPU，画质无损。
2. **转码**：编码或码率不兼容时，用 ffmpeg 重新编码为 H.264 + AAC 的 HLS。

转码相关行为：

- 自动选择可用的 H.264 编码器：`libx264` → `libopenh264` → `h264_v4l2m2m`。
- 服务端默认码率、编码预设可在「设置 → 服务器」中修改；转码与转封装也可分别全局关闭。
- 安装方式：`apt install ffmpeg` / `dnf install ffmpeg-free` / `brew install ffmpeg`。
- 也可以通过 `NUVIO_FFMPEG`、`NUVIO_FFPROBE` 环境变量或设置页填写绝对路径。

没有 ffmpeg 时服务器仍可运行，只是不能转码；浏览器通常仍能直连 MP4（H.264/AAC）。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NUVIO_ADDR` | `:8096` | HTTP 监听地址 |
| `NUVIO_DATA` | `./data` | 数据目录（用户、令牌、媒体库、转码缓存） |
| `NUVIO_DEMO` | `1` | 首次初始化时是否加入演示片库 |
| `NUVIO_FFMPEG` | 自动检测 | ffmpeg 路径 |
| `NUVIO_FFPROBE` | 自动检测 | ffprobe 路径 |
| `NUVIO_TMDB_API_KEY` | 空 | TMDb v3 API Key；也可在「设置 → 元数据」中填写 |

对应命令行参数：`-addr`、`-data`、`-demo=false`、`-ffmpeg`、`-ffprobe`。

## 元数据（TMDb）

在 [TMDb](https://www.themoviedb.org/settings/api) 申请免费 API Key，填入「设置 → 元数据」后保存，服务器会自动重新扫描：

- 按标题与年份匹配电影/剧集，匹配置信度不足时保留本地信息。
- 补全海报、背景图、简介、评分、类型与首播日期；剧集图片会继承给季和单集。
- 图片通过 `/Items/{id}/Images/{type}` 代理并缓存，客户端无需直接访问外网。
- 元数据语言可选 `zh-CN` / `zh-TW` / `en-US` / `ja-JP` 等。

本产品使用 TMDb API，但未获得 TMDb 认可或认证。

## 安全说明

- 密码使用 PBKDF2-HMAC-SHA256（210,000 次迭代）加盐哈希；数据库文件 `data/nuvio.db` 以 `0600` 权限写入。
- Nuvio 本身只提供 HTTP。若要从公网访问，请放在 Caddy/Nginx 后并启用 HTTPS。
- 关闭「首次运行」后没有注册入口；新增账号只能由管理员在设置页创建。
- `/users` 页面可以为每个账号勾选可见媒体库、允许转码、允许转封装；禁用后服务器不会为该账号启动对应的 ffmpeg 任务。
- 播放令牌等同于账号权限，不要分享带 `api_key` 的媒体链接。

## 项目结构

```
cmd/nuvio           入口
internal/server     路由、扫描编排、管理 API、SPA
internal/jellyfin   Jellyfin 兼容 API、播放决策、HLS/字幕/图片
internal/catalog    内存目录与查询
internal/media      扫描、探测、字幕转换
internal/hls        ffmpeg 转码会话
internal/store       SQLite：用户/令牌/媒体库/片库索引/进度
internal/metadata    TMDb 元数据补全
internal/demo       内置演示内容
internal/art        程序化封面
web/ui              React + TypeScript + Vite 客户端
```

## 演示素材

内置演示片库使用 Blender Foundation 的 **Sintel** 预告片（CC BY 3.0，© Blender Foundation，durian.blender.org），仅用于演示播放链路。其余标题、简介与封面均为虚构或程序生成。
