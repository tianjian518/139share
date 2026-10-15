package converter

import (
	"strings"
	"time"

	"github.com/xxcheng123/cloudpan189-interface/client"
	"github.com/xxcheng123/cloudpan189-share/internal/consts"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/datatypes"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/utils"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
)

type VirtualFileConverter interface {
	TransformVirtualFile(tid, pid int64) *models.VirtualFile
}

// parseTimeSafe 安全解析时间字符串。
// 说明：上游（139 等）返回的 ctTime/udTime 可能为空、为 "0" 或格式异常，
// 直接 time.Parse 会得到零值 time.Time，而 GORM 在批量插入时会把零值时间
// 渲染成裸 DEFAULT（SQLite 下报 near "DEFAULT": syntax error）。
// 这里统一兜底为空值，避免把非法时间带进 SQL。
func parseTimeSafe(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return time.Time{}
	}

	if t, err := time.Parse(time.DateTime, s); err == nil {
		return t
	}

	// 兼容 139 原始格式 20060102150405
	if t, err := time.Parse(consts.RevFormat, s); err == nil {
		return t
	}

	return time.Time{}
}

type ShareFileInfo struct {
	*client.ShareFileInfo
	upUserId string
}

func NewShareFileInfo(fileInfo *client.ShareFileInfo, upUserId string) *ShareFileInfo {
	return &ShareFileInfo{
		ShareFileInfo: fileInfo,
		upUserId:      upUserId,
	}
}

func (f *ShareFileInfo) TransformVirtualFile(tid, pid int64) *models.VirtualFile {
	createDate := parseTimeSafe(f.CreateDate)
	modifyDate := parseTimeSafe(f.LastOpTime)

	return &models.VirtualFile{
		ParentId:   pid,
		Name:       utils.SanitizeFileName(f.Name),
		TopId:      tid,
		IsTop:      false,
		CloudId:    string(f.Id),
		Size:       f.Size,
		IsDir:      f.Folder == 1,
		Hash:       strings.ToLower(f.Md5),
		CreateDate: createDate,
		ModifyDate: modifyDate,
		OsType:     models.OsTypeSubscribeShareFolder,
		Addition: datatypes.JSONMap{
			consts.FileAdditionKeyShareId:  f.ShareId,
			consts.FileAdditionKeyIsFolder: f.Folder,
			consts.FileAdditionKeyUpUserId: f.upUserId,
		},
		Rev: f.Rev,
	}
}

type FileInfo struct {
	*client.FileInfo
	addition datatypes.JSONMap
	osType   models.OsType
}

func NewFileInfo(input client.FileInfo, osType models.OsType, addition datatypes.JSONMap) *FileInfo {
	f := &FileInfo{
		FileInfo: &input,
		addition: addition,
		osType:   osType,
	}

	return f
}

func (f *FileInfo) TransformVirtualFile(tid, pid int64) *models.VirtualFile {
	if f.FileInfo == nil {
		return nil
	}

	file := f.FileInfo

	createDate := parseTimeSafe(file.CreateDate)
	modifyDate := parseTimeSafe(file.LastOpTime)

	return &models.VirtualFile{
		ParentId:   pid,
		TopId:      tid,
		Name:       utils.SanitizeFileName(file.Name),
		IsTop:      false,
		Size:       file.Size,
		IsDir:      false,
		Hash:       strings.ToLower(file.Md5),
		CreateDate: createDate,
		ModifyDate: modifyDate,
		OsType:     f.osType,
		Addition:   f.addition,
		Rev:        file.Rev,
		CloudId:    string(file.Id),
	}
}

type FolderInfo struct {
	*client.FolderInfo
	addition datatypes.JSONMap
	osType   models.OsType
}

func NewFolderInfo(input client.FolderInfo, osType models.OsType, addition datatypes.JSONMap) *FolderInfo {
	f := &FolderInfo{
		FolderInfo: &input,
		addition:   addition,
		osType:     osType,
	}

	return f
}

func (f *FolderInfo) TransformVirtualFile(tid, pid int64) *models.VirtualFile {
	if f.FolderInfo == nil {
		return nil
	}

	folder := f.FolderInfo

	createDate := parseTimeSafe(folder.CreateDate)
	modifyDate := parseTimeSafe(folder.LastOpTime)

	return &models.VirtualFile{
		ParentId:   pid,
		TopId:      tid,
		Name:       utils.SanitizeFileName(folder.Name),
		IsTop:      false,
		Size:       0,
		IsDir:      true,
		Hash:       "",
		CreateDate: createDate,
		ModifyDate: modifyDate,
		OsType:     f.osType,
		Addition:   f.addition,
		Rev:        folder.Rev,
		CloudId:    string(folder.Id),
	}
}
