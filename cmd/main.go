package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/xxcheng123/cloudpan189-share/internal/bootstrap"
	"github.com/xxcheng123/cloudpan189-share/internal/configs"
	"github.com/xxcheng123/cloudpan189-share/internal/handler/consumer"
	"github.com/xxcheng123/cloudpan189-share/internal/handler/dav"
	"github.com/xxcheng123/cloudpan189-share/internal/handler/http"
	"github.com/xxcheng123/cloudpan189-share/internal/handler/scheduler"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/shutdown"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	"github.com/xxcheng123/cloudpan189-share/internal/services/cloudbridge"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// initYun139Config 从环境变量注入移动云盘（139）登录态。
// 支持两种写法：
//   - YUN139_AUTHORIZATION：完整的 Basic 凭据（即 base64 解码后 `pc:手机号:token` 这段 token 部分）
//   - YUN139_CREDENTIAL   ：完整凭据串（可带 `pc:手机号:` 前缀），会自动拆出 token 与账号
func initYun139Config() {
	account := os.Getenv("YUN139_ACCOUNT")
	auth := os.Getenv("YUN139_AUTHORIZATION")

	if cred := os.Getenv("YUN139_CREDENTIAL"); cred != "" {
		// 形如 <deviceType>:<手机号>:<密码>|1|RCS|...|<token>
		// 实际值由使用者在「系统设置 → 云盘凭据」页面填写，源码不内置任何凭据
		if i := indexByte(cred, '|'); i > 0 {
			head, tail := cred[:i], cred[i:]
			// head = pc:手机号:密码，tail = |1|RCS|...|<token>
			last := lastIndexByte(tail, '|')
			if last >= 0 && last+1 < len(tail) {
				auth = tail[last+1:]
			}
			_ = head
		}
	}

	cloudbridge.SetYun139Config(auth, account)
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// restoreYun139LinkRegistry 从历史数据恢复 139 分享 linkID 注册表。
//
// 139 分享以字符串 linkID 定位，而底座用 int64 shareId 传递，二者映射存于
// 纯内存注册表，进程重启即清空，导致挂载点扫描不到任何文件（任务显示成功
// 但零新增且无报错）。shareId 由 linkID 稳定哈希而来，故可从数据库里已存的
// addition.link_id 回填，完整重建映射。
func restoreYun139LinkRegistry(db *gorm.DB, logger *zap.Logger) error {
	var additions []string

	// 仅取含 link_id 的记录，避免扫描全部大字段
	if err := db.Model(new(models.VirtualFile)).
		Where("addition LIKE ?", "%link_id%").
		Pluck("addition", &additions).Error; err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(additions))
	linkIDs := make([]string, 0, len(additions))

	for _, raw := range additions {
		var mp map[string]interface{}
		if json.Unmarshal([]byte(raw), &mp) != nil {
			continue
		}

		id, _ := mp["link_id"].(string)
		if id == "" {
			continue
		}

		if _, ok := seen[id]; ok {
			continue
		}

		seen[id] = struct{}{}
		linkIDs = append(linkIDs, id)
	}

	count := cloudbridge.RestoreYun139LinkRegistry(linkIDs)
	logger.Info("139 分享 linkID 注册表恢复完成",
		zap.Int("scanned", len(additions)),
		zap.Int("restored", count))

	return nil
}

// loadYun139ConfigFromDB 从「设置」页保存的数据加载 139 凭据。
//
// 优先级：设置页（数据库）> 环境变量。库里有值则覆盖启动时由环境变量
// 注入的配置；库里为空则保持环境变量值不变，两种配置方式都能用。
func loadYun139ConfigFromDB(db *gorm.DB, logger *zap.Logger) error {
	var setting models.Setting

	if err := db.Model(new(models.Setting)).First(&setting).Error; err != nil {
		// 尚未初始化系统时无记录，属正常情况
		return nil
	}

	auth := setting.Addition.Yun139Authorization
	account := setting.Addition.Yun139Account

	if strings.TrimSpace(auth) == "" && strings.TrimSpace(account) == "" {
		logger.Info("139 凭据：数据库未配置，沿用环境变量")

		return nil
	}

	cloudbridge.ApplyYun139ConfigFromDB(auth, account)
	logger.Info("139 凭据：已从设置加载",
		zap.Bool("configured", cloudbridge.Yun139Configured()),
		zap.String("account", cloudbridge.Yun139AccountValue()))

	return nil
}

func main() {
	initYun139Config()

	// 注入数据库就绪后的初始化钩子（避免 bootstrap 反向依赖 cloudbridge）
	bootstrap.SetBootstrapHook(func(db *gorm.DB, logger *zap.Logger) error {
		if err := loadYun139ConfigFromDB(db, logger); err != nil {
			logger.Error("加载 139 凭据失败", zap.Error(err))
		}

		return restoreYun139LinkRegistry(db, logger)
	})

	svc, err := bootstrap.New(configs.Get())
	if err != nil {
		panic(err)
	}

	defer svc.Close()

	logger := svc.GetLogger("main")
	taskEngine := svc.GetTaskEngine()
	httpEngine := svc.GetHTTPEngine()
	port := svc.GetPort()

	if err = consumer.Start(svc); err != nil {
		panic(err)
	}

	var (
		closeBar func()
	)

	// 启动scheduler
	if closeBar, err = scheduler.Start(svc); err != nil {
		panic(err)
	}

	http.Start(svc)
	dav.Start(svc)

	go func() {
		if err = httpEngine.Run(fmt.Sprintf(":%d", port)); err != nil {
			panic(err)
		}
	}()

	logger.Info("system running....")

	shutdown.Close(func() {
		logger.Info("close shutdown")

		if err = taskEngine.Stop(); err != nil {
			logger.Error("close task engine failed", zap.Error(err))
		}

		closeBar()

		logger.Info("close successful, bye~")
	})
}
