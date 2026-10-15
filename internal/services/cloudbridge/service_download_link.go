package cloudbridge

import (
	"fmt"
	"net/http"
	"time"

	"github.com/pkg/errors"

	"github.com/xxcheng123/cloudpan189-interface/client"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"
	"github.com/xxcheng123/cloudpan189-share/internal/shared"
	"go.uber.org/zap"
)

const (
	personDownloadFormat = "person_download_link:%s"
	familyDownloadFormat = "family_download_link:%s:%s"
	shareDownloadFormat  = "share_download_link:%d:%s"
)

func (s *service) PersonDownloadLink(ctx context.Context, token AuthToken, fileId string) (string, error) {
	return s.loadOrFetch(ctx, fmt.Sprintf(personDownloadFormat, fileId), func() (string, error) {
		link, err := client.New().WithClient(ctx.HTTPClient()).WithToken(token).GetFileDownload(ctx, client.String(fileId))
		if err != nil {
			ctx.Error("获取个人文件下载地址失败",
				zap.String("file_id", fileId), zap.Error(err))

			return "", err
		}

		return link.FileDownloadUrl, nil
	})
}

func (s *service) FamilyDownloadLink(ctx context.Context, token AuthToken, familyId, fileId string) (string, error) {
	return s.loadOrFetch(ctx, fmt.Sprintf(familyDownloadFormat, familyId, fileId), func() (string, error) {
		link, err := client.New().WithClient(ctx.HTTPClient()).WithToken(token).FamilyGetFileDownload(ctx, client.String(familyId), client.String(fileId))
		if err != nil {
			ctx.Error("获取家庭文件下载地址失败",
				zap.String("file_id", fileId), zap.Error(err))

			return "", err
		}

		return link.FileDownloadUrl, nil
	})
}

// ShareDownloadLink 获取分享文件的下载直链（移动云盘 139 协议实现）
//
// 说明：139 通过 dlFromOutLinkV3 返回 S3 预签名 CDN 直链（X-Amz-Expires=900，即 15 分钟有效）。
// 因此这里不使用 loadOrFetch（其会做重定向解析且缓存 2 分钟），而是直接请求并短缓存，
// 避免缓存过期链接导致播放失败。
func (s *service) ShareDownloadLink(ctx context.Context, token AuthToken, shareId int64, fileId string) (string, error) {
	linkID := resolveYun139LinkID(shareId, "")

	cacheKey := fmt.Sprintf(shareDownloadFormat, shareId, fileId)

	if v, ok := shared.ShareCache.Get(cacheKey); ok {
		if cached, ok2 := v.(string); ok2 && cached != "" {
			return cached, nil
		}
	}

	link, err := s.yun139DownloadLink(ctx, linkID, fileId)
	if err != nil {
		ctx.Error("获取分享文件下载地址失败",
			zap.Int64("share_id", shareId),
			zap.String("link_id", linkID),
			zap.String("file_id", fileId),
			zap.Error(err))

		return "", err
	}

	// 预签名链接本身有效期 15 分钟，这里只缓存 5 分钟，留出安全余量
	shared.ShareCache.Set(cacheKey, link, time.Minute*5)

	return link, nil
}

var (
	noFollowRedirectHttpClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36")

			return http.ErrUseLastResponse // 停止跟随重定向
		},
	}
)

// fetchRealDownloadLink 获取真实的下载地址 自带的跳转域名有泄露 token 风险
func (s *service) fetchRealDownloadLink(ctx context.Context, link string) (string, error) {
	resp, err := noFollowRedirectHttpClient.Get(link)
	if err != nil {
		ctx.Error("请求云盘下载链接失败",
			zap.String("downloadUrl", link),
			zap.Error(err))

		return "", err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// 从Location头获取重定向地址（内网地址）
		location := resp.Header.Get("Location")
		if location == "" {
			return "", errors.New("重定向响应但没有Location头")
		}

		return location, nil
	}

	return "", errors.Wrap(err, "获取重定向地址失败")
}

func (s *service) loadOrFetch(ctx context.Context, cacheKey string, fn func() (string, error)) (string, error) {
	if v, ok := shared.ShareCache.Get(cacheKey); ok {
		ctx.Debug("从缓存中获取个人文件下载地址", zap.String("file_id", cacheKey))

		return v.(string), nil
	}

	v, err := fn()
	if err != nil {
		return "", err
	}

	realLink, err := s.fetchRealDownloadLink(ctx, v)
	if err != nil {
		return "", err
	}

	shared.ShareCache.Set(cacheKey, realLink, time.Minute*2)

	ctx.Debug("真实获取个人文件下载地址", zap.String("file_id", cacheKey), zap.String("download_link", realLink))

	return realLink, nil
}
