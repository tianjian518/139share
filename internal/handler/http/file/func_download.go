package file

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"strings"
	"time"

	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/utils"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	"github.com/xxcheng123/cloudpan189-share/internal/shared"
	"github.com/xxcheng123/multistreamer"
	"go.uber.org/zap"

	"github.com/xxcheng123/cloudpan189-share/internal/framework/httpcontext"
	"gorm.io/gorm"

	cloudbridgeSvi "github.com/xxcheng123/cloudpan189-share/internal/services/cloudbridge"
)

type downloadRequest struct {
	Sign      string `form:"sign" binding:"required" example:"abc123"`
	UUID      string `form:"uuid" binding:"required" example:"def456"`
	Timestamp string `form:"timestamp" binding:"required" example:"1234567890"`
	Signer    string `form:"signer,default=v1" binding:"required" example:"v1"`
}

type downloadFileIdRequest struct {
	FileId int64 `uri:"fileId" binding:"required" example:"123456"`
}

// Download 下载文件
// @Summary 下载文件
// @Description 通过文件ID和签名验证下载文件，支持多种传输方式（重定向、本地代理、多线程流）
// @Tags 文件管理
// @Accept json
// @Produce application/octet-stream
// @Param fileId path int true "文件ID" example(123456)
// @Param sign query string true "签名" example("abc123")
// @Param uuid query string true "UUID" example("def456")
// @Param timestamp query string true "时间戳" example("1234567890")
// @Param signer query string true "签名版本" default("v1") example("v1")
// @Param Range header string false "HTTP Range 请求头，支持断点续传" example("bytes=0-1023")
// @Success 200 {file} binary "文件下载成功（本地代理或多线程流模式）"
// @Success 302 {string} string "重定向到下载链接（重定向模式）"
// @Failure 400 {object} httpcontext.Response "参数验证失败，code=99998"
// @Failure 400 {object} httpcontext.Response "签名验证失败，code=6005"
// @Failure 400 {object} httpcontext.Response "查询文件失败，code=6004"
// @Failure 400 {object} httpcontext.Response "查询文件令牌失败，code=6008"
// @Failure 400 {object} httpcontext.Response "获取下载链接失败，code=6009"
// @Failure 400 {object} httpcontext.Response "目录文件不支持下载，code=6006"
// @Failure 400 {object} httpcontext.Response "缺少family_id参数，code=6010"
// @Failure 400 {object} httpcontext.Response "缺少share_id参数，code=6011"
// @Failure 400 {object} httpcontext.Response "不支持的文件类型，code=6012"
// @Failure 400 {object} httpcontext.Response "创建本地代理请求失败，code=6013"
// @Failure 400 {object} httpcontext.Response "创建多线程流请求失败，code=6014"
// @Failure 404 {object} httpcontext.Response "文件令牌未绑定，code=6007"
// @Failure 401 {object} httpcontext.Response "未授权访问"
// @Failure 403 {object} httpcontext.Response "权限不足"
// @Router /api/file/download/{fileId} [get]
func (h *handler) Download() httpcontext.HandlerFunc {
	return func(ctx *httpcontext.Context) {
		req := new(downloadRequest)
		if err := ctx.ShouldBindQuery(req); err != nil {
			ctx.AbortWithInvalidParams(err)

			return
		}

		fileIdReq := new(downloadFileIdRequest)
		if err := ctx.ShouldBindUri(fileIdReq); err != nil {
			ctx.AbortWithInvalidParams(err)

			return
		}

		if err := h.verifyService.VerifyV1(ctx.GetContext(), fileIdReq.FileId, req.Sign, req.UUID, req.Timestamp, req.Signer); err != nil {
			ctx.Fail(busCodeFileVerifyError.WithError(err))

			return
		}

		// 查询挂载文件
		file, err := h.virtualFileService.Query(ctx.GetContext(), fileIdReq.FileId)
		if err != nil {
			ctx.Fail(busCodeFileQueryError.WithError(err))

			return
		}

		if file.IsDir {
			ctx.Fail(busCodeFileIsDirNotSupport)

			return
		}

		mountFile, err := h.mountPointService.Query(ctx.GetContext(), file.TopId)
		if err != nil {
			ctx.Fail(busCodeFileQueryError.WithError(err))

			return
		}

		// 获取下载链接
		//
		// 【重要】令牌查询必须延迟到「真正需要令牌」的分支里执行。
		// 139 分享类挂载点（ShareFile / SubscribeShareFile）走的是免登录的
		// getContentInfoFromOutLink，其 token_id 恒为 0，而 cloud_tokens 表中
		// 并不存在 id=0 的记录 —— 若像过去那样无条件查询，会直接
		// ErrRecordNotFound → 400「文件令牌未绑定」，导致分享链接无法播放。
		var (
			downloadLink string

			authToken   cloudbridgeSvi.AuthToken
			tokenLoaded bool
		)

		// needToken 按需加载云盘令牌（仅个人/家庭文件需要）
		needToken := func() bool {
			if tokenLoaded {
				return true
			}

			token, err := h.cloudTokenService.Query(ctx.GetContext(), mountFile.TokenId)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					ctx.Fail(busCodeTokenNotBind.WithError(err))
				} else {
					ctx.Fail(busCodeTokenQueryError.WithError(err))
				}

				return false
			}

			authToken = cloudbridgeSvi.NewAuthToken(token.AccessToken, token.ExpiresIn)
			tokenLoaded = true

			return true
		}

		switch file.OsType {
		case models.OsTypePersonFile:
			if !needToken() {
				return
			}

			downloadLink, err = h.cloudBridgeService.PersonDownloadLink(ctx.GetContext(), authToken, file.CloudId)
			if err != nil {
				ctx.Fail(busCodeGetDownloadLinkError.WithError(err))

				return
			}
		case models.OsTypeFamilyFile:
			if !needToken() {
				return
			}

			familyId, ok := file.Addition.String(consts.FileAdditionKeyFamilyId)
			if !ok {
				ctx.Fail(busCodeMissFamilyId)

				return
			}

			downloadLink, err = h.cloudBridgeService.FamilyDownloadLink(ctx.GetContext(), authToken, familyId, file.CloudId)
			if err != nil {
				ctx.Fail(busCodeGetDownloadLinkError.WithError(err))

				return
			}
		case models.OsTypeShareFile, models.OsTypeSubscribeShareFile:
			// 免登录通道，无需令牌
			shareId, ok := file.Addition.Int64(consts.FileAdditionKeyShareId)
			if !ok {
				ctx.Fail(busCodeMissShareId)

				return
			}

			downloadLink, err = h.cloudBridgeService.ShareDownloadLink(ctx.GetContext(), authToken, shareId, file.CloudId)
			if err != nil {
				ctx.Fail(busCodeGetDownloadLinkError.WithError(err))

				return
			}
		default:
			ctx.Fail(busCodeUnsupportedOsType)

			return
		}

		// HLS(m3u8) 直链：302 直连云盘，本服务不碰任何媒体流量。
		//
		// 139 分享的直链 302 后就是云盘 m3u8 播放列表，其内部子清单
		// （2160/index.m3u8 等）与分片全是**相对路径**；播放器 302 到云盘域名后，
		// 相对路径自然按云盘地址解析，**无需任何改写**。
		//
		// ⚠️ 铁律：本服务必须 302 直连，绝不代理媒体流量
		// （本项目要部署到甲骨文，本机只做跳板）。
		if isHlsRequest(ctx.Request.URL.Path) || isShareMountType(mountFile.OsType) {
			// ★★ 无后缀请求必须【同源 302 到带 .m3u8 后缀的地址】，绝不能直接甩到云盘。
			//
			// 实测（2026-10-14，飞牛内 ffmpeg 7.1.3，逐字复现）：
			//   ffmpeg -i <无后缀URL>   → "Not detecting m3u8/hls with non standard
			//                              extension and non standard mime type"
			//                            → Error opening input，播放失败
			//   ffmpeg -i <带.m3u8后缀> → 完全正常，零报错
			//
			// 原因：ffmpeg 判 HLS = 「标准后缀」或「标准 MIME」二者居一。
			//   直接 302 到云盘时，云盘恒回 Content-Type: text/plain（非标准 MIME），
			//   而 ffmpeg 依据**原始输入 URL**（无后缀）判定 → 两个条件全不满足 → 失败。
			//   飞牛影视 ffmpeg 请求的恰恰是无后缀形态（mediasrv.log 实锤：
			//   'fnmov:http://127.0.0.1:12399/api/file/download/3315?sign=...'，无 /play.m3u8）。
			//
			// 方案：先 302 到本服务的带后缀地址（**仍是 302，不代理任何流量**），
			//   让 ffmpeg 的输入 URL 带上 .m3u8 → 满足「标准后缀」→ 再跳云盘即成功。
			//   云盘 m3u8 内子清单/分片是相对路径，302 到云盘域名后由播放器自然解析。
			//
			// ★ WebDAV / 第三方播放器（爆米花）路径：
			//   播放器经 WebDAV 拿到 m3u8 后只能以「本服务 URL」为 baseURL 拼相对路径，
			//   会把 480/index.m3u8、stream/0/audio.m3u8 等请求打回本服务 → 无此路由 → 400
			//   → 转圈十几秒后静默停止（实测 2026-10-14，爆米花 192.168.1.2）。
			//   故：**已带 .m3u8 后缀的最终请求**必须取回并改写为云盘绝对地址后返回。
			//   （仅转发几 KB 文本，分片仍由播放器直连云盘，不代理媒体流量。）
			//
			// ★★ 但【只有 WebDAV 类客户端需要改写】，其余必须 302 直连云盘！
			//
			// 这是 2026-10-14 的第二个坑（改完爆米花后手机 APP 反而播不了）：
			//   手机 APP 与爆米花**请求的是同一个 URL 形态**
			//   （strm 内写死 http://127.0.0.1:12399/api/file/download/{id}/play.m3u8），
			//   路径层面完全无法区分，只能靠 User-Agent。
			//   实测日志（139share-new）：
			//     爆米花  UA=Filmly/2.12.11-439       Range=bytes=0-
			//     手机APP UA=trim_player              Range=bytes=0-0
			//   手机 APP 能正确跟随 302 到云盘、由云盘解析相对路径 → 必须 302；
			//   若对它也返回 200 改写文本，它会拿到文本却不再跳转 → 解析失败 → 播放不了。
			if isHlsRequest(ctx.Request.URL.Path) {
				if isWebDAVClient(ctx) {
					h.serveHlsPlaylist(ctx, downloadLink)

					return
				}

				ctx.Header(consts.HeaderKeyTransferType, consts.HeaderValueTransferTypeRedirect)
				ctx.Redirect(http.StatusFound, downloadLink)

				return
			}

			// ★ 无后缀请求：同源 302 到带 .m3u8 后缀的地址，让 ffmpeg/播放器识别为 HLS。
			//
			// ⚠️ 必须带 _probed=1 标记：301/302 之后客户端普遍保留 Range 头，
			// 若不标记会再次命中探测判定，形成 302 死循环（实测 curl -L 跳 5 次仍 302）。
			if ctx.Query(consts.QueryKeyProbed) == "" {
				ctx.Redirect(http.StatusFound, buildHlsPlaylistURL(ctx.Request.URL.Path, ctx.Request.URL.RawQuery))

				return
			}

			// ⚠️ 兜底：带 _probed=1 但仍无后缀的请求，只能 302 直连云盘
			// （个别播放器不跟随第二跳；此分支保证不返回 400）。
			ctx.Header(consts.HeaderKeyTransferType, consts.HeaderValueTransferTypeRedirect)
			ctx.Redirect(http.StatusFound, downloadLink)

			return
		}

		if shared.SettingAddition.MultipleStream {
			ctx.Header(consts.HeaderKeyTransferType, consts.HeaderValueTransferTypeMultiStream)
			ctx.Header(consts.HeaderKeyTransferChunkSize, fmt.Sprintf("%d", shared.SettingAddition.MultipleStreamChunkSize))
			ctx.Header(consts.HeaderKeyTransferThreadCount, fmt.Sprintf("%d", shared.SettingAddition.MultipleStreamThreadCount))
			ctx.Header(consts.HeaderKeyTransferChunkSizeFormat, utils.FormatBytes(shared.SettingAddition.MultipleStreamChunkSize))

			h.doMultiStream(ctx, downloadLink)
		} else if shared.SettingAddition.LocalProxy {
			ctx.Header(consts.HeaderKeyTransferType, consts.HeaderValueTransferTypeLocalProxy)

			h.doProxy(ctx, downloadLink)
		} else {
			ctx.Header(consts.HeaderKeyTransferType, consts.HeaderValueTransferTypeRedirect)
			ctx.Redirect(http.StatusFound, downloadLink)
		}
	}
}

