# sourcemap-scan

抓 JS（**waymore + katana**）并识别 **sourcemap**。

```text
waymore + katana → 合并 JS → 校验 .map → findings.jsonl
```

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
