# 媒体目录与显示规则

Nuvio 不是按文件夹原样展示，而是扫描目录后生成条目：

```
媒体库 → 电影 / 剧集 → 季 → 单集
```

扫描时**库类型决定目录怎么解释**。同一个目录放进「电影库」和「剧集库」，结果可能完全不同，所以先确认库类型，再排查命名。

## 库类型

| 类型 | 接口值 | 目录里的视频会变成 |
| --- | --- | --- |
| 电影库 | `movies` | 每个视频一部电影；有 `SxxExx` 也不会拆成剧集 |
| 剧集库 | `tvshows` | 按剧集 → 季 → 单集分组；识别不到集号的视频归入第 1 季 |
| 混合库 | `mixed` | 能解析出集号的当剧集，其余每个视频当一部电影 |

混合库适合「电影和剧集混在一个盘」的场景，但有两个代价：

- 没有 `SxxExx` 的剧集文件会被当成电影，而不是「第 1 季」
- 剧集和电影同名时可能出现重名条目

能分开放就分开放。

## 电影库

推荐结构：

```
Movies/
  Arrival (2016)/
    Arrival (2016).mkv
    poster.jpg
    fanart.jpg
    Arrival (2016).zh.srt
```

也支持扁平结构：

```
Movies/
  Arrival (2016).mkv
```

规则：

- **一个视频文件 = 一部电影**。同一目录下的两个视频会变成两部电影（花絮目录除外，见下文）。
- 标题优先取文件名；以下情况改用**所在目录名**：
  - 文件名是 `movie`、`video`、`index`、`main`、`feature`、`film`、`title` 这类通用名
  - 目录名里带年份（例如 `Arrival (2016)`）
- 清洗规则：去掉 `1080p`、`BluRay`、`x265`、`WEB-DL` 等发布标记，去掉 `-GROUP` 后缀、`CHS`/`CHT` 等语言标记、`简繁`/`双语`/`第N季` 等中文噪声，再去掉括号内容。
- 年份从文件名或目录名里的 `(2016)`、`[2016]`、`.2016.` 读取。
- 如果目录或文件名里有 `{tmdb-329865}`、`[imdb-tt0111161]`，会直接使用该 ID 匹配 TMDb，不再靠标题猜。

示例：

```
Inception.2010.1080p.BluRay.x264-GROUP.mkv   →  Inception (2010)
The.Matrix.1999.1080p.BluRay.x264/           →  The Matrix (1999)
  The.Matrix.1999.1080p.BluRay.x264.mkv
```

## 剧集库

### 推荐结构

```
Shows/
  Severance/
    poster.jpg
    Season 01/
      Severance S01E01.mkv
      Severance S01E01.zh-CN.srt
      Severance S01E02.mkv
```

- 库根目录下的**第一层目录**就是剧集（`Shows/Severance`）。
- 第一层目录可以是季目录。如果库根目录直接指向一部剧：

  ```
  Severance/
    Season 01/
      S01E01.mkv
  ```

  把库路径设为 `Severance` 也能正确识别为一部剧。
- 剧集名取第一层目录名，去掉年份和发布标记；有 NFO 时以 NFO 标题为准。
- 季目录会被识别为 `Season 1`；特殊季显示为 `Specials`。

### 季目录命名

以下写法都识别为季：

```
Season 01
Season 1
Series 1
S01
第1季
第一季
第二部
```

以下目录名识别为**第 0 季（Specials）**：

```
Specials
Special
Extras
Bonus
OVA
OAD
SP
SPs
NCOP
NCED
PV
```

季目录必须放对层级：

```
Shows/Show Name/Season 02/S02E01.mkv          ✅ 季号来自目录和文件名
Shows/Show Name/Season 02/01.mkv              ✅ 季号来自目录，集号取开头的 01
Shows/Show Name/Season 02/发布组/01.mkv       ⚠️ 季号丢失，整部剧变成第 1 季
Shows/Show Name/Season 02/发布组/Show S02E01.mkv  ✅ 集号来自文件名
```

因为「季」只看**视频所在目录**或**文件名里的 `SxxExx`**，中间多套一层发布组目录就会丢掉季号。番剧要保留发布组目录时，让文件名带 `SxxExx`。

### 集号识别顺序

按以下顺序解析：

1. `S01E01`（允许 `S01.E01`、`S01_E01`、`S01-E01`、`S01 E01` 等分隔符）
2. `1x01`
3. 季目录 + 文件名开头的数字：`Season 01/03 Episode Title.mkv` → `S01E03`
4. 季目录存在但没有数字 → 季号正确，集号稍后按顺序补

