# Stunnel CLI

`sing-box stunnel` 命令提供了通过 Clash API 动态管理 stunnel 出站组的 CLI 工具。

## 前置要求

Stunnel CLI 需要在 sing-box 配置中配置 `experimental.clash_api.external_controller`：

```json
{
  "experimental": {
    "clash_api": {
      "external_controller": "127.0.0.1:9090",
      "secret": "your-secret"
    }
  }
}
```

## 命令

### 列出组或显示详情

```bash
# 列出所有 stunnel 组
sing-box stunnel list

# 显示特定组的详情
sing-box stunnel list <group>
```

示例：

```bash
$ sing-box stunnel list
proxy-auto
backup-group

$ sing-box stunnel list proxy-auto
Group: proxy-auto
Now: 新加坡1 (120ms)
URL: https://cp.cloudflare.com/
All:
  新加坡1    120ms (active)
  新加坡2    150ms
  E-IEPL-香港6    200ms
  direct      (no history)
```

### 从组移除出站

```bash
sing-box stunnel remove <group> <outbound...>
```

使用 `--force` 即使移除当前活跃出站也继续执行：

```bash
$ sing-box stunnel remove proxy-auto 新加坡1
Error: outbound "新加坡1" is currently active. Use --force to proceed.

$ sing-box stunnel remove proxy-auto 新加坡1 --force
Warning: removing active outbound "新加坡1", server will re-select.
Done.
```

### 更新健康检查 URL

```bash
sing-box stunnel url <group> <url>
```

示例：

```bash
$ sing-box stunnel url proxy-auto https://cp.cloudflare.com/
Done.
```

### 从文件应用配置

从 JSON 文件应用 stunnel 组配置。创建/更新出站并设置 URL。

```bash
sing-box stunnel apply <config-file> [--force]
```

配置文件格式支持：

1. **标签字符串** - 引用现有出站
2. **完整出站对象** - 动态创建新出站

```json
{
  "proxy-auto": {
    "outbounds": [
      "existing-tag",
      {
        "type": "vmess",
        "tag": "新加坡1",
        "server": "hoover.amswoop.com",
        "server_port": 31241,
        "uuid": "C74DE3A5-A547-45C1-8BA8-6FB3C0A19F2B",
        "security": "auto"
      }
    ],
    "url": "https://cp.cloudflare.com/"
  },
  "backup-group": {
    "outbounds": ["美国1", "美国2"],
    "url": "https://www.google.com/generate_204"
  }
}
```

示例：

```bash
$ sing-box stunnel apply groups.json
Applying config for group: proxy-auto
  Created outbound: 新加坡1
  Updated outbounds: [existing-tag 新加坡1]
  Updated URL: https://cp.cloudflare.com/
Applying config for group: backup-group
  Updated outbounds: [美国1 美国2]
  Updated URL: https://www.google.com/generate_204
Done.
```

使用 `--force` 即使移除活跃出站也继续执行。

### 导出当前配置

导出所有 stunnel 组的配置，格式与 `apply` 相同。

```bash
# 输出到 stdout
sing-box stunnel export

# 写入文件
sing-box stunnel export <output-file>
```

示例：

```bash
$ sing-box stunnel export stunnel-config.json
Exported to: stunnel-config.json

$ sing-box stunnel export
{
  "proxy-auto": {
    "outbounds": ["新加坡1", "新加坡2", "direct"],
    "url": "https://cp.cloudflare.com/"
  },
  "backup-group": {
    "outbounds": ["美国1", "美国2"]
  }
}
```

导出格式与 `apply` 兼容，可以：

```bash
sing-box stunnel export current.json
# 编辑 current.json 添加/移除出站
sing-box stunnel apply current.json
```

## 错误处理

- `clash_api not configured`: 在配置中添加 `experimental.clash_api.external_controller`
- `unauthorized`: 检查 `clash_api.secret` 设置
- `not found`: 指定的组或出站不存在
- `is not a stunnel group`: 指定的组不是 stunnel 类型出站
- `missing 'type'/'tag' field`: 出站配置必须同时包含这两个字段