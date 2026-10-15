package dav

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/samber/lo"
	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/httpcontext"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/ptr"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/utils"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	"github.com/xxcheng123/cloudpan189-share/internal/shared"
	"gorm.io/gorm"

	group2fileSvi "github.com/xxcheng123/cloudpan189-share/internal/services/group2file"
	verifySvi "github.com/xxcheng123/cloudpan189-share/internal/services/verify"
	virtualfileSvi "github.com/xxcheng123/cloudpan189-share/internal/services/virtualfile"
)

type workEngine struct {
	virtualFileService virtualfileSvi.Service
	verifyService      verifySvi.Service
	group2FileService  group2fileSvi.Service
}

var bi = httpcontext.NewBusinessGenerator(consts.BusCodeDavStartCode)

var (
	busCodeFileQueryError     = bi.Next("查询文件失败")
	busCodeFileSignError      = bi.Next("文件签名失败")
	busCodeFilePathSplitError = bi.Next("路径切割失败")
	busCodeFileInvalidPath    = bi.Next("路径不合法，需要 / 开头的路径")
	busCodeFileNotFound       = bi.Next("文件不存在")
	busCodeQueryTopIdError    = bi.Next("查询 TopId 失败")
)

const (
	downloadURLFormat = "/api/file/download/%d?%s"

	// ★ WebDAV 的 GET/HEAD 必须【一步到位】302 到带 /play.m3u8 后缀的地址。
	//
	// 实测（2026-10-14，网易爆米花挂甲骨文 WebDAV）：
	//   ① 原实现 302 到 /api/file/download/2700?sign=..（无后缀）
	//   ② 播放器拿到的第一个 URL 就已判定「非 HLS/非视频」→ 不再跟随或跟随后失效
	//   ③ 第二跳补的后缀（download 端的同源 302 引导）救不回来
	//
	// 根因与飞牛 ffmpeg 一致：HLS 判据只看**入口 URL 后缀**或 **MIME**，
	// 而云盘恒回 Content-Type: text/plain。故入口必须自带 .m3u8。
	//
	// 分片流量仍是 302 直连云盘，本服务不代理任何媒体数据。
	downloadURLFormatHls = "/api/file/download/%d" + consts.DownloadURLSuffixHls + "?%s"
)

func (e *workEngine) Open() httpcontext.HandlerFunc {
	return func(ctx *httpcontext.Context) {
		fullPath := ctx.Param("path")

		// 切分路径查找文件
		paths, err := utils.SplitPath(fullPath)
		if err != nil {
			ctx.Fail(busCodeFilePathSplitError.WithError(err))

			return
		}

		var (
			file *models.VirtualFile
		)

		// 根节点
		if len(paths) == 0 {
			file = models.RootFile()
		} else if !utils.CheckIsPath(fullPath) {
			ctx.Fail(busCodeFileInvalidPath)

			return
		} else if file, err = e.virtualFileService.QueryByPath(ctx.GetContext(), fullPath); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				ctx.Fail(busCodeFileNotFound.WithError(err))
			} else {
				ctx.Fail(busCodeFileQueryError.WithError(err))
			}

			return
		}

		var allowTopIds []int64

		if userGroupId := ctx.GetInt64(consts.CtxKeyUserGroupId); userGroupId != 0 {
			topIds, err := e.group2FileService.GetBindFiles(ctx.GetContext(), userGroupId)
			if err != nil {
				ctx.Fail(busCodeQueryTopIdError.WithError(err))

				return
			}

			if len(topIds) == 0 || (!lo.Contains(topIds, file.TopId) && file.OsType != models.OsTypeFolder) {
				ctx.Unauthorized("无权限访问")

				return
			}

			allowTopIds = topIds
		}

		var (
			children []*models.VirtualFile
		)

		// 查询子节点
		if file.IsDir {
			childReq := &virtualfileSvi.ListRequest{
				ParentId: ptr.Of(file.ID),
			}

			// 目录查询子节点
			if children, err = e.virtualFileService.List(ctx.GetContext(), childReq); err != nil {
				ctx.Fail(busCodeFileQueryError.WithError(err))

				return
			}
		} else if ctx.Request.Method == http.MethodGet || ctx.Request.Method == http.MethodHead || ctx.Request.Method == http.MethodPost {
			values, err := e.verifyService.SignV1(ctx.GetContext(), file.ID)
			if err != nil {
				ctx.Fail(busCodeFileSignError.WithError(err))

				return
			}

			downloadURL := fmt.Sprintf(downloadURLFormat, file.ID, values.Encode())

			// 分享类挂载点（139 分享）的直链恒为 HLS 播放列表，
			// 入口 URL 必须自带 .m3u8，否则播放器（爆米花/ffmpeg）判定失败。
			if isShareMountType(file.OsType) {
				downloadURL = fmt.Sprintf(downloadURLFormatHls, file.ID, values.Encode())
			}

			ctx.Redirect(http.StatusFound, fmt.Sprintf("%s%s", shared.BaseURL, downloadURL))

			return
		}

		if len(allowTopIds) > 0 {
			children = lo.Filter(children, func(child *models.VirtualFile, index int) bool {
				return child.OsType == models.OsTypeFolder || lo.Contains(allowTopIds, child.TopId)
			})
		}

		// 设置WebDAV响应头
		ctx.Header("Content-Type", "application/xml; charset=utf-8")
		ctx.Header("DAV", "1, 2")

		// 构建XML响应
		var xmlResponse strings.Builder
		xmlResponse.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
		xmlResponse.WriteString(`<D:multistatus xmlns:D="DAV:">`)

		// 当前路径处理
		currentPath := e.normalizeWebDAVPath(ctx.Request.URL.Path)
		if file.IsDir && !strings.HasSuffix(currentPath, "/") {
			currentPath += "/"
		}

		e.addPropResponse(&xmlResponse, file, currentPath)

		// 如果是文件夹且depth不为0，添加子项
		if file.IsDir && ctx.Request.Header.Get("Depth") != "0" && len(children) > 0 {
			for _, child := range children {
				childPath := e.buildChildPath(currentPath, child.Name, child.IsDir)
				e.addPropResponse(&xmlResponse, child, childPath)
			}
		}

		xmlResponse.WriteString(`</D:multistatus>`)

		ctx.Data(http.StatusMultiStatus, "application/xml; charset=utf-8", []byte(xmlResponse.String()))
	}
}
