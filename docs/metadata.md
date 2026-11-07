# 元数据

Nuvio 的元数据来自三处，优先级从高到低：

1. 同目录的 `.nfo` 与本地图片（本地描述）
2. TMDb（可选，需要 API Key）
3. 扫描时从文件名/目录名解析出的标题与年份；都没有时生成占位图与 `Untitled`

## 本地 NFO

文件名约定见 [媒体目录与显示规则](media-layout.md#nfo)。读取的字段：

```xml
<movie>
  <title>Arrival</title>
  <plot>...</plot>
  <year>2016</year>
  <rating>7.6</rating>
  <mpaa>PG-13</mpaa>
  <runtime>116</runtime>
  <genre>Science Fiction</genre>
  <actor><name>...</name><role>...</role></actor>
  <tmdbid>329865</tmdbid>
  <imdbid>tt2543164</imdbid>
</movie>
```

也接受 Kodi/Jellyfin 的：

```xml
<uniqueid type="tmdb">329865</uniqueid>
<uniqueid type="imdb">tt2543164</uniqueid>
```

NFO 有内容时，条目的 `MetadataSource` 记为 `nfo`：

- 标题、简介、年份、评分、分级、类型、演员以本地为准
- TMDb 只补本地缺少的字段
- 标题不会被 TMDb 覆盖

只有 ID、没有其他字段的 NFO 不会把条目标记为本地描述，它只是告诉匹配器该用哪一个 TMDb 条目。

## 直接指定 TMDb / IMDb ID

这是最可靠的匹配方式，适合番剧、压制组命名、翻译名和 TMDb 标题对不上的情况。

可以写在目录名或文件名里：

```
Mayonaka Heart Tune {tmdb-285933}/Season 01/...
[imdb-tt2543164] Arrival (2016)/...
Show [tmdbid=1399] S01E01.mkv
```

解析规则：

- `tmdb-603`、`tmdbid=603`、`tmdb:603`、`tmdb 603`
- `imdb-tt0133093`、`imdbid=tt0133093`
- 有 TMDb ID 时直接读取该条目，不再搜索
- 只有 IMDb ID 时走 TMDb 的 `/find?external_source=imdb_id`

ID 会保存到条目上，重新扫描不会丢；Jellyfin 客户端通过 `ProviderIds.Tmdb` / `ProviderIds.Imdb` 也能看到。

## 自动匹配

配置 TMDb API Key 后，扫描会为每个电影/剧集执行：

1. 有 `TMDBID` → 直接读取
2. 有 `IMDBID` → `/find` 解析
3. 否则按候选标题搜索：
   - 解析出的标题
   - 电影还会尝试所在目录名（目录名往往比文件名干净）
4. 先按「设置 → 元数据语言」搜索；没有结果时用 `en-US` 再搜一次
5. 用标题相似度、年份、时长打分，达到阈值才应用

打分规则（阈值 5）：

| 条件 | 分数 |
| --- | --- |
| 标题完全相同 | 6 |
| 编辑距离相似度 ≥ 0.85 | 3 |
| 标题互相包含 | 2 |
| 相似度 ≥ 0.70 | 2 |
| 年份相同 / 相差 1 年 | +3 / +1 |
| 时长差 ≤ 3 分钟 / ≤ 10 分钟 | +2 / +1 |
| 时长差 > 20 分钟 | −2 |

时长来自本地 ffprobe 与 TMDb 的 `runtime`，用于区分同名翻拍和续集。例如 `The Matrix` 不会匹配到 `The Matrix Reloaded`。

匹配成功后写入：

- 标题（本地 NFO 除外）、简介、年份、首播日期、评分、类型
- 海报、背景图、标题 logo
- 剧集的海报与演员会继承给季和单集
- 单集会补全剧照、简介和播出日期

匹配失败时保留扫描结果，不会把标题清空。

## 手动匹配

设置 → 服务器 → 元数据匹配：

- 列出没有 TMDb ID 的电影和剧集（最多 200 条）
- 点「匹配」→ 预填当前名称 → 搜索 TMDb
- 从结果中选择正确条目，立即抓取元数据并写回数据库

选定的 ID 会保存到条目上，重新扫描时直接读取，不会被自动匹配覆盖。若类型不符（剧集选了电影），接口会拒绝。

对应接口：

```
GET  /api/admin/metadata/unmatched
GET  /api/admin/metadata/search?query=...
POST /api/admin/metadata/match
```

这些接口仅管理员可用，且需要已配置 TMDb Key。

## AI 识别

设置 → 元数据匹配 →「AI 识别」会把未匹配条目的文件名/目录名交给模型，要求返回：

```json
{"title": "The Matrix", "year": 1999}
```

然后用建议标题搜索 TMDb，校验类型与年份，命中后按手动匹配同样的方式写入。默认一次处理 10 条，最多 25 条。仍需配置 TMDb Key——AI 只负责猜标题，条目数据来自 TMDb。

详见 [AI](ai.md)。

## 图片

TMDb 图片通过服务器代理，不要求客户端直接访问外网：

```
/Items/{id}/Images/{Primary|Backdrop|Logo|Thumb}
```

缓存按类型分目录：

```
data/metadata/movies/
data/metadata/tvshows/
data/metadata/people/
```

本地图片优先。修改本地图片后重新扫描即可；删除 TMDb 缓存文件后下次请求会重新下载。

## 重扫与版本

- 元数据有版本号；同语言、同版本且已有 TMDb ID 的条目会跳过，不会每次扫描都请求 TMDb。
- 修改 TMDb Key 或元数据语言会自动触发一次扫描。
- 手动重新扫描不会清除已有的手动匹配。
- 删除媒体库不会删除媒体文件，只会移除索引。

## 常见问题

**番剧的罗马音目录名匹配不到**

TMDb 的搜索能命中罗马音，但返回的主标题是英文或日文原名，标题打分可能不够。最稳妥的做法是写 `{tmdb-...}` ID，或在设置里手动匹配；AI 识别是第三选择。

**匹配到了续集或翻拍**

检查目录/文件名里的年份是否正确。年份错了会直接影响打分；仍然不对时手动匹配。

**本地 NFO 的标题被覆盖了**

只有当 NFO 被成功读取并设置了字段时才会标记为本地描述。确认 NFO 文件名符合约定、XML 能解析；如果只有 `<tmdbid>`，标题仍会被 TMDb 覆盖。

**海报不更新**

本地有 `poster.jpg` 时永远优先于 TMDb。想用在线海报就移走本地图片；TMDb 缓存需要等下次请求或手动删除缓存。
