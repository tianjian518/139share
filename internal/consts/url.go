package consts

const (
	DownloadURLFormat = "/api/file/download/%d?%s"

	// DownloadURLFormatHls 用于 HLS(m3u8) 直链。
	//
	// 移动云盘（139）分享的直链 302 后是 m3u8 播放列表，但原下载路径
	// /api/file/download/{id} 从 URL 看既无后缀也无格式特征，导致部分
	// 播放端（典型：飞牛影视网页版）把响应当"普通视频文件"处理 —— 先发
	// Range: bytes=0-0 探测，拿到 #EXTM3U 文本后判定不是视频，直接播放失败。
	// 在路径末尾追加 /play.m3u8 让这类播放端一眼识别为 HLS，走 HLS 播放分支。
	DownloadURLFormatHls = "/api/file/download/%d/play.m3u8?%s"

	// DownloadURLSuffixHls 供路由与判断复用
	DownloadURLSuffixHls = "/play.m3u8"

	// QueryKeyProbed 标记查询参数。
	//
	// 播放端（飞牛影视）在播放前会发 Range: bytes=0-0 探测资源，本服务对此回 302
	// 引导到带 .m3u8 后缀的播放列表地址；但客户端跟随重定向时普遍保留 Range 头，
	// 若不加标记会再次命中探测判定，形成 302 死循环。重定向目标带上本参数后，
	// 即便仍带 Range 也直接返回播放列表。
	QueryKeyProbed = "_probed"
)
