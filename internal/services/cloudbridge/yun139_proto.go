package cloudbridge

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// ============================================================================
// 移动云盘（139）分享协议实现
// ----------------------------------------------------------------------------
// 与天翼云盘（cloudpan189）无关，是独立的协议层实现。
// 核心机制（均已实测通过）：
//   1. 请求体 AES-128-CBC 加密，密钥 = hex("PVGDwmcvfs1uV3d1") 解码后的 16 字节
//   2. 请求体 JSON 必须按键名递归排序，否则服务端 500
//   3. 响应同样是 AES 加密（可能 gzip 后加密），需解密
//   4. 下载直链必须走 dlFromOutLinkV3，getContentInfoFromOutLink 只给 HLS 地址（有防盗链 403）
// ============================================================================

const (
	// 139 分享接口网关
	yun139ShareHost = "https://share-kd-njs.yun.139.com"
	// 139 分享接口前缀（注意：不是 yun-share/richlifeApp/devapp/IOutLink，
	// 而是 yun-share/richlifeApp/devapp/IOutLink 之下的具体方法名，见下方 path 拼接）
	yun139SharePrefix = "/yun-share/richlifeApp/devapp/IOutLink"

	// 139 协议固定密钥：字符串 "PVGDwmcvfs1uV3d1" 的十六进制字符串形式，
	// 使用时需 hex 解码得到 16 字节 AES-128 密钥
	yun139AesKeySeed = "PVGDwmcvfs1uV3d1"

	// 139 分享链接前缀，用于剥离成 linkID
	yun139ShareURLPrefix = "https://yun.139.com/shareweb/#/w/i/"
)

var (
	// yun139AesKey 是 hex 解码后的 16 字节 AES 密钥
	yun139AesKey = mustHexDecode(hex.EncodeToString([]byte(yun139AesKeySeed)))

	// 默认用户标识（登录账号手机号）留空。
	// 139 凭据一律由用户在「系统设置 → 云盘凭据」页面自行填写并存入数据库，
	// 源码中不内置任何账号，避免分发版本夹带维护者个人信息。
	yun139DefaultAccount = ""
)

func mustHexDecode(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// yun139Config 保存协议层运行所需的凭据与配置
type yun139Config struct {
	// Authorization 139 授权串（Basic 认证的凭据部分，不含 "Basic " 前缀）
	Authorization string
	// Account 登录账号（手机号）
	Account string
	// DeviceInfo 设备信息头
	DeviceInfo string
}

// defaultYun139Config 返回默认配置，可通过 SetYun139Config 覆盖
var globalYun139Config = &yun139Config{
	Account:    yun139DefaultAccount,
	DeviceInfo: "||9|12.27.0|firefox|140.0|||linux unknown|1920X526|zh-CN|||",
}

// SetYun139Config 设置用户提供的 Authorization（用户 Authorization 串）
func SetYun139Config(authorization, account string) {
	if strings.TrimSpace(authorization) != "" {
		globalYun139Config.Authorization = strings.TrimSpace(authorization)
	}
	if strings.TrimSpace(account) != "" {
		globalYun139Config.Account = strings.TrimSpace(account)
	}
}

// ApplyYun139ConfigFromDB 用「设置」页保存的凭据覆盖内存态。
//
// 优先级：设置页（数据库）> 环境变量。启动时若库里已有值，则库值覆盖；
// 库里为空则保留环境变量注入的值，做到两种方式都能用、且页面填写的为准。
func ApplyYun139ConfigFromDB(authorization, account string) {
	if strings.TrimSpace(authorization) != "" {
		globalYun139Config.Authorization = strings.TrimSpace(authorization)
	}
	if strings.TrimSpace(account) != "" {
		globalYun139Config.Account = strings.TrimSpace(account)
	}
}

// SetYun139ConfigExact 用「设置」页写入的值**原样覆盖**内存态（含空值）。
//
// 与 ApplyYun139ConfigFromDB 的区别：后者用于启动加载，空值表示"库里没配，
// 保留环境变量"；本函数用于设置页保存，空值表示"用户主动清空"，必须真的清掉，
// 否则会出现「页面已清空、但内存里旧 token 还在生效」的不一致。
func SetYun139ConfigExact(authorization, account string) {
	globalYun139Config.Authorization = strings.TrimSpace(authorization)
	globalYun139Config.Account = strings.TrimSpace(account)
}

// Yun139Configured 返回当前是否已具备可用的 139 授权串。
func Yun139Configured() bool {
	return strings.TrimSpace(globalYun139Config.Authorization) != ""
}

// Yun139AccountValue 返回当前生效的 139 账号。
func Yun139AccountValue() string {
	return globalYun139Config.Account
}

// ============================================================================
// 加密 / 解密
// ============================================================================

// sortedJSONStringify 按键名递归排序后序列化 JSON。
// 139 服务端要求请求体 JSON 的 key 严格有序，否则返回 500。
func sortedJSONStringify(obj interface{}) (string, error) {
	var buf bytes.Buffer
	if err := writeSortedJSON(&buf, obj); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func writeSortedJSON(buf *bytes.Buffer, obj interface{}) error {
	switch v := obj.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(b)
	case float64:
		b, _ := json.Marshal(v)
		buf.Write(b)
	case int:
		fmt.Fprintf(buf, "%d", v)
	case int64:
		fmt.Fprintf(buf, "%d", v)
	case json.Number:
		buf.WriteString(v.String())
	case []interface{}:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeSortedJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case []string:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeSortedJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeSortedJSON(buf, v[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		// 兜底：走 json.Marshal 再解析（保序能力有限，但至少不 panic）
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var parsed interface{}
		if err := json.Unmarshal(b, &parsed); err != nil {
			return err
		}
		return writeSortedJSON(buf, parsed)
	}
	return nil
}

// yun139Encrypt 对请求体进行 AES-128-CBC 加密，返回 base64(IV + 密文)
func yun139Encrypt(obj interface{}) (string, error) {
	plain, err := sortedJSONStringify(obj)
	if err != nil {
		return "", errors.Wrap(err, "序列化请求体失败")
	}

	block, err := aes.NewCipher(yun139AesKey)
	if err != nil {
		return "", errors.Wrap(err, "创建 AES cipher 失败")
	}

	data := []byte(plain)
	// PKCS7 padding
	pad := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)

	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", errors.Wrap(err, "生成 IV 失败")
	}

	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)

	raw := append(iv, out...)
	return base64.StdEncoding.EncodeToString(raw), nil
}

