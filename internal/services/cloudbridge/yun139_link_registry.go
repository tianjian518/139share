package cloudbridge

import (
	"hash/fnv"
	"strings"
	"sync"
)

// ============================================================================
// 分享链接注册表
// ----------------------------------------------------------------------------
// 项目底座的接口签名使用 int64 的 shareId 定位分享，
// 而移动云盘（139）使用字符串 linkID（如 2ygb7wmcRM82b）。
//
// 本注册表维护 "shareId(int64) <-> linkID(string)" 的双向映射：
//   • 校验分享（CheckShare）时把 linkID 注册进来并计算稳定的 shareId
//   • 列举文件 / 取直链时通过 shareId 反查回 linkID
//
// shareId 由 linkID 经 FNV-1a 哈希得到（稳定、可复现，进程重启后一致）。
// ============================================================================

var yun139LinkRegistry = struct {
	sync.RWMutex
	byShareID map[int64]string
	byLinkID  map[string]int64
	default1  string
}{
	byShareID: make(map[int64]string),
	byLinkID:  make(map[string]int64),
}

// hashYun139LinkID 把字符串 linkID 稳定地映射为 int64 shareId
func hashYun139LinkID(linkID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(linkID))

	// 取低 63 位，避免负数造成展示/存储困扰
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}

// registerYun139Link 注册 linkID，返回其 shareId
func registerYun139Link(linkID string) int64 {
	linkID = strings.TrimSpace(linkID)
	if linkID == "" {
		return 0
	}

	shareID := hashYun139LinkID(linkID)

	yun139LinkRegistry.Lock()
	defer yun139LinkRegistry.Unlock()

	yun139LinkRegistry.byShareID[shareID] = linkID
	yun139LinkRegistry.byLinkID[linkID] = shareID

	// 记录"最近一次注册"的分享，作为兜底默认值
	yun139LinkRegistry.default1 = linkID

	return shareID
}

// lookupYun139LinkID 通过 shareId 反查 linkID
func lookupYun139LinkID(shareID int64) (string, bool) {
	yun139LinkRegistry.RLock()
	defer yun139LinkRegistry.RUnlock()

	v, ok := yun139LinkRegistry.byShareID[shareID]

	return v, ok
}

// defaultYun139LinkID 返回最近一次注册（或配置的默认）linkID
func defaultYun139LinkID() string {
	yun139LinkRegistry.RLock()
	defer yun139LinkRegistry.RUnlock()

	if yun139LinkRegistry.default1 != "" {
		return yun139LinkRegistry.default1
	}

	return ""
}

// resolveYun139LinkID 解析出可用的 linkID。
// 优先：传入的 accessCode/shareCode 本身携带了 linkID（新链接）；
// 其次：通过 shareId 反查注册表；
// 最后：回退到默认 linkID。
func resolveYun139LinkID(shareID int64, hint string) string {
	// hint 可能是完整链接、裸 linkID，或访问码
	if hint != "" {
		if id := parseYun139LinkID(hint); id != "" && strings.Contains(hint, "/w/i/") {
			registerYun139Link(id)
			return id
		}
	}

	if v, ok := lookupYun139LinkID(shareID); ok {
		return v
	}

	if hint != "" {
		if id := parseYun139LinkID(hint); id != "" {
			registerYun139Link(id)
			return id
		}
	}

	return defaultYun139LinkID()
}

// ============================================================================
// 注册表持久化恢复
// ----------------------------------------------------------------------------
// 注册表是纯内存结构，进程重启后会清空，导致所有挂载点退化为「无 linkID」
// 而扫描不到任何文件（表现为 task completed 但零新增、且无报错）。
//
// 由于 shareId = FNV-1a(linkID) 是稳定哈希且已写入数据库，只要能把历史
// 出现过的 linkID 重新灌回注册表，映射即可完整重建。
// 因此启动时从 virtual_files.addition 中提取 link_id 做一次回填。
// ============================================================================

// RestoreYun139LinkRegistry 从数据库已存记录中恢复 linkID 注册表。
// linkIDs 为历史出现过的 linkID 列表（去重后传入即可）。
func RestoreYun139LinkRegistry(linkIDs []string) int {
	count := 0

	for _, id := range linkIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}

		if _, ok := yun139LinkRegistry.byLinkID[id]; ok {
			continue
		}

		registerYun139Link(id)
		count++
	}

	return count
}
