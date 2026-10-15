package cloudbridge

import (
	"github.com/pkg/errors"
	"github.com/xxcheng123/cloudpan189-interface/client"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"
	"go.uber.org/zap"
)

func (s *service) CheckSubscribeUser(ctx context.Context, subscribeUser string) (string, error) {
	resp, err := client.New().WithClient(ctx.HTTPClient()).SubscribeGetUser(ctx, subscribeUser)
	if err != nil {
		ctx.Error("查询订阅用户信息失败", zap.Error(err), zap.String("up_user_id", subscribeUser))

		return "", errors.WithStack(err)
	}

	return resp.Data.Name, nil
}

func (s *service) CheckSubscribeShare(ctx context.Context, subscribeUser, shareCode string) (shareId int64, isFolder bool, fileId string, err error) {
	resp, err := client.New().WithClient(ctx.HTTPClient()).GetShareInfo(ctx, shareCode)
	if err != nil {
		ctx.Error("查询订阅分享信息失败", zap.Error(err), zap.String("up_user_id", subscribeUser), zap.String("share_code", shareCode))

		return 0, false, "", errors.WithStack(err)
	}

	return resp.ShareId, resp.IsFolder, string(resp.FileId), nil
}

type CheckShareResult struct {
	ShareId    int64
	IsFolder   bool
	AccessCode string
	ShareMode  int
	FileId     string
	// LinkId 为 139 分享的字符串 linkID。
	// 调用方必须把它持久化到 addition，否则进程重启后内存注册表丢失，
	// 由 shareId 反查不到 linkID，挂载点将扫描不到任何文件。
	LinkId string
}

// CheckShare 校验分享链接/访问码是否有效（移动云盘 139 协议实现）
//
// 139 分享链接无需访问码（passwd 为空），校验方式为请求根目录：
// 能成功返回即视为有效。
func (s *service) CheckShare(ctx context.Context, shareCode string, accessCode string) (result *CheckShareResult, err error) {
	linkID := parseYun139LinkID(shareCode)
	if linkID == "" {
		linkID = resolveYun139LinkID(0, shareCode)
	}
	if linkID == "" {
		return nil, errors.New("分享链接/ID 为空")
	}

	resp, err := s.listYun139Dir(ctx, linkID, "root")
	if err != nil {
		ctx.Error("查询分享信息失败", zap.Error(err), zap.String("share_code", shareCode))
		return nil, errors.WithStack(err)
	}

	defer registerYun139Link(linkID)

	shareID := hashYun139LinkID(linkID)

	// 默认根节点为目录
	isFolder := true
	fileID := "root"

	if resp.Data != nil && len(resp.Data.CoLst) > 0 && len(resp.Data.CaLst) == 0 {
		// 根节点只有文件、没有目录时，视为单文件分享
		isFolder = false
		fileID = resp.Data.CoLst[0].CoID
	}

	return &CheckShareResult{
		ShareId:    shareID,
		IsFolder:   isFolder,
		AccessCode: accessCode,
		ShareMode:  0,
		FileId:     fileID,
		LinkId:     linkID,
	}, nil
}

func (s *service) CheckPerson(ctx context.Context, token AuthToken, fileId string) (string, error) {
	resp, err := client.New().WithClient(ctx.HTTPClient()).WithToken(token).GetFolderInfo(ctx, client.String(fileId))
	if err != nil {
		ctx.Error("查询文件信息失败", zap.Error(err), zap.String("file_id", fileId))

		return "", errors.WithStack(err)
	}

	return resp.FileName, nil
}

func (s *service) CheckFamily(ctx context.Context, token AuthToken, familyId, fileId string) error {
	_, err := client.New().WithClient(ctx.HTTPClient()).WithToken(token).FamilyListFiles(ctx, client.String(familyId), client.String(fileId))
	if err != nil {
		ctx.Error("查询文件信息失败", zap.Error(err), zap.String("file_id", fileId))

		return errors.WithStack(err)
	}

	return nil
}