// isHlsRequest 判断当前请求是否为 HLS 播放列表请求。
//
// strm 内的 HLS 地址形如 /api/file/download/{id}/play.m3u8，
// 路由以通配段 *extra 兜住，这里通过后缀识别。
func isHlsRequest(urlPath string) bool {
	return strings.HasSuffix(strings.ToLower(urlPath), consts.DownloadURLSuffixHls)
}

// isShareMountType 判断挂载点是否为「分享类」（直链 302 后恒为 m3u8）。
//
// 移动云盘 139 分享挂载点即属此类：其直链是 HLS 播放列表，而非普通视频文件。
// 这类挂载点即便请求路径不带 /play.m3u8 后缀（飞牛 ffmpeg 探测后的主请求即
// 无后缀形态），也必须走 HLS 播放列表改写分支 —— 否则会被 302 甩到云盘，
// 而云盘对 m3u8 恒回 Content-Type: text/plain，导致播放端（ffmpeg 需
// 「标准后缀 或 标准 MIME」二者居一）判定失败。
func isShareMountType(osType string) bool {
	switch osType {
	case models.OsTypeShareFolder, models.OsTypeShareFile,
		models.OsTypeSubscribeShareFolder, models.OsTypeSubscribeShareFile:
		return true
	}

	return false
}

