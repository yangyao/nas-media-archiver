# 开发、部署与命令行

本文面向自行构建、部署或参与开发的用户。所有命令均在仓库根目录运行。
日常使用请先阅读[产品介绍](../README.md)和[安装与连接](getting-started.md)。

## 启动服务

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

详细步骤见 [QNAP 安装说明](qnap-install.md)。

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
internal/batchsplit/  拆分工具共享引擎，供服务端与 CLI 使用
apps/macos/           原生 Mac 客户端、测试、图标及打包脚本
packaging/qnap/       QPKG 生命周期脚本与安装包构建
scripts/              服务集成检查与拆分脚本入口
docs/                 使用、API、架构与验证说明
```

Go 依赖由根目录的 `go.mod` 管理，Mac 客户端使用独立的 Swift Package。未来 Web 客户端可放在 `apps/web/`，共用现有 API，构建后嵌入 Go 服务端；当前版本尚未提供完整 Web 客户端。详见 [仓库结构说明](repository-layout.md)。

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

- [Mac 客户端](../apps/macos/README.md)
- [使用指南](usage.md)
- [API 说明](service-api.md)
- [仓库结构](repository-layout.md)
- [设计说明](design.md)
- [性能测试](benchmarks.md)
- [QNAP 注意事项](qnap-notes.md)
- [验证记录](validation.md)

