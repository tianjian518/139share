package media

import (
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/taskcontext"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/ptr"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	"github.com/xxcheng123/cloudpan189-share/internal/services/mountpoint"
	"github.com/xxcheng123/cloudpan189-share/internal/services/virtualfile"
	"github.com/xxcheng123/cloudpan189-share/internal/shared"
	"github.com/xxcheng123/cloudpan189-share/internal/types/media"
	"go.uber.org/zap"

	verifySvi "github.com/xxcheng123/cloudpan189-share/internal/services/verify"
)

const maxRecursionDepth = 100 // 最大递归深度，防止栈溢出

// buildStrmURL 依据挂载点类型生成 strm 内写入的直链 URL。
//
// 移动云盘（139）分享类挂载点的直链 302 后是 m3u8 播放列表，需要让 URL
// 带上 .m3u8 后缀，否则飞牛影视网页版会把它当普通视频文件处理而播放失败
// （详见 consts.DownloadURLFormatHls 注释）。其余类型沿用原始无后缀地址。
//
// 注意：osType 由调用方在遍历顶层挂载点时一次性取得并沿目录树继承传入，
// 严禁在此处逐文件查库 —— 全库数千文件会造成大量重复查询拖垮重建任务。
func buildStrmURL(fileID int64, osType string, values url.Values) string {
	// 恢复（2026-10-13 二次修正）：分享类挂载点必须带 /play.m3u8 后缀。
	//
	// 此前误将此处回滚为无后缀地址，导致 strm 内写成 /api/file/download/{id}，
	// 播放端拿到 302 且无 MIME 特征 → 判定非视频 → 播放失败。
	// 实测（2026-10-13 08:46）：
	//   /api/file/download/1934           → 302（text/html，无 HLS 特征）
	//   /api/file/download/1934/play.m3u8 → 200 + application/vnd.apple.mpegurl
	if isHlsMountType(osType) {
		return shared.JoinDownloadURLHls(fileID, values)
	}

	return shared.JoinDownloadURL(fileID, values)
}

// isHlsMountType 判断挂载点类型是否为「直链 302 到 m3u8」的分享类挂载点。
func isHlsMountType(osType string) bool {
	switch osType {
	case models.OsTypeShareFolder, models.OsTypeShareFile,
		models.OsTypeSubscribeShareFolder, models.OsTypeSubscribeShareFile:
		return true
	default:
		return false
	}
}

func (h *handler) RebuildStrmFile() taskcontext.HandlerFunc {
	return func(ctx *taskcontext.Context) error {
		var (
			logger = ctx.GetContext().Logger
		)

		// 扫描文件
		// 判断是否符合 strm 文件格式
		mountpoints, err := h.mountpointService.List(ctx.GetContext(), &mountpoint.ListRequest{
			NoPaginate: true,
		})
		if err != nil {
			logger.Error("查询挂载点失败", zap.Error(err))

			return err
		}

		logger.Debug("查询到挂载点", zap.Int("count", len(mountpoints)))

		car := media.NewWriterCar(shared.MediaConfig.StoragePath, shared.MediaConfig.ConflictPolicy, shared.BaseURL)

		for _, mountpoint := range mountpoints {
			logger.Debug("处理挂载点", zap.String("mountpoint", mountpoint.FullPath))
			h.walkBuildStrm(
				ctx.GetContext(),
				mountpoint.FileId,
				car.NewSubCar(mountpoint.FullPath),
				0,
				mountpoint.OsType,
			)
		}

		return nil
	}
}

func (h *handler) walkBuildStrm(ctx context.Context, fid int64, car media.WriterCar, depth int, osType string) {
	// 检查递归深度，防止栈溢出
	if depth >= maxRecursionDepth {
		ctx.Warn("达到最大递归深度，停止处理", zap.Int("depth", depth), zap.Int64("parent_id", fid))

		return
	}

	files, err := h.virtualfileService.List(ctx, &virtualfile.ListRequest{
		ParentId: ptr.Of(fid),
	})
	if err != nil {
		ctx.Error("查询子文件失败", zap.Error(err), zap.Int64("parent_id", fid))

		return
	}

	for _, file := range files {
		if file.IsDir {
			// 同名目录折叠（2026-10-14）：分享者常把剧集目录再套一层同名目录，
			// 例如 `/电视剧/国产剧/X/X/Season 1/`，导致 strm 路径出现重复层级，
			// 影视刮削器会把内层当作独立剧集、识别失败。
			// 判据：子目录名与当前层名完全相同 → 不新增层级，原样 recurse。
			// 注意用 path.Base 取当前层名，根 car 的 fullPath 为空时 GetName() 返回 "."，
			// 此情况下恒不相等，天然不会误折叠。
			if file.Name == car.GetName() {
				ctx.Debug("批量创建文件 - 折叠同名目录",
					zap.String("dir_name", file.Name),
					zap.Int64("file_id", file.ID),
				)
				h.walkBuildStrm(ctx, file.ID, car, depth+1, osType)

				continue
			}

			h.walkBuildStrm(ctx, file.ID, car.NewSubCar(file.Name), depth+1, osType)

			continue
		}

		// 获取文件后缀
		extName := path.Ext(file.Name)
		if len(shared.MediaConfig.IncludedSuffixes) > 0 && !slices.Contains(shared.MediaConfig.IncludedSuffixes, extName) {
			ctx.Debug("批量创建文件 - 跳过文件", zap.String("file_name", file.Name))

			continue
		}

		// 重新生成文件名: video.mp4 -> video.strm
		filename := strings.TrimSuffix(file.Name, extName) + ".strm"

		// 生成URL
		values, err := h.verifyService.SignV1(ctx, file.ID, verifySvi.WithV1NoExpire())
		if err != nil {
			ctx.Error("批量创建文件 - 遍历 - 获取文件签名失败", zap.Int64("file_id", file.ID), zap.Error(err))

			continue
		}

		if id, err := h.mediaFileService.WriteStrm(ctx, car.NewSubCar(filename), file.ID, buildStrmURL(file.ID, osType, values)); err != nil {
			ctx.Error("批量创建文件 - 遍历 - 创建 strm 文件失败", zap.Int64("file_id", file.ID), zap.Error(err))

			continue
		} else {
			ctx.Debug("批量创建文件 - 遍历 - 创建 strm 文件成功", zap.Int64("file_id", file.ID), zap.Int64("media_file_id", id))
		}
	}
}