// isProbeRequest 判断是否为播放端的「资源探测」请求。
//
// 实测（2026-10-13）：飞牛影视（网页版 ffmpeg 与手机 APP 原生播放器）
// 在真正播放前都会先发 Range: bytes=0-0 探测资源大小/类型。
// 对此类请求必须回 302，否则返回 200 + 文本会被判定为非视频。
func isProbeRequest(ctx *httpcontext.Context) bool {
	rangeHeader := strings.TrimSpace(ctx.GetHeader("Range"))
	if rangeHeader == "" {
		return false
	}

	return rangeHeader == "bytes=0-0" || rangeHeader == "bytes=0-1"
}

// webDAVClientUAKeywords 第三方 WebDAV / hls.js 类客户端的 User-Agent 特征。
//
// ★ 为什么必须靠 UA 区分（2026-10-14 实测）：
// 爆米花（Filmly）经 WebDAV 拿到的 m3u8 入口地址，与飞牛手机 APP 读 strm 的地址
// **形态完全相同**（均为 /api/file/download/{id}/play.m3u8?sign=...），
// 路径、查询串、请求方法都无差异，唯一可用的区分点就是 User-Agent。
//
// 这类客户端在 CORS 下读不到 302 之后的 responseURL，只能以本服务为 baseURL
// 拼接 m3u8 内的相对路径（480/index.m3u8 等）→ 请求打回本服务 → 400
// → 转圈十几秒后静默停止。故必须由服务端改写为云盘绝对地址后返回 200。
var webDAVClientUAKeywords = []string{
	"Filmly",  // 网易爆米花
	"webdav",  // 通用 WebDAV 客户端
	"davfs",   // Linux davfs2
	"rclone",  // rclone 挂载
	"Infuse",  // Infuse
	"nPlayer", // nPlayer
}

