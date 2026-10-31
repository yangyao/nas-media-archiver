# nas-media-archiver

面向 QNAP NAS 和 Linux 的照片、视频归档工具，提供 Go 服务端与命令行、原生 macOS 客户端，支持按日期归档、重复文件比对及安全校验。

文件扫描、归档和目录清理由服务端执行，关闭 Mac 客户端不会中断任务。支持 QNAP QPKG 安装，也可以直接在 Linux 主机上运行。

## 主要功能

- 按日期归档到 `photos/YYYY/MM`、`videos/YYYY/MM` 和 `pngs/YYYY/MM`。
- 使用 `YYYYMMDD_HHMMSS_size.ext` 命名，优先读取文件名或媒体元数据中的拍摄时间，缺失时回退到修改时间。
- 可选择跳过内容相同的候选文件，保留来源；同名但内容不同的文件使用后缀另存，不覆盖已有文件。
- 跨文件系统复制并校验，确认目标与来源内容一致后再移除来源。
- 支持扫描预览、进度查看、暂停与继续、失败重试，以及空目录清理。
- 尚未开始归档的任务可以重新扫描，更新新增或移除的文件。
- 可查看来源目录中的残留文件，对需要关注的项目提供原因说明和处理操作。
- 自动排除 NAS 缩略图、回收站及 Syncthing 系统目录。

## 支持的格式

| 类型 | 格式 | 归档位置 |
| --- | --- | --- |
| 照片及图片 | `jpg`、`jpeg`、`heic`、`webp`、`gif` | `photos/年/月` |
| PNG 图片 | `png` | `pngs/年/月` |
| 视频 | `mp4`、`mov`、`3gp` | `videos/年/月` |

GIF 按原始字节归档，不转码、不拆分动画。无法识别的格式会保留在来源目录。

## 服务端与 Mac 客户端

客户端需要 macOS 26+。操作流程：

1. 连接归档服务，填写服务地址和服务端提供的 Token。
2. 选择服务端目录，扫描并查看预览。
3. 确认后开始归档，查看进度或暂停任务。
4. 完成后检查需要关注的项目及来源残留内容，按需清理空目录。

Mac 客户端默认启用“跳过重复文件”：先按拍摄时间、大小和扩展名查找候选，再逐字节确认内容；相同文件保留来源。它不需要对整个归档库计算哈希，但也不是跨任意文件名的全库去重。

### 启动服务

```bash
make build
./dist/archive serve \
  --listen 127.0.0.1:8787 \
  --state-dir /path/to/persistent-state \
  --archive-base /path/to/archive \
  --roots /path/to/input,/path/to/archive \
  --token-file /path/to/persistent-state/token \
  --name "我的归档服务"
```

服务会在 Token 文件不存在时生成随机凭证。Mac 客户端连接的是这个归档服务，不是 QTS 管理界面或 SSH。

默认只监听本机；局域网访问时应显式配置监听地址和允许访问的目录。API 需要 Token。客户端把 Token 保存在仅当前用户可访问的本地文件中，不使用钥匙串保存连接密码。在不可信网络中，应通过 HTTPS 反向代理保护连接。

### 构建 Mac 客户端

在安装 Xcode 26+ 的 Mac 上运行：

```bash
make test-macos
make macos
open apps/macos/dist/NASArchiver.app
```

可将生成的 App 复制到 `/Applications` 安装。打包优先使用本机可用的 Apple 签名身份，也可通过 `CODE_SIGN_IDENTITY` 指定。

客户端采用原生 SwiftUI 和 Liquid Glass 控件。任务侧边栏集中显示正在处理的任务与历史记录，连接设置位于左下角。同名任务用独立的 `#1`、`#2` 标记区分。

### 状态与恢复

服务运行时在内存中更新进度，由单一写入者每两秒向 SQLite 提交最多 500 条变更。异常退出可能丢失尚未保存的进度；存在积压时，丢失范围可能超过一个周期。重新启动后，需要检查并恢复任务，或重新扫描；未变化文件的已保存解析结果可以复用。

文件安全不依赖进度是否刚刚保存：目标内容仍需经过校验，来源不会仅凭任务状态被删除。CLI 使用独立的 `job.json` 和 `events.jsonl` 保存任务记录。

## 命令行使用

### 构建

```bash
go build -o archive ./cmd/archive
make build-linux-arm64
make build-linux-arm7
```

### 扫描目录

```bash
./archive scan \
  --path /path/to/input \
  --state-dir /path/to/archive-state
```

### 预演归档

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run \
  --workers 8 \
  --snapshot-every 100
```

### 正式归档

```bash
./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --workers 8 \
  --snapshot-every 100
```

命令行还提供 `status`、`watch`、`files`、`retry`、`export` 和 `jobs`，用于查看状态、跟踪进度、处理失败及导出记录。

`--workers 8` 与 `--snapshot-every 100` 是现有 ARM QNAP 测试下的 CLI 参考值，不是服务端参数。实际性能取决于文件类型、存储设备和校验开销。

## QNAP 安装与权限

详细步骤见 [QNAP 安装说明](docs/qnap-install.md)。

- 正式归档前先扫描预览或进行命令行预演。
- QNAP 共享目录采用 ACL，执行账号需要具有来源和归档目录的相应权限。
- 手动运行 CLI 时，可能需要使用 `sudo -u admin`。
- 推荐安装 ExifTool，以及用于读取视频信息的 `ffprobe` 或 `ffmpeg`。

```bash
sudo -u admin ./archive run \
  --job <job-id> \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state
```

## 代码目录

```text
cmd/archive/          Go 命令行与 HTTP 服务，构建为单个 NAS 可执行文件
  web/                嵌入服务端的状态页面
apps/macos/           原生 Mac 客户端、测试、图标及打包脚本
packaging/qnap/       QPKG 生命周期脚本与安装包构建
scripts/              服务集成检查
docs/                 使用、API、架构与验证说明
```

Go 依赖由根目录的 `go.mod` 管理，Mac 客户端使用独立的 Swift Package。未来 Web 客户端可放在 `apps/web/`，共用现有 API，构建后嵌入 Go 服务端；当前版本尚未提供完整 Web 客户端。详见 [仓库结构说明](docs/repository-layout.md)。

## 开发与验证

```bash
make test                  # Go race 测试
make smoke                 # 使用临时文件执行真实 HTTP 集成测试
make test-macos             # Swift 客户端测试
make build-linux-arm64     # Linux ARM64 构建
make build-linux-arm7      # Linux ARMv7 构建
make qnap-stage            # 生成 QDK 构建输入，不是可安装的 QPKG
```

## 项目文档

以下文档包含部分英文技术说明：

- [Mac 客户端](apps/macos/README.md)
- [使用指南](docs/usage.md)
- [API 说明](docs/service-api.md)
- [仓库结构](docs/repository-layout.md)
- [设计说明](docs/design.md)
- [性能测试](docs/benchmarks.md)
- [QNAP 注意事项](docs/qnap-notes.md)
- [验证记录](docs/validation.md)

## 许可证

[MIT](LICENSE)
