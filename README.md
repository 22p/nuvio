# Nuvio

一个用 Go 编写的 **Jellyfin 兼容**自托管流媒体服务器。自带现代响应式 Web 客户端，同时把核心 Jellyfin HTTP 接口开放给现有的手机、电视和桌面客户端使用。

不实现插件系统。目标很明确：**扫描自己的媒体、用浏览器或第三方 Jellyfin 客户端登录、看片、记住进度。**

## 功能

- **Web 客户端**：首页、继续观看、片库、收藏、搜索、剧集详情、播放器、设置；桌面与移动端响应式，支持 English / 简体中文 / 繁體中文 / 日本語 / 한국어。可安装为 PWA（需要 HTTPS 或 localhost），离线时仍能打开界面外壳。
- **Jellyfin 兼容**：按 12.4.0 实现登录、浏览、搜索、`PlaybackInfo`、直连串流、HLS、字幕、进度与收藏等核心接口，第三方客户端可直接连接。
- **媒体库**：电影、剧集、混合库；支持 `Show/Season 01/S01E01`、扁平目录、NFO 与本地图片。普通扫描会跳过未修改文件的 ffprobe，也可以手动全量扫描。
- **播放**：直连优先，其次转封装（`-c copy`），最后 HLS 转码；可选 VAAPI / QSV / Vulkan 硬件加速，失败自动回退软件。
- **字幕**：自动发现 `.srt` / `.vtt` / `.ass` / `.ssa`，可提取内嵌字幕；网页统一按 WebVTT 渲染，支持样式偏好与 0.1 秒时间轴微调。
- **账户与存储**：管理员/普通用户、可见媒体库与转码权限、登录设备管理；SQLite（WAL、纯 Go、无 CGO）。
- **AI（可选）**：兼容 OpenAI 的接口，用于元数据识别、字幕翻译和首页主题片单；默认关闭，只发送文本。

## 文档

| 文档 | 内容 |
| --- | --- |
| [媒体目录与显示规则](docs/media-layout.md) | 库类型、目录结构、季集编号、花絮、图片、NFO、网页显示顺序 |
| [元数据](docs/metadata.md) | 本地 NFO、TMDb 匹配、Provider ID、手动匹配、AI 识别 |
| [字幕](docs/subtitles.md) | 外挂与内嵌字幕、语言识别、ASS 过滤、AI 翻译 |
| [播放与转码](docs/playback.md) | 直连 / 转封装 / 转码、硬件加速、缓存 |
| [AI](docs/ai.md) | 兼容 OpenAI 的接口与三项 AI 功能 |
| [配置](docs/configuration.md) | 环境变量、数据目录、设置页、备份 |
| [部署](docs/deployment.md) | 源码构建、Docker、Podman、硬件直通、反向代理 |
| [第三方客户端](docs/jellyfin-clients.md) | 连接方式、接口范围、未实现的功能 |

完整索引见 [`docs/`](docs/README.md)。

## 快速开始

要求：Go 1.27+；构建 Web 界面需要 Node.js 24+。播放时可选安装 `ffmpeg` / `ffprobe`。

```bash
make build          # 安装前端依赖、构建 Web、编译单文件二进制
./nuvio             # 默认 http://localhost:8096，数据写入 ./data
```

如果 8096 已被 Jellyfin 占用：

```bash
./nuvio -addr :8097
```

首次打开网页会进入初始化页，创建管理员账号后即可进入内置演示片库。管理员可以在 `http://localhost:8096/users` 管理账号与权限。

开发模式：

```bash
go run ./cmd/nuvio -addr :8096          # 终端 1
cd web/ui && npm run dev                # 终端 2（默认代理到 :8096）
```

## 部署

```bash
docker build -t nuvio .
docker run -d --name nuvio -p 8096:8096 -v nuvio-data:/data nuvio
```

媒体目录、`/dev/dri` 硬件直通、Podman Quadlet、独立缓存盘和反向代理见 [部署](docs/deployment.md)。

## 反向代理

公网访问、需要 HTTPS 或要安装 PWA 时，建议把 Nuvio 放在反向代理后面。Caddy 会自动申请证书：

```caddy
nuvio.example.com {
    reverse_proxy 127.0.0.1:8096 {
        flush_interval -1
    }
}
```

代理需要保留 `Authorization` 和 `Range`（并返回 `206`），关闭响应缓冲，把真实客户端 IP 写入 `X-Forwarded-For`。同机部署时让 Nuvio 只监听回环地址，避免绕过代理访问 8096：

```bash
./nuvio -addr 127.0.0.1:8096
```

完整的 Caddy / Nginx 配置和超时、缓存注意事项见 [部署](docs/deployment.md#反向代理)。

## 连接第三方客户端

在 Jellyfin 手机、Android TV 或桌面客户端中添加：

```
http://<服务器地址>:8096
```

用 Nuvio 网页里创建的同一账号登录。对外声明的接口版本为 `12.4.0`，实现的是核心子集；插件、Live TV、音乐库、Quick Connect、下载/离线同步等没有实现，客户端会按 404 或空结果降级。直连播放现在需要 token，不能把不带 `ApiKey` 的裸 URL 直接交给系统播放器。接口清单见 [第三方客户端](docs/jellyfin-clients.md)。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NUVIO_ADDR` | `:8096` | HTTP 监听地址 |
| `NUVIO_DATA` | `./data` | 数据目录（数据库、缓存、字幕） |
| `NUVIO_DEMO` | `1` | 首次初始化是否加入演示片库 |
| `NUVIO_FFMPEG` | 自动检测 | ffmpeg 路径 |
| `NUVIO_FFPROBE` | 自动检测 | ffprobe 路径 |
| `NUVIO_TMDB_API_KEY` | 空 | TMDb v3 Key |

对应命令行参数：`-addr`、`-data`、`-demo=false`、`-ffmpeg`、`-ffprobe`。设置页、数据目录和备份说明见 [配置](docs/configuration.md)。

## 安全

- Nuvio 只提供 HTTP，公网访问请放在 Caddy / Nginx 后并启用 HTTPS。
- `/Videos/{id}/stream` 需要有效的 `ApiKey` 查询参数或 `Authorization` 头；不再允许匿名拉流。第三方客户端如果只把裸 URL 交给播放器，需要能带上查询参数中的 token。
- 密码使用 PBKDF2-HMAC-SHA256 加盐哈希；数据目录包含数据库与 API Key，应限制访问权限。

## 演示素材

内置演示片库使用 Blender Foundation 的 **Sintel** 预告片（CC BY 3.0，© Blender Foundation，durian.blender.org），仅用于演示播放链路。其余标题、简介与封面均为虚构或程序生成。

## 许可证

源代码采用 [MIT 许可证](LICENSE)。内置演示片段按 **CC BY 3.0** 提供，不适用 MIT；依赖项保留各自的 MIT、BSD-3-Clause、Apache-2.0 与 SIL OFL 1.1 许可证。
