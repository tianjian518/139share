package cloudbridge

import (
	"time"

	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"
	"go.uber.org/zap"
)

type ShareInfo struct {
	Name       string    `json:"name"`
	IsFolder   bool      `json:"isFolder"`
	AccessCode string    `json:"accessCode"`
	ShareId    int64     `json:"shareId"`
	ID         string    `json:"id"`
	ShareTime  time.Time `json:"shareTime"`
}

// GetShareInfo 获取分享详情（移动云盘 139 协议实现）
//
// 139 通过 getOutLinkInfoV6 的根目录请求即可拿到分享名称等元信息，
// 分享 ID（ShareId）在 139 中即 linkID 的数字散列，这里以 RootCaID 作为 ID 返回。
func (s *service) GetShareInfo(ctx context.Context, shareCode string, accessCode string) (*ShareInfo, error) {
	linkID := parseYun139LinkID(shareCode)
	if linkID == "" {
		linkID = resolveYun139LinkID(0, shareCode)
	}

	resp, err := s.listYun139Dir(ctx, linkID, "root")
	if err != nil {
		ctx.Error("获取分享详情失败", zap.String("shareCode", shareCode), zap.Error(err))

		return nil, err
	}

	info := &ShareInfo{
		AccessCode: accessCode,
		ID:         "root",
		ShareId:    hashYun139LinkID(linkID),
	}

	if resp.Data != nil {
		info.Name = resp.Data.LkName
		info.IsFolder = true // 139 分享根节点始终是目录
		info.ShareTime = parseYun139Time(resp.Data.CtTime)
	}

	return info, nil
}
