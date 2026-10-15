package mountpoint

import (
	"regexp"
	"strings"

	"github.com/xxcheng123/cloudpan189-share/internal/bootstrap"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"

	cloudbridgeSvi "github.com/xxcheng123/cloudpan189-share/internal/services/cloudbridge"
	cloudtokenSvi "github.com/xxcheng123/cloudpan189-share/internal/services/cloudtoken"
	"github.com/xxcheng123/cloudpan189-share/internal/types/topic"

	"gorm.io/gorm"
)

type Service interface {
	Create(ctx context.Context, req *CreateRequest) (int64, error)
	Query(ctx context.Context, fileId int64) (*models.MountPoint, error)
	QueryByPath(ctx context.Context, fullPath string) (*models.MountPoint, error)
	List(ctx context.Context, req *ListRequest) ([]*models.MountPoint, error)
	Count(ctx context.Context, req *ListRequest) (int64, error)
	Delete(ctx context.Context, fileId int64) error
	BatchDelete(ctx context.Context, ids []int64) error
	EnableAutoRefresh(ctx context.Context, fileId int64, enable bool) error
	GetAutoRefreshList(ctx context.Context, req *GetAutoRefreshListRequest) ([]*models.MountPoint, error)
	UpdateRefreshConfig(ctx context.Context, fileId int64, config RefreshConfig) error
	ModifyToken(ctx context.Context, fid int64, tokenId int64) error
	BatchParseText(ctx context.Context, req *topic.BatchParseTextRequest) ([]*topic.BatchParseItem, error)
}

type service struct {
	svc                bootstrap.ServiceContext
	cloudTokenService  cloudtokenSvi.Service
	cloudBridgeService cloudbridgeSvi.Service
}

func NewService(
	svc bootstrap.ServiceContext,
	cloudTokenService cloudtokenSvi.Service,
	cloudBridgeService cloudbridgeSvi.Service,
) Service {
	return &service{
		svc:                svc,
		cloudTokenService:  cloudTokenService,
		cloudBridgeService: cloudBridgeService,
	}
}

func (s *service) getDB(ctx context.Context) *gorm.DB {
	return s.svc.GetDB(ctx).Model(new(models.MountPoint))
}

var (
	reFolderID = regexp.MustCompile(`^\d+$`)
	// 天翼云盘分享：https://cloud.189.cn/t/xxxx
	reShareLink = regexp.MustCompile(`cloud\.189\.cn\/t\/([a-zA-Z0-9]+)`)
	// 移动云盘(139)分享：https://yun.139.com/shareweb/#/w/i/xxxx 或 https://caiyun.139.com/w/i/xxxx
	reYun139ShareLink = regexp.MustCompile(`(?:yun|caiyun)\.139\.com\/(?:shareweb\/#\/)?w\/i\/([a-zA-Z0-9]+)`)
	reAccessCode      = regexp.MustCompile(`(?:\S+码|code)[:：]\s*([a-zA-Z0-9]+)`)
	// 139 裸分享码（linkID）：如 2xTrH57TFAJw2
	reYun139BareCode = regexp.MustCompile(`^[a-zA-Z0-9]{8,}$`)
)

// extractBare139LinkIDInBatch 从「139 裸分享码」形式的输入中提取 linkID。
// 返回空串表示不是 139 裸码。纯数字视为个人文件夹ID，不在此处理。
func extractBare139LinkIDInBatch(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") || strings.Contains(s, "/w/i/") {
		return ""
	}
	s = strings.Trim(s, "()【】[]{}<>\"'")
	if parts := strings.Fields(s); len(parts) > 0 {
		s = parts[0]
	}
	if !reYun139BareCode.MatchString(s) {
		return ""
	}
	if reFolderID.MatchString(s) {
		return ""
	}
	return s
}