// isWebDAVClient 判断请求是否来自「需要服务端改写 m3u8」的 WebDAV 类客户端。
//
// 仅这类客户端返回改写后的 200 文本；其余（飞牛 APP trim_player、ffmpeg 等）
// 一律 302 直连云盘，由云盘自行解析相对路径。
func isWebDAVClient(ctx *httpcontext.Context) bool {
	ua := ctx.GetHeader("User-Agent")
	if ua == "" {
		return false
	}

	lowerUA := strings.ToLower(ua)
	for _, keyword := range webDAVClientUAKeywords {
		if strings.Contains(lowerUA, strings.ToLower(keyword)) {
			return true
		}
	}

	return false
}

// maxHlsPlaylistSize HLS 播放列表体积上限（正常仅几 KB，留足余量防异常大响应）。
const maxHlsPlaylistSize = 4 << 20

// serveHlsPlaylist 取回云盘 m3u8 播放列表，把其中的**相对路径改写为绝对地址**后返回。
//
// ⚠️ 仅转发几 KB 文本，视频分片仍由播放器直连云盘 —— 不代理媒体流量。
func (h *handler) serveHlsPlaylist(ctx *httpcontext.Context, link string) {
	logger := ctx.GetContext().Logger

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		logger.Error("创建 HLS 播放列表请求失败", zap.Error(err), zap.String("link", link))
		ctx.Fail(busCodeCreateLocalProxyRequestError.WithError(err))

		return
	}

	// 云盘侧按 UA 分发；带一个常规 UA 提高兼容性。
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")

	resp, err := globalHTTPClient.Do(req)
	if err != nil {
		logger.Error("获取 HLS 播放列表失败", zap.Error(err), zap.String("link", link))
		ctx.Fail(busCodeGetDownloadLinkError.WithError(err))

		return
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHlsPlaylistSize))
	if err != nil {
		logger.Error("读取 HLS 播放列表失败", zap.Error(err), zap.String("link", link))
		ctx.Fail(busCodeGetDownloadLinkError.WithError(err))

		return
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("获取 HLS 播放列表返回异常状态", zap.Int("status", resp.StatusCode), zap.String("link", link))
		ctx.Fail(busCodeGetDownloadLinkError.WithError(
			fmt.Errorf("上游返回 %d", resp.StatusCode)))

		return
	}

	rewritten := rewriteM3u8ToAbsolute(body, link)

	// 允许任何来源播放（爆米花/飞牛网页版/手机 APP），并禁用缓存避免拿到过期签名地址。
	ctx.Header("Access-Control-Allow-Origin", "*")
	ctx.Header("Access-Control-Allow-Headers", "*")
	ctx.Header("Access-Control-Expose-Headers", "*")
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Type", "application/vnd.apple.mpegurl")
	ctx.Status(http.StatusOK)
	_, _ = ctx.Writer.Write(rewritten)
}

