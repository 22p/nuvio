# 部署

## 从源码构建

要求：Go 1.27+；构建 Web 界面需要 Node.js 20+。

```bash
make build          # 安装前端依赖、构建 Web、编译单文件二进制
./nuvio             # 默认 http://localhost:8096，数据写入 ./data
```

如果 8096 已被 Jellyfin 占用：

```bash
./nuvio -addr :8097
```

开发模式：

```bash
go run ./cmd/nuvio -addr :8096          # 终端 1
cd web/ui && npm run dev                # 终端 2（默认代理到 :8096）
```

播放需要可选的 `ffmpeg` / `ffprobe`：

```bash
apt install ffmpeg        # Debian / Ubuntu
dnf install ffmpeg-free   # Fedora
brew install ffmpeg       # macOS
```

也可以设置 `NUVIO_FFMPEG`、`NUVIO_FFPROBE`，或在设置页填写绝对路径。没有 ffmpeg 时服务器仍可运行，只是不能转码。

## Docker

```bash
docker build -t nuvio .
docker run -d --name nuvio -p 8096:8096 -v nuvio-data:/data nuvio
```

挂载媒体目录和渲染节点：

```bash
docker run -d --name nuvio -p 8096:8096 \
  -v nuvio-data:/data \
  -v /opt/media:/video:ro \
  --device /dev/dri:/dev/dri \
  nuvio
```

在网页里把媒体库路径填容器内路径 `/video`，不是宿主机的 `/opt/media`。

## Podman（Quadlet）

rootless 放在 `~/.config/containers/systemd/nuvio.container`，系统级放在 `/etc/containers/systemd/`：

```ini
# ~/.config/containers/systemd/nuvio.container
[Unit]
Description=Nuvio media server

[Container]
Image=ghcr.io/22p/nuvio:latest
ContainerName=nuvio
AutoUpdate=registry
Environment=TZ=Asia/Shanghai
AddDevice=/dev/dri:/dev/dri
Volume=nuvio:/data
#Tmpfs=/data/transcode:rw,size=4g
#Volume=/opt/data:/video:ro,z
PublishPort=8096:8096

[Service]
Restart=always

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now nuvio   # 系统级去掉 --user，改用 sudo
```

- `Volume=nuvio:/data` 是命名卷。媒体目录用 `Volume=/opt/data:/video:ro,z` 挂进来后，网页里填 `/video`。
- SELinux 发行版上的 bind mount 需要 `:z`（共享）或 `:Z`（独占）。
- 转码缓存默认写在 `/data/transcode`。想单独用一块盘，直接嵌套挂载即可，Podman 会按容器内路径深度先挂父再挂子：

  ```ini
  Volume=/mnt/nvme/nuvio-cache:/data/transcode
  ```

  同时在 `[Unit]` 里加 `RequiresMountsFor=/mnt/nvme/nuvio-cache`，否则盘还没就绪时缓存会落进数据卷。
- `Tmpfs=/data/transcode:rw,size=4g` 可以完全不写盘，但 remux 会把整片切成 fMP4，4g 对高码率单集可能不够，按单集大小留余量或改用独立卷。
- rootless 下访问 `/dev/dri` 需要当前用户在 `render`（部分发行版是 `video`）组里。

## 硬件转码（VAAPI / QSV）

Docker 镜像内置 Intel（`intel-media-driver` / `libva-intel-driver`）与 AMD（`mesa-va-gallium`）的 VAAPI 驱动，以及 Intel QSV 运行时 `libvpl` 和 `onevpl-intel-gpu`（仅 x86_64）。把宿主机的渲染节点传入容器即可：

```bash
docker run -d --name nuvio -p 8096:8096 \
  --device /dev/dri:/dev/dri \
  -v nuvio-data:/data nuvio
```

启动时会实际编码/解码一帧来验证能力，而不是只看设备节点是否存在。日志会打印类似：

```
Hardware acceleration: vaapi (/dev/dri/renderD128) encode h264, hevc, av1 / decode h264, hevc, av1, vp9
```

设置 → 服务器 → 转码中也会逐后端展示可用的编码与解码格式。硬件加速可选 `Auto`、`VAAPI (Intel / AMD)`、`QSV (Intel)` 或 `Off`。

宿主机需要安装对应的内核驱动（Intel `i915`/`xe`、AMD `amdgpu`）和 VAAPI 驱动（Debian/Ubuntu 可安装 `mesa-va-drivers`、`intel-media-va-driver-non-free`）；裸机运行时把运行用户加入 `render` 组。

播放决策、编码器和缓存行为见 [播放与转码](playback.md)。

## 反向代理

Nuvio 只提供 HTTP。公网访问建议放在 Caddy / Nginx 后并启用 HTTPS。代理需要保留 `Authorization`、`X-Emby-Token`、`Range` 请求头，并正确返回 `206`。`/Videos/{id}/stream` 允许匿名访问，不要在代理层额外加鉴权，否则第三方客户端可能无法播放。细节见 [配置](configuration.md#反向代理)。

## 源码结构

```
cmd/nuvio           入口
internal/server     路由、扫描编排、管理 API、SPA
internal/jellyfin   Jellyfin 兼容 API、播放决策、HLS/字幕/图片
internal/catalog    内存目录与查询
internal/media      扫描、探测、字幕转换
internal/hls        ffmpeg 转码 / 转封装会话
internal/store      SQLite：用户/令牌/媒体库/片库索引/进度
internal/metadata   TMDb 元数据补全
internal/ai         OpenAI 兼容客户端
internal/demo       内置演示内容
internal/art        程序化封面
web/ui              React + TypeScript + Vite 客户端
docs                详细文档
```
