package bootstrap

import (
	"regexp"
	"time"

	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"

	"gorm.io/gorm"
	"gorm.io/gorm/utils"
)

// sqlRedactRules 针对 SQL 明文日志的脱敏规则。
//
// 139 凭据存在 setting.addition 的 JSON 串里，UPDATE setting 时整段 JSON
// 会被 Explain 拼进日志，且经历多层转义（\" 、\\\" 、...），层数随 gorm
// 版本与 Explain 行为可能变化。因此这里不假设转义层数：先锚定字段名，
// 再用 [\\"]* 吃掉字段名与值之间的所有反斜杠/引号，最后 [^"\\]* 吃掉值本身。
//
// 匹配示例（均可正确脱敏）：
//
//	{"yun139Authorization":"明文"}
//	\\\"\\\"yun139Authorization\\\"\\\":\\\"\\\"明文
var sqlRedactRules = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// yun139Authorization：139 授权串，最关键的凭据
	{regexp.MustCompile(`(yun139Authorization)([\\"]*(?:\s*[\\"]*\s*)?:\s*[\\"]*)[^"\\]*`), `${1}${2}[REDACTED]`},
	// password：登录口令
	{regexp.MustCompile(`("password)([\\"]*(?:\s*[\\"]*\s*)?:\s*[\\"]*)[^"\\]*`), `${1}${2}[REDACTED]`},
}

// redactSQL 替换 SQL 日志中的敏感字段值，避免凭据写入访问日志。
func redactSQL(sql string) string {
	if sql == "" {
		return sql
	}

	for _, rule := range sqlRedactRules {
		sql = rule.pattern.ReplaceAllString(sql, rule.replacement)
	}

	return sql
}

const (
	callBackBeforeName = "core:before"
	callBackAfterName  = "core:after"
	startTime          = "_start_time"
)

const tracePluginName = "tracePlugin"

type TracePlugin struct{}

func (op *TracePlugin) Name() string {
	return tracePluginName
}

func (op *TracePlugin) Initialize(db *gorm.DB) (err error) {
	// 开始前
	_ = db.Callback().Create().Before("gorm:before_create").Register(callBackBeforeName, before)
	_ = db.Callback().Query().Before("gorm:query").Register(callBackBeforeName, before)
	_ = db.Callback().Delete().Before("gorm:before_delete").Register(callBackBeforeName, before)
	_ = db.Callback().Update().Before("gorm:setup_reflect_value").Register(callBackBeforeName, before)
	_ = db.Callback().Row().Before("gorm:row").Register(callBackBeforeName, before)
	_ = db.Callback().Raw().Before("gorm:raw").Register(callBackBeforeName, before)

	// 结束后
	_ = db.Callback().Create().After("gorm:after_create").Register(callBackAfterName, after)
	_ = db.Callback().Query().After("gorm:after_query").Register(callBackAfterName, after)
	_ = db.Callback().Delete().After("gorm:after_delete").Register(callBackAfterName, after)
	_ = db.Callback().Update().After("gorm:after_update").Register(callBackAfterName, after)
	_ = db.Callback().Row().After("gorm:row").Register(callBackAfterName, after)
	_ = db.Callback().Raw().After("gorm:raw").Register(callBackAfterName, after)

	return nil
}

var _ gorm.Plugin = &TracePlugin{}

func before(db *gorm.DB) {
	db.InstanceSet(startTime, time.Now())
}

func after(db *gorm.DB) {
	_ctx := db.Statement.Context
	ctx, ok := _ctx.(context.Context)

	if !ok {
		return
	}

	_ts, isExist := db.InstanceGet(startTime)

	if !isExist {
		return
	}

	ts, ok := _ts.(time.Time)

	if !ok {
		return
	}

	sql := db.Explain(db.Statement.SQL.String(), db.Statement.Vars...)

	sqlInfo := new(context.SQL)
	sqlInfo.Time = time.Now().Format(consts.TimeFormat)
	sqlInfo.SQL = redactSQL(sql)
	sqlInfo.Stack = utils.FileWithLineNum()
	sqlInfo.Rows = db.Statement.RowsAffected
	sqlInfo.CostSeconds = time.Since(ts).Seconds()

	if ctx.Trace != nil {
		ctx.AppendSQL(sqlInfo)
	}
}