// resolveToAbsolute 把 m3u8 内的（相对）URI 解析为绝对地址。
// 已是绝对地址或 data: 协议时返回空串，表示「无需改写」。
func resolveToAbsolute(uri string, baseRef *url.URL) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}

	if strings.Contains(uri, "://") || strings.HasPrefix(uri, "data:") {
		return ""
	}

	ref, err := url.Parse(uri)
	if err != nil {
		return ""
	}

	return baseRef.ResolveReference(ref).String()
}

// rewriteTagURI 改写标签行中的 URI="..." 片段（如 #EXT-X-KEY / #EXT-X-MAP）。
func rewriteTagURI(line string, baseRef *url.URL) string {
	const marker = `URI="`

	var sb strings.Builder

	rest := line
	for {
		idx := strings.Index(rest, marker)
		if idx < 0 {
			sb.WriteString(rest)

			break
		}

		sb.WriteString(rest[:idx+len(marker)])
		rest = rest[idx+len(marker):]

		end := strings.Index(rest, `"`)
		if end < 0 {
			sb.WriteString(rest)

			break
		}

		uri := rest[:end]
		if abs := resolveToAbsolute(uri, baseRef); abs != "" {
			sb.WriteString(abs)
		} else {
			sb.WriteString(uri)
		}
		sb.WriteString(`"`)
		rest = rest[end+1:]
	}

	return sb.String()
}

