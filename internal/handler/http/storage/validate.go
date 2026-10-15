package storage

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/httpcontext"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/datatypes"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	cloudbridgeSvi "github.com/xxcheng123/cloudpan189-share/internal/services/cloudbridge"
	"go.uber.org/zap"
)

var (
	reShareLinkForAdd = regexp.MustCompile(`cloud\.189\.cn\/t\/([a-zA-Z0-9]+)`)
	// 移动云盘(139)：https://yun.139.com/shareweb/#/w/i/xxxx
	reYun139LinkForAdd = regexp.MustCompile(`(?:yun|caiyun)\.139\.com\/(?:shareweb\/#\/)?w\/i\/([a-zA-Z0-9]+)`)
	reAccessCodeForAdd = regexp.MustCompile(`(?:\S+码|code)[:：]\s*([a-zA-Z0-9]+)`)
	// 139 裸分享码（linkID）：如 2xTrH57TFAJw2，无域名、无 /w/i/ 前缀，纯字母数字
	reYun139BareCodeForAdd = regexp.MustCompile(`^[a-zA-Z0-9]{8,}$`)
	// 纯数字：个人文件夹ID
	reFolderIDForAdd = regexp.MustCompile(`^\d+$`)
)

// extractBare139LinkID 从「139 裸分享码」形式的输入中提取 linkID。
//
// 支持场景：
//   - 纯 linkID：2xTrH57TFAJw2
//   - 带前后杂讯：复制链接时只留下尾部短码
//
// 返回空串表示不是 139 裸码，调用方应继续走其它分支。
func extractBare139LinkID(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}
	// 含域名或 /w/i/ 的，交给链接分支处理
	if strings.Contains(s, "://") || strings.Contains(s, "/w/i/") {
		return ""
	}
	// 去掉包裹的括号等符号
	s = strings.Trim(s, "()【】[]{}<>\"'")
	// 取第一个空白分隔字段
	if parts := strings.Fields(s); len(parts) > 0 {
		s = parts[0]
	}
	if !reYun139BareCodeForAdd.MatchString(s) {
		return ""
	}
	// 纯数字视为「个人文件夹ID」，不当作 139 分享码
	if reFolderIDForAdd.MatchString(s) {
		return ""
	}
	return s
}

func (h *handler) executeOsTypeSubscribe(ctx context.Context, req *addRequest) (datatypes.JSONMap, httpcontext.BusinessError) {
	if req.OsType != models.OsTypeSubscribe {
		return nil, busCodeStorageOsTypeNotMatch
	}
	if req.SubscribeUser == "" {
		return nil, busCodeStorageSubscribeUserEmpty
	}
	if _, err := h.cloudBridgeService.CheckSubscribeUser(ctx, req.SubscribeUser); err != nil {
		return nil, busCodeStorageQuerySubscribeUserError.WithError(err)
	}
	return datatypes.JSONMap{
		consts.FileAdditionKeyUpUserId: req.SubscribeUser,
	}, nil
}

func (h *handler) executeOsTypeSubscribeShare(ctx context.Context, req *addRequest) (datatypes.JSONMap, string, httpcontext.BusinessError) {
	if req.OsType != models.OsTypeSubscribeShareFolder {
		return nil, "", busCodeStorageOsTypeNotMatch
	}
	if req.SubscribeUser == "" || req.ShareCode == "" {
		return nil, "", busCodeStorageSubscribeShareIncomplete
	}
	shareId, isFolder, fileId, err := h.cloudBridgeService.CheckSubscribeShare(ctx, req.SubscribeUser, req.ShareCode)
	if err != nil {
		return nil, "", busCodeStorageQuerySubscribeShareError.WithError(err)
	}
	return datatypes.JSONMap{
		consts.FileAdditionKeyUpUserId: req.SubscribeUser,
		consts.FileAdditionKeyShareId:  shareId,
		consts.FileAdditionKeyIsFolder: isFolder,
	}, fileId, nil
}

