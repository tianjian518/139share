package cloudbridge

import (
	"time"

	"github.com/xxcheng123/cloudpan189-interface/client"
	"github.com/xxcheng123/cloudpan189-share/internal/pkgs/datatypes"
	"github.com/xxcheng123/cloudpan189-share/internal/repository/models"
	"github.com/xxcheng123/cloudpan189-share/internal/types/converter"
)

// ============================================================================
// 移动云盘（139）→ 项目内部虚拟文件结构 的适配
// ----------------------------------------------------------------------------
// 复用项目既有的 converter.FolderInfo / converter.FileInfo，
// 只需把 139 的字段映射到 client.FolderInfo / client.FileInfo 即可，
// 这样上层业务（扫描、入库、下载）完全无需改动。
// ============================================================================

// newYun139FolderConverter 把 139 目录项转换为虚拟文件转换器
func newYun139FolderConverter(ca yun139CaItem, addition datatypes.JSONMap) converter.VirtualFileConverter {
	folder := client.FolderInfo{
		CreateDate: formatYun139Time(ca.CtTime),
		LastOpTime: formatYun139Time(ca.UdTime),
		Id:         client.String(ca.CaID),
		Name:       ca.CaName,
		ParentId:   client.String("root"),
		Rev:        ca.UdTime,
		FileCata:   0,
	}

	return converter.NewFolderInfo(folder, models.OsTypeShareFolder, addition)
}

// newYun139FileConverter 把 139 文件项转换为虚拟文件转换器
func newYun139FileConverter(co yun139CoItem, addition datatypes.JSONMap) converter.VirtualFileConverter {
	file := client.FileInfo{
		CreateDate:  formatYun139Time(co.CtTime),
		LastOpTime:  formatYun139Time(co.UdTime),
		Id:          client.String(co.CoID),
		Name:        co.CoName,
		Md5:         co.Md5,
		Size:        co.CoSize,
		Rev:         co.UdTime,
		FileCata:    0,
		MediaType:   0,
		Orientation: 0,
		StarLabel:   0,
	}

	return converter.NewFileInfo(file, models.OsTypeShareFile, addition)
}

// formatYun139Time 将 139 的 20060102150405 格式转换为项目使用的 time.DateTime 格式。
// 说明：139 的 ctTime/udTime 可能缺失或为 "0"，此时解析为零值。
// 这里直接兜底为当前时间，避免把空串传给下游导致时间字段变成零值。
func formatYun139Time(s string) string {
	t := parseYun139Time(s)
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("2006-01-02 15:04:05")
}