// absolutizeURI 改写裸 URI 行（变体播放列表或分片），保留原缩进。
func absolutizeURI(line, trimmed string, baseRef *url.URL) string {
	abs := resolveToAbsolute(trimmed, baseRef)
	if abs == "" {
		return line
	}

	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]

	return indent + abs
}

// rewriteM3u8ToAbsolute 把 m3u8 内的所有相对路径改写为**云盘绝对地址**。
//
// ★ 为什么必须改写（2026-10-14 实测，网易爆米花挂本服务 WebDAV）：
// 第三方播放器（爆米花）经 WebDAV 拿到 m3u8 后，无法获知 302 之后的真实地址，
// 只能以「本服务 URL」为 baseURL 拼接 m3u8 内的相对路径
// （480/index.m3u8、stream/0/audio.m3u8 等）
//   → 请求回到本服务 /api/file/download/{id}/480/index.m3u8
//   → 本服务无此路由 → 400 → 播放器转圈十几秒后静默停止（不报错）
//
// 改写后子清单与分片全部指向云盘域名，播放器直连云盘取流，
// 本服务仅转发几 KB 文本、**不代理任何媒体流量**（符合 302 部署前提）。
func rewriteM3u8ToAbsolute(content []byte, base string) []byte {
	baseURL, err := url.Parse(base)
	if err != nil {
		return content
	}

	// 以 m3u8 自身所在目录为基准，相对路径基于该目录解析。
	baseRef := baseURL.ResolveReference(&url.URL{Path: "./"})

	lines := strings.Split(string(content), "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// 情况一：标签行中的 URI="..."
		if strings.HasPrefix(trimmed, "#") {
			lines[i] = rewriteTagURI(line, baseRef)

			continue
		}

		// 情况二：裸 URI 行（变体播放列表或分片）
		lines[i] = absolutizeURI(line, trimmed, baseRef)
	}

	return []byte(strings.Join(lines, "\n"))
}

// buildHlsPlaylistURL 依据当前请求路径拼出「带 /play.m3u8 后缀 + _probed 标记」的地址。
//
// 入参为原始请求路径与查询串，输出同源的新地址，用于探测请求的 302 引导。
func buildHlsPlaylistURL(reqPath, rawQuery string) string {
	// 已带后缀的路径直接复用，避免 /play.m3u8/play.m3u8 叠加。
	target := reqPath
	if !isHlsRequest(target) {
		target = strings.TrimSuffix(target, "/") + consts.DownloadURLSuffixHls
	}

	if rawQuery == "" {
		return target + "?" + consts.QueryKeyProbed + "=1"
	}

	return target + "?" + rawQuery + "&" + consts.QueryKeyProbed + "=1"
}

// globalHTTPClient 供本地代理复用，避免每次请求新建 Transport。
var globalHTTPClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