func (h *handler) executeOsTypeShare(ctx context.Context, req *addRequest) (datatypes.JSONMap, string, httpcontext.BusinessError) {
	if req.OsType != models.OsTypeShareFolder {
		return nil, "", busCodeStorageOsTypeNotMatch
	}
	if req.ShareCode == "" {
		return nil, "", busCodeStorageShareCodeEmpty
	}

	// 1. 清洗输入
	cleanCode := strings.ReplaceAll(req.ShareCode, "（", "(")
	cleanCode = strings.ReplaceAll(cleanCode, "）", ")")
	cleanCode = strings.ReplaceAll(cleanCode, "：", ":")
	cleanCode = strings.TrimSpace(cleanCode)

	var pureShareCode, pureAccessCode string

	// 2. 智能提取访问码
	req.ShareAccessCode = strings.TrimSpace(req.ShareAccessCode)
	if codeMatch := reAccessCodeForAdd.FindStringSubmatch(cleanCode); len(codeMatch) > 1 {
		pureAccessCode = codeMatch[1]
	} else if req.ShareAccessCode != "" {
		pureAccessCode = req.ShareAccessCode
	} else {
		parts := strings.Fields(cleanCode)
		if len(parts) > 1 {
			lastPart := strings.Trim(parts[len(parts)-1], "()")
			if len(lastPart) == 4 {
				pureAccessCode = lastPart
			}
		}
	}

	// 3. 智能提取分享码
	if matches := reYun139LinkForAdd.FindStringSubmatch(cleanCode); len(matches) > 1 {
		// 139 分享：保留完整链接原样传给 CheckShare，
		// 底层 parseYun139LinkID 会自行剥离 /w/i/ 取出 linkID。
		pureShareCode = strings.TrimSpace(matches[0])
		pureAccessCode = ""
	} else if matches := reShareLinkForAdd.FindStringSubmatch(cleanCode); len(matches) > 1 {
		pureShareCode = matches[1]
	} else if bare139 := extractBare139LinkID(cleanCode); bare139 != "" {
		// 139 裸分享码（linkID），如 2xTrH57TFAJw2：无域名直接可用，
		// 底层 parseYun139LinkID 已支持裸 ID，这里交给它处理。
		pureShareCode = bare139
		pureAccessCode = "" // 139 分享不带访问码，避免误判成 4 位访问码
	} else if !strings.Contains(cleanCode, "://") && !strings.Contains(cleanCode, "/w/i/") {
		// 非链接的裸分享码（天翼短码格式）
		if idx := strings.Index(cleanCode, "("); idx > -1 {
			pureShareCode = strings.TrimSpace(cleanCode[:idx])
		} else {
			parts := strings.Fields(cleanCode)
			if len(parts) > 0 {
				pureShareCode = strings.Trim(parts[0], "()")
			}
		}
	} else {
		// 兜底：未知格式的链接，整串交给底层解析
		pureShareCode = cleanCode
	}

	// 4. 构造 SDK 调用用的分享码格式
	formattedCode := pureShareCode
	if pureShareCode != "" && pureAccessCode != "" {
		formattedCode = fmt.Sprintf("%s（访问码：%s）", pureShareCode, pureAccessCode)
		req.ShareCode = formattedCode
		req.ShareAccessCode = pureAccessCode
	} else if pureShareCode != "" {
		req.ShareCode = pureShareCode
	}

	// 5. 调用 API 验证
	ctx.Info("开始校验分享码", zap.String("try_code", formattedCode), zap.String("access_code", pureAccessCode))

	result, err := h.cloudBridgeService.CheckShare(ctx, formattedCode, pureAccessCode)

	// 6. 错误处理与重试（仅天翼分享需要；139 链接重试拼 cloud.189.cn 无意义）
	isYun139 := reYun139LinkForAdd.MatchString(cleanCode) || reYun139BareCodeForAdd.MatchString(pureShareCode)
	if !isYun139 && (err != nil || (result != nil && result.ShareId == 0)) {
		ctx.Warn("完整格式校验未通过，尝试使用纯码重试",
			zap.String("formatted_code", formattedCode),
			zap.String("pure_code", pureShareCode),
			zap.Error(err),
			zap.Any("first_result", result),
		)
		if formattedCode != pureShareCode {
			resultRetry, errRetry := h.cloudBridgeService.CheckShare(ctx, pureShareCode, pureAccessCode)
			if errRetry == nil && resultRetry != nil && resultRetry.ShareId != 0 {
				result = resultRetry
				err = nil
			} else {
				checkUrl := fmt.Sprintf("https://cloud.189.cn/t/%s", pureShareCode)
				resultUrl, errUrl := h.cloudBridgeService.CheckShare(ctx, checkUrl, pureAccessCode)
				if errUrl == nil && resultUrl != nil && resultUrl.ShareId != 0 {
					result = resultUrl
					err = nil
				}
			}
		}
	}

	// 7. 最终检查
	if err != nil {
		return nil, "", busCodeStorageQuerySubscribeShareError.WithError(err)
	}
	if result == nil || result.ShareId == 0 {
		ctx.Error("所有尝试均失败，无法获取ShareId",
			zap.String("final_result_struct", fmt.Sprintf("%+v", result)))
		return nil, "", busCodeStorageQuerySubscribeShareError.WithError(fmt.Errorf("无法获取有效的分享ID(ShareId=0)，请确认分享链接是否有效"))
	}

	return datatypes.JSONMap{
		consts.FileAdditionKeyShareId:    result.ShareId,
		consts.FileAdditionKeyIsFolder:   result.IsFolder,
		consts.FileAdditionKeyShareMode:  result.ShareMode,
		consts.FileAdditionKeyAccessCode: pureAccessCode,
		// 持久化 139 linkID：内存注册表重启即丢，靠这里回填才能恢复映射
		"link_id": result.LinkId,
	}, result.FileId, nil
}

