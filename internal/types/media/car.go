package media

import (
	"path"
	"path/filepath"
	"strings"
)

type WriterCar interface {
	RootPath() string
	NewSubCar(filePath string) WriterCar
	GetFileConflictPolicy() FileConflictPolicy
	GetBaseURL() string
	// GetFullPath 返回 car 指向的文件的完整路径
	GetFullPath() string
	// GetPath 返回 car 指向的文件的路径（相对于根路径）
	GetPath() string
	// GetName 获取本级文件名
	GetName() string
	// SetPath 整体替换 car 指向的相对路径（用于遍历开始时归一化挂载点路径）
	SetPath(filePath string) WriterCar
}

func NewWriterCar(rootPath string, conflictPolicy FileConflictPolicy, baseURL string) WriterCar {
	return &writerCar{
		rootPath:       rootPath,
		conflictPolicy: conflictPolicy,
		baseURL:        baseURL,
	}
}

type writerCar struct {
	rootPath       string
	conflictPolicy FileConflictPolicy
	baseURL        string
	fullPath       string
}

func (c *writerCar) RootPath() string {
	return c.rootPath
}

func (c *writerCar) NewSubCar(filePath string) WriterCar {
	// 注意：挂载点 FullPath 形如 `/完美世界`（带前导斜杠）。
	// 若不剥掉，第一次 path.Join("", "/完美世界") 会得到绝对路径 `/完美世界`，
	// 后续 filepath.Join(rootPath, "/完美世界/…") 会丢弃 rootPath，
	// 最终写出 `mkdir /完美世界: permission denied`。
	// 这里统一按"相对 rootPath 的目录名"处理，保证 GetFullPath 始终落在 StoragePath 之内。
	filePath = strings.TrimPrefix(filePath, "/")

	return &writerCar{
		rootPath:       c.rootPath,
		conflictPolicy: c.conflictPolicy,
		baseURL:        c.baseURL,
		fullPath:       path.Join(c.fullPath, filePath),
	}
}

func (c *writerCar) GetFileConflictPolicy() FileConflictPolicy {
	return c.conflictPolicy
}

func (c *writerCar) GetBaseURL() string {
	return c.baseURL
}

func (c *writerCar) GetFullPath() string {
	return filepath.Join(c.rootPath, c.fullPath)
}

func (c *writerCar) GetPath() string {
	return path.Join(c.fullPath)
}

func (c *writerCar) GetName() string {
	return path.Base(c.fullPath)
}

// SetPath 用给定路径整体替换本 car 的相对路径，返回新 car（不修改原 car）。
// 用于遍历挂载点前把「挂载点 FullPath」归一化：
// 挂载点末段若与云盘根节点同名，剥掉末段，避免同一目录名在 strm 路径里出现两次。
func (c *writerCar) SetPath(filePath string) WriterCar {
	filePath = strings.TrimPrefix(filePath, "/")

	return &writerCar{
		rootPath:       c.rootPath,
		conflictPolicy: c.conflictPolicy,
		baseURL:        c.baseURL,
		fullPath:       path.Clean(filePath),
	}
}
