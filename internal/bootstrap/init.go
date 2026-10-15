package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/taskengine"
	"go.uber.org/zap"

	"github.com/glebarez/sqlite"
	"github.com/xxcheng123/cloudpan189-share/internal/configs"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	"github.com/xxcheng123/cloudpan189-share/internal/shared"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func useSQLiteDB(c *configs.Config) (db *gorm.DB, err error) {
	dir := filepath.Dir(c.DBFile)
	if err = os.MkdirAll(dir, 0766); err != nil {
		return
	}

	db, err = gorm.Open(sqlite.Open(c.DBFile), &gorm.Config{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to open SQLite database")
	}
	db.Exec("PRAGMA journal_mode = WAL;")
	db.Exec("PRAGMA synchronous = NORMAL;")
	db.Exec("PRAGMA busy_timeout = 5000;")

	if err = db.Use(new(TracePlugin)); err != nil {
		return nil, errors.Wrap(err, "failed to register trace plugin")
	}

	return db, nil
}

func useMySqlDB(c *configs.Config) (db *gorm.DB, err error) {
	if c.MySQL == nil {
		return nil, errors.New("MySQL configuration is required when using MySQL database")
	}

	// 构建 MySQL DSN (Data Source Name)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.MySQL.User,
		c.MySQL.Pass,
		c.MySQL.Host,
		c.MySQL.Port,
		c.MySQL.DBName,
	)

	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to MySQL database")
	}

	if err = db.Use(new(TracePlugin)); err != nil {
		return nil, errors.Wrap(err, "failed to register trace plugin")
	}

	return db, nil
}

func connectDB(c *configs.Config) (db *gorm.DB, err error) {
	switch c.DBType {
	case "mysql":
		return useMySqlDB(c)
	case "sqlite":
		return useSQLiteDB(c)
	default:
		return useSQLiteDB(c) // 默认使用 SQLite
	}
}

func assignShared(db *gorm.DB) (err error) {
	var setting = new(models.Setting)
	if err = db.First(setting).Error; err != nil {
		return err
	}

	shared.SaltKey = setting.SaltKey
	shared.BaseURL = setting.BaseURL
	shared.EnableAuth = setting.EnableAuth
	shared.SettingAddition = setting.Addition

	var mediaConfig = new(models.MediaConfig)
	if err = db.First(mediaConfig).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	shared.MediaConfig = mediaConfig

	return nil
}

func initTaskEngine(logger *zap.Logger) taskengine.TaskEngine {
	return taskengine.NewTaskEngine(taskengine.WithLogger(logger.Named("task_engine")))
}

// BootstrapHook 在数据库就绪后执行的自定义初始化钩子。
// 由 cmd 层注入，用于避免 bootstrap 反向依赖具体业务包（如 cloudbridge）造成循环导入。
type BootstrapHook func(db *gorm.DB, logger *zap.Logger) error

// bootstrapHook 保存 cmd 层注入的初始化钩子
var bootstrapHook BootstrapHook

// SetBootstrapHook 注册数据库就绪后的初始化钩子
func SetBootstrapHook(hook BootstrapHook) {
	bootstrapHook = hook
}
