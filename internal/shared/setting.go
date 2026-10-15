package shared

import (
	"fmt"
	"net/url"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
)

var (
	SaltKey    string
	BaseURL    string
	EnableAuth bool

	SettingAddition = models.SettingAddition{}
)

var (
	ShareCache = cache.New(5*time.Minute, 10*time.Minute)
)

func JoinDownloadURL(fileId int64, values url.Values) string {
	baseURL := BaseURL
	if baseURL == "" {
		// 默认使用 localhost 和常用端口
		baseURL = "http://localhost:12395"
	}
	return fmt.Sprintf("%s%s", baseURL, fmt.Sprintf(consts.DownloadURLFormat, fileId, values.Encode()))
}

// JoinDownloadURLHls 生成带 .m3u8 后缀的 HLS 下载链接。
//
// 用于移动云盘（139）分享类挂载点：其直链 302 后是 m3u8 播放列表。
// 若沿用无后缀的原始路径，飞牛影视网页版会把响应当普通视频文件处理
// （先 Range: bytes=0-0 探测，拿到 #EXTM3U 后判定非视频 → 播放失败）。
// 追加 /play.m3u8 让播放端从 URL 识别为 HLS。
func JoinDownloadURLHls(fileId int64, values url.Values) string {
	baseURL := BaseURL
	if baseURL == "" {
		// 默认使用 localhost 和常用端口
		baseURL = "http://localhost:12395"
	}
	return fmt.Sprintf("%s%s", baseURL, fmt.Sprintf(consts.DownloadURLFormatHls, fileId, values.Encode()))
}
