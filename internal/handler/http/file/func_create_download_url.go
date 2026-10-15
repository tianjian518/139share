package file

import (
	"strings"

	"github.com/samber/lo"
	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/httpcontext"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	"github.com/xxcheng123/cloudpan189-share/internal/shared"
)

type createDownloadURLRequest struct {
	FileID int64 `json:"fileId" binding:"required,min=1" example:"123456"`
}

type createDownloadURLResponse struct {
	DownloadURL string `json:"downloadUrl" example:"/api/file/download/123456?sign=abc&uuid=def&timestamp=1234567890&signer=v1"`
	// IsHls 告知前端该直链 302 之后是不是 HLS(m3u8)。
	//
	// 前端无法自行判断：下发的地址形如 /api/file/download/xxx?sign=...，
	// 从 URL 和后缀都看不出格式；若用 fetch 探测 302 最终地址，浏览器会因
	// 跨域（目标为云盘域名）而失败，导致电脑端误判为普通视频、播放黑屏
	// （手机 APP 的 WebView 不强制 CORS，故只在电脑上复现）。
	// 因此由后端直接给出结论，前端不再做任何网络探测。
	IsHls bool `json:"isHls" example:"true"`
}

// isHlsByFileName 依据文件名后缀判断是否 HLS
func isHlsByFileName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".m3u8")
}

// CreateDownloadURL 创建文件下载链接
// @Summary 创建文件下载链接
// @Description 为指定文件生成带签名的下载链接
// @Tags 文件管理
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param request body createDownloadURLRequest true "文件ID信息"
// @Success 200 {object} httpcontext.Response{data=createDownloadURLResponse} "下载链接创建成功"
// @Failure 400 {object} httpcontext.Response "参数验证失败，code=99998"
// @Failure 400 {object} httpcontext.Response "文件签名失败，code=6015"
// @Failure 401 {object} httpcontext.Response "未授权访问"
// @Failure 403 {object} httpcontext.Response "权限不足"
// @Router /api/file/create_download_url [post]
func (h *handler) CreateDownloadURL() httpcontext.HandlerFunc {
	return func(ctx *httpcontext.Context) {
		var req createDownloadURLRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			ctx.AbortWithInvalidParams(err)

			return
		}

		if userGroupId := ctx.GetInt64(consts.CtxKeyUserGroupId); userGroupId != 0 {
			file, err := h.virtualFileService.Query(ctx.GetContext(), req.FileID)
			if err != nil {
				ctx.Fail(busCodeFileQueryError.WithError(err))

				return
			}

			topIds, err := h.group2FileService.GetBindFiles(ctx.GetContext(), userGroupId)
			if err != nil {
				ctx.Fail(busCodeQueryTopIdError.WithError(err))

				return
			}

			if len(topIds) == 0 || !lo.Contains(topIds, file.TopId) {
				ctx.Unauthorized("无权限访问")

				return
			}
		}

		values, err := h.verifyService.SignV1(ctx.GetContext(), req.FileID)
		if err != nil {
			ctx.Fail(busCodeFileSignError.WithError(err))

			return
		}

		// 判断该文件直链是否为 HLS
		isHls := false

		if file, err := h.virtualFileService.Query(ctx.GetContext(), req.FileID); err == nil {
			isHls = isHlsByFileName(file.Name)

			if !isHls {
				// 分享类挂载点（含移动云盘 139）的直链 302 后恒为 m3u8
				if mp, err := h.mountPointService.Query(ctx.GetContext(), file.TopId); err == nil {
					switch mp.OsType {
					case models.OsTypeShareFolder, models.OsTypeShareFile,
						models.OsTypeSubscribeShareFolder, models.OsTypeSubscribeShareFile:
						isHls = true
					}
				}
			}
		}

		// HLS 直链必须带 /play.m3u8 后缀。
		//
		// 实测（2026-10-13 09:0x）：无后缀地址被播放端（飞牛 ffmpeg / hls.js）
		// 当作普通文件处理 → 拿到 302 或 text/plain → 判定非视频 → 播放失败。
		// 带后缀时本服务返回 200 + application/vnd.apple.mpegurl，两端均正常。
		downloadURL := shared.JoinDownloadURL(req.FileID, values)

		if isHls {
			downloadURL = shared.JoinDownloadURLHls(req.FileID, values)
		}

		ctx.Success(&createDownloadURLResponse{
			DownloadURL: downloadURL,
			IsHls:       isHls,
		})
	}
}