文件名里的季号优先于目录。例如 `Season 02/Show S01E03.mkv` 会记成 `S01E03`。

### 季目录里的集号与顺序兜底

当季目录存在时，文件名开头的纯数字会被当作集号：

```
Season 01/
  01.mkv      → S01E01
  02.mkv      → S01E02
  03.mkv      → S01E03
```

文件名不以数字开头时（例如 `Episode Title.mkv`），集号按**文件遍历顺序**补。这带来两个风险：

- **缺集会错位**：只有 `[01] [03] [04]` 时，会变成 `E1 E2 E3`，第 3 集的信息会贴到第 4 个文件上
- **不补零会乱序**：`1.mkv 2.mkv 10.mkv` 的字典序是 `1, 10, 2`

所以即使不用 `SxxExx`，也建议把文件放在季目录里，并用**数字开头、补零**的文件名：

```
Season 01/
  01.mkv
  02.mkv
  ...
```

`Show Name - 01.mkv` 这种文件名不会解析出集号（只有 `SxxExx`、`1x01` 或开头的纯数字才会），仍然按顺序编号；补零时排序碰巧正确。

文件名没有 `SxxExx`、也没有季目录时，剧集库里的文件一律归入**第 1 季**，再按顺序编号。

### 扁平剧集库

库根目录直接放剧集文件也支持：

```
Shows/
  Severance.S01E01.mkv
  Severance.S01E02.mkv
  Another.Show.S01E01.mkv
```

`SxxExx` 前面的部分会作为剧集名，因此多个剧可以共用一个目录。前缀会经过清洗，`Show.Name` 与 `Show Name` 会归到同一部剧；但清洗后仍然不同的前缀（例如多了季号或发布组名）会被当成两部剧。

### 单集标题

标题取 `SxxExx` 之后的内容并清洗：

```
Severance S01E01 Good News About Hell.mkv  →  Good News About Hell
Severance S01E01.mkv                       →  Episode 1
```

没有 `SxxExx` 时，取去掉开头数字后的剩余文本；如果清洗后为空，回退为 `Episode N`。

配置 TMDb 并匹配到剧集后，单集标题会被官方标题覆盖；匹配不到时保留上面解析出的文本。发布组命名经常留下噪声，必要时用 `{tmdb-...}` 固定剧集 ID。

### 示例：一部番剧

推荐结构（文件直接放在季目录，花絮放子目录）：

```
Anime/
  败犬女主太多了！/
    poster.jpg
    Season 01/
      败犬女主太多了！ S01E01.mkv
      败犬女主太多了！ S01E02.mkv
      extras/
        Tokuten 01.mkv
      trailers/
        [Group][败犬女主太多了！][PV][S01E01].mkv
```

如果目录名和 TMDb 标题对不上（罗马音、翻译名、带发布组），在剧集目录上写 ID：

```
Anime/
  Make Heroine ga Oosugiru! {tmdb-12345}/
    Season 01/
      ...
```

不推荐的结构：

```
Anime/
  番名/
    Season 01/
      发布组/
        [Group] 番名 [01][1080p][CHS].mkv
```

问题：多了一层发布组目录，文件名又没有 `SxxExx`，季号会丢失，整部剧变成第 1 季，集号只能按文件顺序编号。要么让文件名带 `S01E01`，要么把文件直接放进 `Season 01/`。

## 花絮与预告片

电影或剧集目录下，以下目录里的视频会被当作花絮：

```
trailers
extras
featurettes
behind the scenes
deleted scenes
interviews
scenes
shorts
bonus
```

目录名不区分大小写。可以放在条目目录下，也可以放在季目录下：

```
Movies/
  Movie Name (2019)/
    Movie Name (2019).mkv
    trailers/Official Trailer.mkv
    extras/Behind The Scenes.mkv

Shows/
  Show Name/
    Season 01/
      Show Name S01E01.mkv
      extras/Tokuten 01.mkv
      trailers/[Group][Show Name][PV][S01E01].mkv
```

规则：

- 花絮挂在**所属电影或剧集**下面，不单独成为条目。
- 不出现在片库列表、「最近添加」、搜索和递归浏览里。
- 详情页底部的「花絮」区块会列出它们；第三方客户端通过 `GET /Items/{id}/SpecialFeatures` 获取。
- 直接放在**库根目录**下的 `extras/` 没有归属对象，会被忽略。
- 文件名没有要求；方括号发布名会提取 `PV S01E01`、`CM`、`Tokuten` 这类标记作为显示名。

## 图片

同一目录下，以下文件名会被识别为对应图片（`.jpg` `.jpeg` `.png` `.webp`）：