// yun139Decrypt 解密 139 响应体（base64(IV+密文)，可能为 gzip 后加密）
func yun139Decrypt(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("空响应")
	}

	// 非加密响应（明文 JSON）直接返回
	if strings.HasPrefix(encoded, "{") || strings.HasPrefix(encoded, "[") {
		return []byte(encoded), nil
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.Wrap(err, "base64 解码失败")
	}
	if len(raw) <= aes.BlockSize {
		return nil, errors.New("密文长度不足")
	}

	iv, ct := raw[:aes.BlockSize], raw[aes.BlockSize:]

	block, err := aes.NewCipher(yun139AesKey)
	if err != nil {
		return nil, errors.Wrap(err, "创建 AES cipher 失败")
	}

	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)

	// 去 PKCS7 padding
	if len(out) == 0 {
		return nil, errors.New("解密结果为空")
	}
	pad := int(out[len(out)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("padding 非法")
	}
	out = out[:len(out)-pad]

	// gzip 解压
	if len(out) >= 2 && out[0] == 0x1f && out[1] == 0x8b {
		return gunzip(out)
	}

	return out, nil
}

// ============================================================================
// 请求发送
// ============================================================================

// yun139Post 发送一个加密 POST 请求并返回解密后的响应体。
// httpClient 为 nil 时使用默认客户端。
func yun139Post(httpClient *http.Client, method string, body interface{}) ([]byte, error) {
	if globalYun139Config.Authorization == "" {
		return nil, errors.New("未配置 139 Authorization")
	}

	encrypted, err := yun139Encrypt(body)
	if err != nil {
		return nil, err
	}

	url := yun139ShareHost + yun139SharePrefix + "/" + method

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(encrypted))
	if err != nil {
		return nil, errors.Wrap(err, "构造请求失败")
	}

	yun139ApplyHeaders(req)

	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "请求 139 接口失败")
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "读取响应失败")
	}

	plain, err := yun139Decrypt(string(raw))
	if err != nil {
		return nil, errors.Wrapf(err, "解密响应失败(HTTP %d)", resp.StatusCode)
	}

	return plain, nil
}

// yun139ApplyHeaders 设置 139 接口所需的全部请求头
func yun139ApplyHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("X-Deviceinfo", globalYun139Config.DeviceInfo)
	req.Header.Set("hcy-cool-flag", "1")
	req.Header.Set("CMS-DEVICE", "default")
	req.Header.Set("x-m4c-caller", "PC")
	req.Header.Set("X-Yun-Api-Version", "v1")
	req.Header.Set("Origin", "https://yun.139.com")
	req.Header.Set("Referer", "https://yun.139.com/")
	req.Header.Set("Authorization", "Basic "+globalYun139Config.Authorization)
}

// gunzip 解压 gzip 数据
func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errors.Wrap(err, "gzip 解压失败")
	}
	defer func() { _ = zr.Close() }()

	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, errors.Wrap(err, "读取 gzip 流失败")
	}

	return out, nil
}
