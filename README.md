# sourcemap-scan

抓 JS（**waymore + katana**）并识别 **sourcemap**。

```text
waymore（按根域去重）+ katana（按子域） → 合并 JS → 校验 .map → findings.jsonl
```

### waymore 默认策略（批量关键）

列表里是 `aaa.dell.com`、`bbb.dell.com`、`ccc.apple.com` 时：

| 步骤 | 行为 |
|------|------|
| waymore | 只对 **根域** 各跑一次：`dell.com`、`apple.com`（**不加** `-n`，一次拿全站历史） |
| 分配 | 把结果按 hostname 分回列表里的子域 |
| katana | 仍对每个子域各爬一次现网 |

100 个 `*.dell.com` → waymore **1 次**，不是 100 次。

旧行为（每个 host 单独 waymore + `-n`）：加 `-waymore-per-host`。

## 依赖

```bash
# katana
go install github.com/projectdiscovery/katana/cmd/katana@latest

# waymore
pip install waymore
```

Go `1.22+`

## 编译

```bash
go build -o sourcemap-scan ./cmd/sourcemap-scan
```

## 用法

```bash
# 批量
sourcemap-scan targets.txt

# 单个（域名或 URL）
sourcemap-scan https://app.example.com
sourcemap-scan app.example.com

# 输出 / 并发
sourcemap-scan targets.txt -o maps.jsonl -c 4

# 只要现网（更快）
sourcemap-scan targets.txt -no-waymore

# 只要历史
sourcemap-scan targets.txt -no-katana

# 打到屏幕
sourcemap-scan targets.txt -o -
```

默认输出 **`findings.jsonl`**，目标并发 **3**。

`targets.txt` 示例：

```text
https://a.example.com
b.example.com
# 注释忽略
```

## 参数

| 参数 | 说明 | 默认 |
|------|------|------|
| 位置参数 | 目标文件，或 URL/域名 | |
| `-o` | 输出 JSONL，`-` = stdout | `findings.jsonl` |
| `-c` | 同时扫几个站 | `3` |
| `-w` | 单站资产并发 | `10` |
| `-v` | 详细日志 | off |
| `-no-waymore` | 关闭 waymore | |
| `-no-katana` | 关闭 katana | |
| `-waymore-per-host` | 每个子域单独跑 waymore（慢） | off |

## 输出示例

```json
{
  "target": "https://app.example.com",
  "asset_url": "https://cdn.example.com/app.js",
  "map_url": "https://cdn.example.com/app.js.map",
  "discovered_by": "js_comment",
  "js_source": "both",
  "sources_count": 12,
  "has_sources_content": true
}
```

## 帮助

```bash
sourcemap-scan -h
```
