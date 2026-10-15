package httpcontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/xxcheng123/cloudpan189-share/internal/framework/context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type HandlerFunc func(ctx *Context)

type HandlerFuncWrapper struct {
	logger *zap.Logger
}

func NewHandlerFuncWrapper(logger *zap.Logger) *HandlerFuncWrapper {
	return &HandlerFuncWrapper{logger: logger}
}

// logSensitiveHeaders 访问日志中需要脱敏的请求头（小写）。
// 这些头一旦明文落进日志文件，任何能读日志的人都能拿到凭据，因此统一打码。
var logSensitiveHeaders = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
	"x-auth-token":  {},
	"x-api-key":     {},
	"token":         {},
	"password":      {},
}

// logSensitiveBodyKeys JSON 请求体中需要脱敏的字段名（小写）。
// 与请求头分开维护：139 凭据是走 body 的 authorization 字段提交的。
var logSensitiveBodyKeys = map[string]struct{}{
	"authorization": {},
	"password":      {},
	"passwd":        {},
	"token":         {},
	"accesstoken":   {},
	"refreshtoken":  {},
	"secret":        {},
}

// sanitizeLogBody 脱敏 JSON 请求体 / 响应体中的敏感字段值，其余内容原样保留。
// 递归处理嵌套对象（如响应里的 data.authorization），解析失败时原样返回。
func sanitizeLogBody(body string) string {
	if body == "" {
		return ""
	}

	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return body
	}

	sanitizedPayload, changed := redactValue(payload)
	if !changed {
		return body
	}

	sanitized, err := json.Marshal(sanitizedPayload)
	if err != nil {
		return "[REDACTED]"
	}

	return string(sanitized)
}

// redactValue 递归遍历 JSON 值，把敏感 key 对应的字符串值替换为占位符。
// 返回值第二个元素表示是否发生了替换（用于避免无谓的重新序列化）。
func redactValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false

		for key, item := range typed {
			if _, ok := logSensitiveBodyKeys[strings.ToLower(key)]; ok {
				if _, isStr := item.(string); isStr {
					typed[key] = "[REDACTED]"
					changed = true

					continue
				}
			}

			updated, itemChanged := redactValue(item)
			if itemChanged {
				typed[key] = updated
				changed = true
			}
		}

		return typed, changed
	case []any:
		changed := false

		for index, item := range typed {
			updated, itemChanged := redactValue(item)
			if itemChanged {
				typed[index] = updated
				changed = true
			}
		}

		return typed, changed
	default:
		return value, false
	}
}

// sanitizeLogHeaders 克隆请求头并脱敏敏感项，避免凭据写入访问日志。
func sanitizeLogHeaders(header http.Header) http.Header {
	if header == nil {
		return nil
	}

	cloned := header.Clone()

	for key := range cloned {
		if _, ok := logSensitiveHeaders[strings.ToLower(key)]; ok {
			cloned.Set(key, "[REDACTED]")
		}
	}

	return cloned
}

const httpContextKey = "__request__context__"

func (w *HandlerFuncWrapper) Wrap(handler HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, ok := c.Get(httpContextKey); ok {
			if ctx, ok2 := v.(*Context); ok2 {
				handler(ctx)

				return
			}
		}

		ctx := newContext(c, w.logger)

		c.Set(httpContextKey, ctx)

		handler(ctx)
	}
}

func (w *HandlerFuncWrapper) Wraps(handlers ...HandlerFunc) []gin.HandlerFunc {
	hds := make([]gin.HandlerFunc, 0, len(handlers))
	for _, h := range handlers {
		hds = append(hds, w.Wrap(h))
	}

	return hds
}

func loggerHandler() HandlerFunc {
	return func(reqContext *Context) {
		var (
			ts      = time.Now()
			ctx     = reqContext.GetContext()
			traceId = ctx.ID()

			fields = make([]zap.Field, 0)
		)

		reqContext.Writer.Header().Set(traceHeaderKey, traceId)

		var (
			decodedURL, _ = url.QueryUnescape(reqContext.Request.URL.RequestURI())
			requestInfo   = &context.Request{
				TTL:        "un-limit",
				Method:     reqContext.Request.Method,
				DecodedURL: decodedURL,
				Header:     sanitizeLogHeaders(reqContext.Request.Header),
			}
		)

		// 只解析 json
		if strings.Contains(reqContext.Request.Header.Get("Content-Type"), "application/json") {
			if body, err := reqContext.GetRawData(); err == nil && len(body) > 0 {
				reqContext.Request.GetBody = func() (io.ReadCloser, error) {
					reqContext.Request.Body = io.NopCloser(bytes.NewBuffer(body))
					buffer := bytes.NewBuffer(body)
					closer := io.NopCloser(buffer)

					return closer, nil
				}

				bb, _ := reqContext.Request.GetBody()
				reqContext.Request.Body = bb

				requestInfo.Body = sanitizeLogBody(string(body))
			}
		}

		writer := &responseWriter{
			ResponseWriter: reqContext.Writer,
			b:              &bytes.Buffer{},
		}
		reqContext.Writer = writer

		defer func() {
			// region 发生 Panic 异常发送告警提醒
			if err := recover(); err != nil {
				stackInfo := string(debug.Stack())
				fields = append(fields,
					zap.Any("panic", err),
					zap.String("stack", stackInfo),
					zap.String("trace_id", traceId),
				)
				reqContext.GetContext().Error("HTTP panic recovery", fields...)
				_ = reqContext.AbortWithError(http.StatusInternalServerError, errors.New("内部服务器错误"))
			}

			cost := time.Since(ts).Seconds()
			success := reqContext.Writer.Status() == http.StatusOK

			respInfo := &context.Response{
				Header:      reqContext.Writer.Header(),
				HttpCode:    reqContext.Writer.Status(),
				HttpCodeMsg: http.StatusText(reqContext.Writer.Status()),
				CostSeconds: cost,
			}
			if strings.Contains(reqContext.Writer.Header().Get("Content-Type"), "application/json") {
				respInfo.Body = sanitizeLogBody(writer.b.String())
			}

			ctx.WithRequest(requestInfo)
			ctx.WithResponse(respInfo)

			fields = append(fields,
				zap.String("method", reqContext.Request.Method),
				zap.String("path", decodedURL),
				zap.Int("http_code", reqContext.Writer.Status()),
				zap.Bool("success", success),
				zap.Float64("cost_seconds", cost),
				zap.Any("trace_info", ctx.Trace),
				zap.Errors("errors", reqContext.errors),
				zap.String("client_ip", reqContext.ClientIP()),
			)

			ctx.Info("http-log", fields...)
		}()

		reqContext.Next()
	}
}

func LoggerHandler(logger *zap.Logger) gin.HandlerFunc {
	return NewHandlerFuncWrapper(logger).Wrap(loggerHandler())
}

type responseWriter struct {
	gin.ResponseWriter
	b *bytes.Buffer
}

func (w responseWriter) Write(data []byte) (int, error) {
	// 如果还没超限，就记录日志
	if w.b.Len() <= 1024*1024 {
		w.b.Write(data)
	}

	// 正常写响应
	return w.ResponseWriter.Write(data)
}
