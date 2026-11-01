# 按大小拆分目录

在 Mac 菜单选择“工具 → 拆分目录”，选择服务端目录后点击“预览”，批次名称自动沿用来源目录名。
确认目录列表后，由用户点击“开始拆分”。默认每批最多 **8 GB（8,000,000,000 字节）**。

例如来源 `/share/ssd/upload/iphone15`，自动使用前缀 `iphone15`：

```text
upload/
  iphone15/               # 原目录保留，隐藏同步项目留在这里
  iphone15_batch_01/
  iphone15_batch_02/
```

不足 8 GB 也会移到一个平级批次；没有普通文件时不创建目录。
文件保持完整，单个文件超限时独立成批并显示提示，不拆碎视频。
批次目录使用下划线命名，如 `iphone15_batch_01`；输入名称中的空白会自动转为下划线。
已有批次不会覆盖，编号会顺延。关闭工具后可以重新打开查看进度与最近任务。
重启或失败后点击“继续拆分”核对和恢复；文件在预览后变化时需重新预览。

## 命令行与脚本

同一套 Go 实现同时提供 CLI，脚本入口为 `scripts/split-directory.sh`。
不需要 NAS 安装 Python、ExifTool 或其他处理媒体的工具。

```sh
make build
# 默认只读预览
scripts/split-directory.sh --path /path/to/iphone15 --prefix iphone15
# 显式移动；恢复计划放在来源目录之外的持久存储上
scripts/split-directory.sh --path /path/to/iphone15 --prefix iphone15 \
  --apply --plan-file /persistent/plans/iphone15.json
# 中断后继续同一计划
scripts/split-directory.sh --resume --apply --plan-file /persistent/plans/iphone15.json
```

在已安装的 QNAP 上，可设置 `ARCHIVE_BIN` 为 QPKG 内 `bin/archive` 路径，
或直接执行 `archive split-directory`。CLI 应由管理员保证没有归档或同步写入同一目录；
App/API 会检查服务内的任务占用，但不能锁住外部同步程序。

工具只做同文件系统内的文件名移动，不重复存储文件内容，不压缩、不计算全量哈希。
保留隐藏项目和符号链接，不跟随它们移动内容。源目录保持存在；普通空子目录会清理。
目标文件的发布不覆盖已有路径，移除来源前同步目标目录，并持久化恢复计划。
