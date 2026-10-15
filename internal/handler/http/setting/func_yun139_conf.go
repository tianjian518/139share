package setting

import (
	"strings"

	"github.com/xxcheng123/cloudpan189-share/internal/framework/httpcontext"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/utils"
	"github.com/xxcheng123/cloudpan189-share/internal/services/cloudbridge"
)

type yun139ConfRequest struct {
	Account       *string `json:"account" example:"13800138000"` // 139 账号（手机号，可选）
	Authorization *string `json:"authorization" example:"***"`   // 139 授权串（不含 Basic 前缀，可选）
	Clear         *bool   `json:"clear" example:"false"`         // 传 true 表示清空已保存的凭据
}

type yun139ConfResponse struct {
	Account       string `json:"account"`       // 当前配置的账号
	Authorization string `json:"authorization"` // 当前配置的授权串（原样返回，便于页面回显核对）
	Configured    bool   `json:"configured"`    // 是否已配置可用凭据
}

// Yun139Conf 查询 139 凭据配置
// @Summary 查询移动云盘（139）凭据配置
// @Description 获取当前生效的 139 账号与授权串，仅管理员可访问
// @Tags 系统设置
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} httpcontext.Response{data=yun139ConfResponse} "查询成功"
// @Failure 401 {object} httpcontext.Response "未授权访问"
// @Failure 403 {object} httpcontext.Response "权限不足"
// @Router /api/setting/yun139_conf [get]
func (h *handler) GetYun139Conf() httpcontext.HandlerFunc {
	return func(ctx *httpcontext.Context) {
		setting, err := h.settingService.Query(ctx.GetContext())
		if err != nil {
			ctx.Fail(codeQueryFailed.WithError(err))

			return
		}

		ctx.Success(&yun139ConfResponse{
			Account:       setting.Addition.Yun139Account,
			Authorization: setting.Addition.Yun139Authorization,
			Configured:    strings.TrimSpace(setting.Addition.Yun139Authorization) != "",
		})
	}
}

// ModifyYun139Conf 修改 139 凭据配置
// @Summary 修改移动云盘（139）凭据配置
// @Description 保存 139 账号与授权串到数据库，并立即在内存态生效，仅管理员可操作
// @Tags 系统设置
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param request body yun139ConfRequest true "139 凭据配置（仅填写需要修改的字段）"
// @Success 200 {object} httpcontext.Response "保存成功"
// @Failure 400 {object} httpcontext.Response "参数验证失败，code=99998"
// @Failure 400 {object} httpcontext.Response "更新 139 凭据失败，code=6007"
// @Failure 401 {object} httpcontext.Response "未授权访问"
// @Failure 403 {object} httpcontext.Response "权限不足"
// @Router /api/setting/modify_yun139_conf [post]
func (h *handler) ModifyYun139Conf() httpcontext.HandlerFunc {
	return func(ctx *httpcontext.Context) {
		req := new(yun139ConfRequest)
		if err := ctx.ShouldBindJSON(req); err != nil {
			ctx.AbortWithInvalidParams(err)

			return
		}

		current, err := h.settingService.Query(ctx.GetContext())
		if err != nil {
			ctx.Fail(codeQueryFailed.WithError(err))

			return
		}

		merged := current.Addition

		if req.Clear != nil && *req.Clear {
			merged.Yun139Account = ""
			merged.Yun139Authorization = ""
		}

		if req.Account != nil {
			merged.Yun139Account = strings.TrimSpace(*req.Account)
		}

		if req.Authorization != nil {
			merged.Yun139Authorization = strings.TrimSpace(*req.Authorization)
		}

		if err := h.settingService.Update(ctx.GetContext(),
			utils.WithField("addition", merged),
		); err != nil {
			ctx.Fail(codeModifyYun139Failed.WithError(err))

			return
		}

		// 立即生效（无需重启）：设置页写入什么就以什么为准（空值 = 主动清空，必须真的清）
		cloudbridge.SetYun139ConfigExact(merged.Yun139Authorization, merged.Yun139Account)

		ctx.Success(&yun139ConfResponse{
			Account:       merged.Yun139Account,
			Authorization: merged.Yun139Authorization,
			Configured:    cloudbridge.Yun139Configured(),
		})
	}
}