func (h *handler) executeOsTypePersonal(ctx context.Context, req *addRequest) httpcontext.BusinessError {
	if req.OsType != models.OsTypePersonFolder {
		return busCodeStorageOsTypeNotMatch
	}
	if req.FileId == "" {
		return busCodeStoragePersonParamsIncomplete
	}
	if req.CloudToken == 0 {
		return busCodeStorageCloudTokenEmpty
	}
	token, err := h.cloudTokenService.Query(ctx, req.CloudToken)
	if err != nil {
		return busCodeStorageCloudTokenNotExist.WithError(err)
	}
	if _, err = h.cloudBridgeService.CheckPerson(ctx, cloudbridgeSvi.NewAuthToken(token.AccessToken, token.ExpiresIn), req.FileId); err != nil {
		return busCodeStoragePersonFileQueryError.WithError(err)
	}
	return nil
}

func (h *handler) executeOsTypeFamily(ctx context.Context, req *addRequest) (datatypes.JSONMap, httpcontext.BusinessError) {
	if req.OsType != models.OsTypeFamilyFolder {
		return nil, busCodeStorageOsTypeNotMatch
	}
	if req.FileId == "" || req.FamilyId == "" {
		return nil, busCodeStorageFamilyParamsIncomplete
	}
	if req.CloudToken == 0 {
		return nil, busCodeStorageCloudTokenEmpty
	}
	token, err := h.cloudTokenService.Query(ctx, req.CloudToken)
	if err != nil {
		return nil, busCodeStorageCloudTokenNotExist.WithError(err)
	}
	if err = h.cloudBridgeService.CheckFamily(ctx, cloudbridgeSvi.NewAuthToken(token.AccessToken, token.ExpiresIn), req.FamilyId, req.FileId); err != nil {
		return nil, busCodeStorageFamilyFileQueryError.WithError(err)
	}
	return datatypes.JSONMap{
		consts.FileAdditionKeyFamilyId: req.FamilyId,
	}, nil
}