func (h *handler) doProxy(ctx *httpcontext.Context, link string) {
	start := time.Now()
	logger := ctx.GetContext().Logger

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		logger.Error("创建本地代理请求失败", zap.Error(err), zap.String("link", link))

		ctx.Fail(busCodeCreateLocalProxyRequestError.WithError(err))

		return
	}

	h.copyOptimizedHeaders(ctx.Request.Header, req.Header)

	rangeHeader := ctx.Request.Header.Get("Range")
	isRangeRequest := rangeHeader != ""

	if isRangeRequest {
		req.Header.Set("Connection", "keep-alive")
	}

	resp, err := globalHTTPClient.Do(req)
	if err != nil {
		if ctx.Request.Context().Err() != nil {
			logger.Error("客户端断开连接", zap.String("link", link))

			return
		}

		logger.Error("本地代理请求失败", zap.Error(err), zap.String("link", link))

		ctx.Fail(busCodeCreateLocalProxyRequestError.WithError(err))

		return
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	h.copyOptimizedResponseHeaders(resp.Header, ctx)
	ctx.Status(resp.StatusCode)

	ctx.Stream(func(w io.Writer) bool {
		n, err := io.Copy(w, resp.Body)
		if err != nil {
			logger.Error("本地代理响应写入失败", zap.Error(err), zap.String("link", link))

			return false
		}

		logger.Debug("本地代理响应写入成功", zap.Int64("n", n), zap.String("link", link), zap.Duration("cost", time.Since(start)))

		return true
	})
}

func (h *handler) doMultiStream(ctx *httpcontext.Context, link string) {
	var (
		logger = ctx.GetContext().Logger
	)

	httpReq := ctx.Request.Header.Clone()
	httpReq.Set("Accept-Encoding", "identity")
	httpReq.Del("Content-Type")

	streamer, err := multistreamer.NewStreamer(ctx,
		link,
		httpReq,
		multistreamer.WithLogger(logger),
		multistreamer.WithThreads(shared.SettingAddition.MultipleStreamThreadCount),
		multistreamer.WithChunkSize(shared.SettingAddition.MultipleStreamChunkSize),
	)
	if err != nil {
		logger.Error("多线程流初始化失败", zap.Error(err), zap.String("url", link))

		ctx.Fail(busCodeCreateMultiStreamProxyError.WithError(err))

		return
	}

	for k, v := range streamer.GetResponseHeader() {
		ctx.Header(k, v[0])
	}

	ctx.Status(streamer.HTTPCode())

	if err = streamer.Transfer(ctx, ctx.Writer); err != nil {
		if h.isConnectionError(err) {
			logger.Info("客户端连接断开", zap.String("link", link))
		} else {
			logger.Error("多线程流文件传输失败", zap.Error(err), zap.String("link", link))
		}
	}
}

func (h *handler) copyOptimizedHeaders(src, dst http.Header) {
	importantHeaders := []string{
		"Range", "If-Range", "If-Modified-Since", "If-None-Match",
		"User-Agent", "Accept", "Accept-Encoding", "Authorization",
		"Referer", "Origin",
	}

	for _, header := range importantHeaders {
		if value := src.Get(header); value != "" {
			dst.Set(header, value)
		}
	}
}

func (h *handler) copyOptimizedResponseHeaders(src http.Header, ctx *httpcontext.Context) {
	importantHeaders := []string{
		"Content-Type", "Content-Length", "Content-Range",
		"Accept-Ranges", "Last-Modified", "ETag", "Cache-Control",
		"Content-Disposition", "Content-Encoding",
	}

	for _, header := range importantHeaders {
		if value := src.Get(header); value != "" {
			ctx.Header(header, value)
		}
	}

	ctx.Header("Connection", "keep-alive")
	ctx.Header("Keep-Alive", "timeout=120, max=100")
}

func (h *handler) isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"connection was forcibly closed",
		"wsasend",
		"broken pipe",
		"connection reset by peer",
		"client disconnected",
		"context canceled",
		"context deadline exceeded",
		"use of closed network connection",
		"connection refused",
		"no route to host",
	}

	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}

	return false
}