| 用途 | 文件名 |
| --- | --- |
| 海报 | `poster`、`folder`、`cover`、`default`、`movie` |
| 背景图 | `fanart`、`backdrop`、`background` |
| 标题 logo | `logo`、`clearlogo` |
| 横向图 / 缩略图 | `landscape`、`thumb` |

单集图片可以放在：

```
Show/Season 01/metadata/Show S01E01.jpg
Show/Season 01/Show S01E01.jpg
Show/metadata/Show S01E01.jpg
Show/Show S01E01.jpg
```

也接受 `-thumb`、`_thumb`、`.thumb`、`-poster` 后缀；文件名对不上时，会退而匹配相同的 `SxxExx` 编号。

本地图片优先于 TMDb 海报。没有本地图片、也没有配置 TMDb 时，Nuvio 会生成确定性的抽象封面和背景图。

## NFO

支持 Kodi 风格的 XML：

| 对象 | 查找顺序 |
| --- | --- |
| 电影 | `<视频文件名>.nfo` → `movie.nfo` |
| 单集 | `<视频文件名>.nfo` → `episode.nfo` |
| 剧集 | `<剧集目录名>.nfo` → `<清洗后的剧集名>.nfo` → `tvshow.nfo` → `series.nfo` |

读取的字段：

```
title, plot, outline, tagline, year, rating, mpaa, runtime,
genre, actor, tmdbid, imdbid, uniqueid
```

其中 `<uniqueid type="tmdb">12345</uniqueid>` 与 `<tmdbid>` 等价。

有 NFO 时 `MetadataSource` 记为 `nfo`：**本地标题、简介、年份、评分、类型、演员优先**，TMDb 只补空缺。NFO 里只有 ID 而没有其他字段时，不会把整个条目标记为本地元数据。

## 忽略与跳过

- 以 `.` 开头的文件和目录会被跳过。
- 这些目录会被跳过：`@eaDir`、`#recycle`、`lost+found`、`$RECYCLE.BIN`、`System Volume Information`、`node_modules`、`.git`、`transcodes`、`cache`。
- 支持 Jellyfin 的 `.ignore`：
  - **空文件**：整个目录被排除。
  - 非空文件：每行一条规则，支持 `#` 注释、`!` 反向选择、结尾 `/` 仅匹配目录，以及 `*`、`?`、`[abc]` 通配符。
  - 规则作用于该目录及其子目录，匹配的是文件/目录名，不是完整路径。

```
# 不导入样片
Sample/
*.txt
```

## 在网页里怎么显示

| 位置 | 显示内容 |
| --- | --- |
| 首页 | 继续观看、接下来、各库「最近添加」；生成过 AI 片单时还有主题列表 |
| 片库页 | 电影库只显示电影；剧集库只显示剧集；混合库可切换全部 / 电影 / 剧集 |
| 剧集详情 | 先选季，再列该季单集；「播放」按钮优先续播、其次下一集、最后第一集 |
| 电影详情 | 直接显示电影；下方可选「花絮」 |
| 搜索 | 匹配条目标题与剧集名，不包含花絮 |

排序：

- 片库页默认**最近添加**（`DateCreated:Descending`），可切换名称 A–Z、评分最高、年份最新。
- API 默认按 `SortName` 升序（不区分大小写）；`SortName` 会忽略开头的 `The `、`A `、`An `。
- 详情页的季按季号排序，单集按季号、集号排序。
- 「最近添加」按文件修改时间倒序；同一部剧的新集会让剧集整体排到前面。

## 常见问题

**剧集被拆成很多部电影**

媒体库类型建成了「电影库」或「混合库」。改成「剧集库」后重新扫描；混合库只对能解析出季集的命名生效（`SxxExx`、`1x01`、季目录 + 开头数字）。

**第二季显示成第一季**

文件在 `Season 02/发布组目录/` 里，且文件名没有 `SxxExx`。把文件直接放进 `Season 02/`，或让文件名带 `S02E01`。

**集号错位**

文件名没有集号，Nuvio 只能按顺序编号。补齐 `SxxExx`，或放在季目录里用 `01.mkv`、`02.mkv` 这种以数字开头的文件名（务必补零）。

**剧集名带着一堆发布信息**

第一层目录名太脏。把目录改成干净标题，或在目录/文件名里写 `{tmdb-剧集ID}`；匹配到 TMDb 后标题会被官方标题覆盖。

**一集里出现两个视频，或者花絮混进正片**

同一个电影目录里放了多个视频，或者花絮目录名不在识别列表里。检查目录名是否为 `trailers`、`extras` 等。

**改了文件但网页没变化**

扫描不是实时监听的。添加媒体库时会扫描；之后需要在设置页手动触发扫描。改名、换文件后请重新扫描。
