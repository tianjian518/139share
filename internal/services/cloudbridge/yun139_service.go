package cloudbridge

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"
	"github.com/xxcheng123/cloudpan189-share/internal/types/converter"
)

// ============================================================================
// 移动云盘（139）分享：响应结构与业务方法
// ============================================================================

// yun139OutLinkInfo 分享目录信息（getOutLinkInfoV6 返回的 data）
type yun139OutLinkInfo struct {
	NodNum    int             `json:"nodNum"`
	CaLst     []yun139CaItem  `json:"caLst"`
	CoLst     []yun139CoItem  `json:"coLst"`
	LkName    string          `json:"lkName"`
	Creator   string          `json:"creator"`
	Owner     string          `json:"owner"`
	NickName  string          `json:"nickName"`
	CaLength  string          `json:"caLength"`
	CoLength  string          `json:"coLength"`
	IsCreator string          `json:"isCreator"`
	CtTime    string          `json:"ctTime"`
	Raw       json.RawMessage `json:"-"`
}

// yun139CaItem 目录项
type yun139CaItem struct {
	CaID      string `json:"caID"`
	CaName    string `json:"caName"`
	Path      string `json:"path"`
	CtTime    string `json:"ctTime"`
	UdTime    string `json:"udTime"`
	Illegal   int    `json:"illegalType"`
	IsCreator string `json:"isCreator"`
}

// yun139CoItem 文件项
// ⚠️ 注意：139 返回的 coType / illegalType / updateStatus 均为**数字**，
// 不可声明为 string，否则 json.Unmarshal 直接报 type error 导致整个响应解析失败。
type yun139CoItem struct {
	CoID       string `json:"coID"`
	CoName     string `json:"coName"`
	CoSize     int64  `json:"coSize"`
	CoType     int    `json:"coType"`
	CoSuffix   string `json:"coSuffix"`
	Path       string `json:"path"`
	CtTime     string `json:"ctTime"`
	UdTime     string `json:"udTime"`
	PresentURL string `json:"presentURL"`
	Md5        string `json:"md5"`
	Sha256     string `json:"sha256"`
	CaID       string `json:"caID"`
}

// yun139OutLinkResponse getOutLinkInfoV6 的完整响应
type yun139OutLinkResponse struct {
	ResultCode string             `json:"resultCode"`
	Desc       string             `json:"desc"`
	Data       *yun139OutLinkInfo `json:"data"`
	Success    bool               `json:"success"`
	Code       string             `json:"code"`
}

// yun139DownloadResponse dlFromOutLinkV3 的完整响应
type yun139DownloadResponse struct {
	ResultCode string `json:"resultCode"`
	Desc       string `json:"desc"`
	Data       *struct {
		DownloadMode int    `json:"downloadMode"`
		TaskID       string `json:"taskId"`
		RedrURL      string `json:"redrUrl"`
		UserDomainID string `json:"userDomainId"`
		HashValue    string `json:"hashValue"`
		HashName     string `json:"hashName"`
		DownloadURL  string `json:"downloadUrl"`
	} `json:"data"`
	Success bool   `json:"success"`
	Code    string `json:"code"`
}

// yun139Result 通用结果判定
type yun139Result struct {
	ResultCode string `json:"resultCode"`
	Desc       string `json:"desc"`
	Success    bool   `json:"success"`
	Code       string `json:"code"`
}

func (r yun139Result) ok() bool {
	return r.ResultCode == "0" || r.Code == "0" || r.Success
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// parseYun139LinkID 从分享链接或裸 ID 中提取 linkID
func parseYun139LinkID(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	if idx := strings.Index(input, "/w/i/"); idx >= 0 {
		id := input[idx+len("/w/i/"):]
		// 去掉可能存在的查询串/锚点
		if i := strings.IndexAny(id, "?#/"); i >= 0 {
			id = id[:i]
		}
		return id
	}

	// 已是裸 ID
	if i := strings.IndexAny(input, "?#/"); i >= 0 {
		return input[:i]
	}
	return input
}

// parseYun139Time 解析 139 的时间格式 20060102150405
func parseYun139Time(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(consts.RevFormat, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// chtHTTPClient 返回协议层使用的 HTTP 客户端。
//
// 说明：上下文的 HTTPClient() 返回 resty 客户端，而其底层 *http.Client 未导出且无 getter，
// 因此这里使用协议层自持的单例 client（自带连接池复用与超时），
// 生命周期与进程一致，避免每次请求重建 Transport 造成连接泄漏。
func (s *service) chtHTTPClient(_ context.Context) *http.Client {
	return yun139HTTPClient
}

// yun139HTTPClient 协议层全局 HTTP 客户端
var yun139HTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	},
}

// ---------------------------------------------------------------------------
// 分享目录列举（对应 GetShareFiles）
// ---------------------------------------------------------------------------

// listYun139Dir 列举指定目录下的内容，pCaID 为空或 "root" 表示根目录
func (s *service) listYun139Dir(ctx context.Context, linkID, pCaID string) (*yun139OutLinkResponse, error) {
	if pCaID == "" {
		pCaID = "root"
	}

	account := globalYun139Config.Account

	body := map[string]interface{}{
		"getOutLinkInfoReq": map[string]interface{}{
			"account": account,
			"linkID":  linkID,
			"passwd":  "",
			"caSrt":   1,
			"coSrt":   1,
			"srtDr":   0,
			"bNum":    1,
			"pCaID":   pCaID,
			"eNum":    200,
		},
		"commonAccountInfo": map[string]interface{}{
			"account":     account,
			"accountType": 1,
		},
	}

	raw, err := yun139Post(s.chtHTTPClient(ctx), "getOutLinkInfoV6", body)
	if err != nil {
		return nil, err
	}

	var resp yun139OutLinkResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, errors.Wrapf(err, "解析分享目录响应失败: %s", truncate(string(raw), 300))
	}

	if !resp.Success && resp.ResultCode != "0" && resp.Code != "0" {
		return nil, errors.Errorf("获取分享目录失败: resultCode=%s desc=%s", resp.ResultCode, resp.Desc)
	}

	return &resp, nil
}

