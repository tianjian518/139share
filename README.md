# 139share

一个基于移动云盘（139）/天翼云盘的文件分享和管理系统，提供 WebDAV 接口、strm 生成和 Web 管理界面。

> **版本：1.0**
>
> 专为分享链接播放场景优化的发行版：把云盘分享链接直接映射为 WebDAV / strm / HLS 播放地址。
> 云盘凭据在「系统设置」页面填写，**无需配置环境变量**。

## 🚀 项目简介

139share 是一款面向云盘分享链接的智能文件分享管理工具。它能把云盘分享链接转换为标准的目录结构，并通过 WebDAV 协议提供统一的文件访问接口，同时支持生成 strm 文件供影视端直接播放。

## ✨ 核心功能

**🔄 智能链接转换**
- 解析天翼云盘分享链接，转换为标准目录树结构
- 支持多层级文件夹映射，保持原有组织架构

**🌐 WebDAV 统一接口**
- 提供标准 WebDAV 协议支持，兼容主流客户端
- 统一文件访问入口，简化多链接管理流程
- 完整的文件锁定机制，确保并发安全

**💻 全功能网页端**
- 现代化文件浏览器界面，支持文件夹导航
- 支持在线下载、搜索功能
- 内置媒体播放器，支持视频、音频在线预览

**⚡ 高性能流媒体**
- 多线程并发传输，提升大文件访问速度
- 流式播放技术，实现视频无缓冲即时观看
- 智能带宽适配，确保播放流畅度

**📁 媒体目录映射**
- 将云盘文件通过strm形式映射到本地media_dir目录
- 完美兼容Emby、Jellyfin、Plex等媒体服务器
- 可自定义支持的视频格式列表
- 支持一键重建和批量管理

## 🚀 快速开始

### Docker 部署（推荐）

```sh
docker run -d \
  --name 139share \
  -p 12395:12395 \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/media_dir:/app/media_dir \
  --restart unless-stopped \
  tianjian518/139share:1.0
```
更多请参考文档：[CloudPan189 Share 快速开始文档](docs/1.quick_start.md)

### 访问系统
- Web 界面: `http://服务器IP:12395`
- WebDAV 地址: `http://服务器IP:12395/dav`

### 初始化设置
1. 首次打开会进入初始化页面
2. 登录后在"令牌管理"中添加天翼云盘令牌
3. 在"存储管理"中配置存储源
4. 开始使用文件管理功能

## 📁 WebDAV 挂载说明

### 挂载地址格式
```
http(s)://你的网站地址/dav
```

### 示例地址
```
http://localhost:12395/dav
```

### 支持的客户端
- **Windows**: 网络驱动器映射、RaiDrive、WinSCP
- **macOS**: Finder 连接服务器、Cyberduck
- **Linux**: davfs2、文件管理器（Nautilus、Dolphin）
- **移动端**: ES文件浏览器、Solid Explorer、FE文件管理器
- **专业工具**: Cyberduck、FileZilla Pro

### 挂载步骤
1. 打开支持 WebDAV 的客户端
2. 输入服务器地址：`http://你的域名或IP:端口/dav`
3. 输入认证信息（如需要）
4. 连接成功后即可像本地磁盘一样使用

### 注意事项
- 确保服务正常运行且端口可访问
- 部分客户端可能需要启用不安全连接（HTTP）
- 建议在生产环境中使用 HTTPS 协议

## 🛠️ 技术栈

### 后端
- **语言**: Go 1.24
- **框架**: Gin
- **数据库**: SQLite (GORM)
- **认证**: JWT

### 前端
- **框架**: Vue 3 + TypeScript
- **构建工具**: Vite
- **状态管理**: Pinia

## 📦 开发部署

### 环境要求
- Go 1.24+
- Node.js 22+
- npm 或 yarn 或 pnpm

### 1. 克隆项目
```bash
git clone https://github.com/tianjian518/139share.git
cd 139share
```

### 2. 后端部署
```bash
# 安装依赖
go mod tidy

# 构建项目
make build

# 或直接运行
go run cmd/main.go
```

### 3. 前端部署
```bash
# 进入前端目录
cd fe

# 安装依赖
npm install

# 开发模式
npm run dev

# 构建生产版本
npm run build
```

### 4. 配置文件

编辑 `etc/config.yaml` 配置文件：

```yaml
port: 12395          # 服务端口
dbFile: "data/share.db"   # 数据库文件路径
logFile: "logs/share.log" # 日志文件路径
mediaDir: "media_dir"  # 媒体文件映射目录
```

### 5. 启动服务
```bash
# 启动后端服务
go run cmd/main.go

# 启动前端开发服务器（另一个终端）
cd fe && npm run dev
```

### 6. 访问开发环境
- 前端界面: http://localhost:5173
- 后端 API: http://localhost:12395
- WebDAV 地址: http://localhost:12395/dav

## 🔧 开发指南

### 项目结构
```
139share/
├── cmd/                 # 主程序入口
├── configs/             # 配置管理
├── etc/                 # 配置文件
├── fe/                  # 前端项目
│   ├── src/
│   │   ├── api/         # API 接口
│   │   ├── components/  # 组件
│   │   ├── stores/      # 状态管理
│   │   ├── utils/       # 工具函数
│   │   └── views/       # 页面组件
├── internal/            # 内部模块
│   ├── jobs/           # 后台任务
│   ├── models/         # 数据模型
│   ├── router/         # 路由
│   └── services/       # 业务服务
└── logs/               # 日志文件
```

### API 接口
- `/api/user/*` - 用户管理
- `/api/cloudtoken/*` - 令牌管理
- `/api/storage/*` - 存储管理
- `/api/setting/*` - 系统设置
- `/dav/*` - WebDAV 接口

## ❓ 常见问题

### WebDAV 连接失败
- 检查防火墙设置，确保端口开放
- 某些客户端需要在地址末尾添加 `/`
- Windows 网络驱动器可能需要启用基本认证

### 文件播放卡顿
- 检查网络带宽和服务器性能
- 尝试降低播放质量
- 确保天翼云盘令牌有效

### 令牌失效问题
- 定期检查令牌状态
- 及时更新过期令牌
- 建议配置多个备用令牌

### Docker 相关问题
- 确保 Docker 服务正常运行
- 检查端口映射是否正确
- 数据卷挂载路径是否有权限

## 🤝 贡献

我们欢迎各种形式的贡献，包括但不限于提交 Bug 报告、功能请求、文档改进和代码贡献。请在提交之前阅读我们的贡献指南（如果可用）。

## 📄 许可证

本项目采用 MIT 许可证。详情请参阅 `LICENSE` 文件。

## 🙏 致谢

感谢所有为本项目做出贡献的开发者和社区成员。

## 💬 支持

如果您在使用过程中遇到任何问题，可以通过以下方式获得支持：

- 提交 GitHub Issue
- 查阅项目文档
- 参与社区讨论

---

⭐ 如果这个项目对您有帮助，请给它一个 Star！