// 实现 BatchParseText
func (s *service) BatchParseText(ctx context.Context, req *topic.BatchParseTextRequest) ([]*topic.BatchParseItem, error) {
	// 1. 获取 Token 信息（仅"个人文件夹ID"解析需要；139/天翼分享链接无需账号）
	var authToken cloudbridgeSvi.AuthToken
	if req.CloudToken > 0 {
		tokenInfo, err := s.cloudTokenService.Query(ctx, req.CloudToken)
		if err != nil {
			return nil, err
		}
		if tokenInfo != nil {
			authToken = cloudbridgeSvi.NewAuthToken(tokenInfo.AccessToken, tokenInfo.ExpiresIn)
		}
	}

	var results []*topic.BatchParseItem
	lines := strings.Split(req.Content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 预处理：统一中文符号
		cleanLine := strings.ReplaceAll(line, "（", "(")
		cleanLine = strings.ReplaceAll(cleanLine, "）", ")")
		cleanLine = strings.ReplaceAll(cleanLine, "：", ":")

		var (
			shareCode  string
			accessCode string
			fileId     string
			isShare    bool
			isFolder   bool
		)

		// 1. 尝试匹配分享链接 (全行搜索)
		// 1.1 移动云盘(139)：shareCode 需要保留完整链接，底层 parseYun139LinkID 自行剥离 /w/i/
		if matches := reYun139ShareLink.FindStringSubmatch(cleanLine); len(matches) > 1 {
			shareCode = strings.TrimSpace(matches[0])
			isShare = true
		} else if matches := reShareLink.FindStringSubmatch(cleanLine); len(matches) > 1 {
			// 1.2 天翼云盘：shareCode 为 /t/ 后的短码
			shareCode = matches[1]
			isShare = true
		} else if bare139 := extractBare139LinkIDInBatch(cleanLine); bare139 != "" {
			// 1.3 139 裸分享码（linkID），如 2xTrH57TFAJw2
			shareCode = bare139
			isShare = true
		}

		// 2. 如果不是分享链接，尝试匹配纯数字文件夹ID
		if !isShare {
			// 这里需要严谨一点，如果是纯数字或者是 "数字" 这种格式
			// 简单处理：如果是纯数字
			if reFolderID.MatchString(line) {
				fileId = line
				isFolder = true
			} else {
				// 如果是 "folder_id 12345" 这种格式，尝试提取
				parts := strings.Fields(line)
				if len(parts) > 0 && reFolderID.MatchString(parts[0]) {
					fileId = parts[0]
					isFolder = true
				}
			}
		}

		// 3. 提取访问码 (仅针对分享链接；139 分享不带访问码，跳过避免误判)
		if isShare {
			isBare139 := reYun139BareCode.MatchString(shareCode) && !reFolderID.MatchString(shareCode)
			if !isBare139 {
				if codeMatch := reAccessCode.FindStringSubmatch(cleanLine); len(codeMatch) > 1 {
					accessCode = codeMatch[1]
				} else {
					parts := strings.Fields(cleanLine)
					if len(parts) > 1 {
						lastPart := strings.Trim(parts[len(parts)-1], "()")
						if len(lastPart) == 4 {
							accessCode = lastPart
						}
					}
				}
			}
		}
		if isShare {
			info, err := s.cloudBridgeService.GetShareInfo(ctx, shareCode, accessCode)

			name := ""
			if err == nil && info != nil {
				name = info.Name
			} else {
				name = "未知分享_" + shareCode
			}

			results = append(results, &topic.BatchParseItem{
				Name:            name,
				OsType:          models.OsTypeShareFolder,
				ShareCode:       shareCode,
				ShareAccessCode: accessCode,
			})

		} else if isFolder {
			name, err := s.cloudBridgeService.CheckPerson(ctx, authToken, fileId)

			if err != nil || name == "" {
				name = "未知文件夹_" + fileId
			}

			results = append(results, &topic.BatchParseItem{
				Name:   name,
				OsType: models.OsTypePersonFolder,
				FileId: fileId,
			})
		}
	}

	return results, nil
}