// fetchYun139ShareFiles 获取分享文件列表（供 GetShareFiles 调用）
func (s *service) fetchYun139ShareFiles(ctx context.Context, linkID, fileID string, addMpFn func(map[string]interface{}) map[string]interface{}) ([]converter.VirtualFileConverter, error) {
	resp, err := s.listYun139Dir(ctx, linkID, fileID)
	if err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return make([]converter.VirtualFileConverter, 0), nil
	}

	files := make([]converter.VirtualFileConverter, 0, len(resp.Data.CaLst)+len(resp.Data.CoLst))

	// 目录
	for _, ca := range resp.Data.CaLst {
		files = append(files, newYun139FolderConverter(ca, addMpFn(map[string]interface{}{
			consts.FileAdditionKeyIsFolder: true,
		})))
	}

	// 文件
	for _, co := range resp.Data.CoLst {
		files = append(files, newYun139FileConverter(co, addMpFn(map[string]interface{}{
			consts.FileAdditionKeyIsFolder: false,
		})))
	}

	return files, nil
}

// ---------------------------------------------------------------------------
// 下载直链（对应 ShareDownloadLink）
// ---------------------------------------------------------------------------

// yun139ContentInfo 是 getContentInfoFromOutLink 响应中的 contentInfo 节点
// （只声明用得到的字段）
type yun139ContentInfo struct {
	ContentID   string `json:"contentID"`
	ContentName string `json:"contentName"`
	ContentSize int64  `json:"contentSize"`
	// PresentURL 是可直接播放的 HLS 直链（m3u8，含签名，实测可 200 拉取）
	PresentURL  string `json:"presentURL"`
	PresentLURL string `json:"presentLURL"`
	PresentHURL string `json:"presentHURL"`
}

// yun139ContentInfoResponse getContentInfoFromOutLink 的响应
type yun139ContentInfoResponse struct {
	ResultCode string `json:"resultCode"`
	Desc       string `json:"desc"`
	Data       *struct {
		ContentInfo *yun139ContentInfo `json:"contentInfo"`
	} `json:"data"`
	Success bool   `json:"success"`
	Code    string `json:"code"`
}

// yun139DownloadLink 获取分享文件的真实播放/下载直链。
//
// 【重要】主用 getContentInfoFromOutLink 返回的 presentURL（HLS m3u8 直链）。
// 原因：dlFromOutLinkV3 走的是「登录态个人下载」通道，本项目的分享挂载点没有
// 云盘登录令牌（token_id 恒为 0），实测该接口恒返回
// `01000101 服务器响应异常`，拿不到任何直链。
// 而 getContentInfoFromOutLink 只需 linkID + coID，免登录即可返回带签名的
// presentURL（m3u8，实测 HTTP 200 可播），这与 OpenList 官方 139 驱动的做法一致。
// dlFromOutLinkV3 保留为兜底（一旦将来补齐登录态即可直接生效）。
func (s *service) yun139DownloadLink(ctx context.Context, linkID, coID string) (string, error) {
	account := globalYun139Config.Account

	// ---- 首选：免登录的 getContentInfoFromOutLink（拿 HLS presentURL）----
	infoBody := map[string]interface{}{
		"getContentInfoFromOutLinkReq": map[string]interface{}{
			"contentId": coID,
			"linkID":    linkID,
			"passwd":    "",
			"account":   account,
		},
	}

	if raw, err := yun139Post(s.chtHTTPClient(ctx), "getContentInfoFromOutLink", infoBody); err == nil {
		var resp yun139ContentInfoResponse
		if err := json.Unmarshal(raw, &resp); err == nil && resp.Data != nil && resp.Data.ContentInfo != nil {
			ci := resp.Data.ContentInfo

			if u := strings.TrimSpace(ci.PresentURL); u != "" {
				return u, nil
			}
			if u := strings.TrimSpace(ci.PresentHURL); u != "" {
				return u, nil
			}
			if u := strings.TrimSpace(ci.PresentLURL); u != "" {
				return u, nil
			}
		}
	} else if err != nil {
		ctx.Warn("获取分享内容信息失败，尝试下载接口兜底",
			zap.String("link_id", linkID), zap.String("co_id", coID), zap.Error(err))
	}

	// ---- 兜底：dlFromOutLinkV3（需要登录态，当前通常失败）----
	body := map[string]interface{}{
		"dlFromOutLinkReqV3": map[string]interface{}{
			"account": account,
			"linkID":  linkID,
			"passwd":  "",
			"coIDLst": map[string]interface{}{
				"item": []string{coID},
			},
		},
	}

	raw, err := yun139Post(s.chtHTTPClient(ctx), "dlFromOutLinkV3", body)
	if err != nil {
		return "", err
	}

	var resp yun139DownloadResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", errors.Wrapf(err, "解析下载直链响应失败: %s", truncate(string(raw), 300))
	}

	if resp.Data == nil {
		return "", errors.Errorf("获取下载直链失败: resultCode=%s desc=%s", resp.ResultCode, resp.Desc)
	}

	// 优先 CDN 直链，其次重定向地址
	if u := strings.TrimSpace(resp.Data.DownloadURL); u != "" {
		return u, nil
	}
	if u := strings.TrimSpace(resp.Data.RedrURL); u != "" {
		return u, nil
	}

	return "", errors.Errorf("下载直链为空: resultCode=%s desc=%s", resp.ResultCode, resp.Desc)
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
