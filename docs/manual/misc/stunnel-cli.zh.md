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

**注意：** 内部出站（`direct`、`block`、`stunnel`、`selector`、`urltest`、`dns`）不能被移除，会被跳过并显示警告。

使用 `--force` 即使移除当前活跃出站也继续执行：

```bash
$ sing-box stunnel remove proxy-auto 新加坡1
Error: outbound "新加坡1" is currently active. Use --force to proceed.

$ sing-box stunnel remove proxy-auto 新加坡1 --force
Warning: removing active outbound "新加坡1", server will re-select.
Done.

$ sing-box stunnel remove proxy-auto direct
Warning: skipping internal outbound "direct" (type: Direct)
Error: no valid outbounds to remove (all are internal types or not found)
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

从 JSON 文件应用 stunnel 组配置。创建/删除出站并设置 URL。

```bash
sing-box stunnel apply <config-file> [--force] [--mode <mode>]
```

**模式:**

| 模式 | 描述 |
|------|-------------|
| `replace` (默认) | 用配置替换协议出站（internal 类型如 direct/block 会被保留） |
| `add` | 添加新出站到现有列表（跳过重复标签） |
| `remove` | 从组中移除匹配的出站（internal 类型会被保留） |

**配置文件格式:**

只支持完整的出站配置对象，不支持标签字符串引用。

```json
{
  "proxy-auto": {
    "outbounds": [
      {
        "type": "vmess",
        "tag": "新加坡1",
        "server": "hoover.amswoop.com",
        "server_port": 31241,
        "uuid": "C74DE3A5-A547-45C1-8BA8-6FB3C0A19F2B",
        "security": "auto"
      },
      {
        "type": "vless",
        "tag": "香港1",
        "server": "example.com",
        "server_port": 443,
        "uuid": "...",
        "tls": { "enabled": true }
      }
    ],
    "url": "https://cp.cloudflare.com/"
  }
}
```

**允许的出站类型:**

协议类型（可动态管理）: `vmess`, `vless`, `trojan`, `shadowsocks`, `shadowtls`, `socks`, `http`, `wireguard`, `tuic`, `hysteria2`, `hysteria`, `naive`, `anytls`

**内部类型（预配置，不可修改）:** `block`, `direct`, `stunnel`, `selector`, `urltest`, `dns`

**示例:**

```bash
# 替换模式 (默认) - 替换协议出站，保留 internal
$ sing-box stunnel apply groups.json
Applying config for group: proxy-auto (mode: replace)
  Deleting outbound: old-proxy-1
  Keeping existing outbound: direct
  Created outbound: 新加坡1
  Created outbound: 香港1
  Updated outbounds: [direct 新加坡1 香港1]

# 添加模式 - 添加新出站
$ sing-box stunnel apply groups.json --mode add
Applying config for group: proxy-auto (mode: add)
  Outbound 香港1 already exists, skipping
  Created outbound: 美国1
  Adding outbound: 美国1
  Updated outbounds: [direct 香港1 美国1]

# 移除模式 - 移除匹配的出站
$ sing-box stunnel apply groups.json --mode remove
Applying config for group: proxy-auto (mode: remove)
  Removing outbound: 香港1
  Updated outbounds: [direct 新加坡1]
```

使用 `--force` 即使移除活跃出站也继续执行。

### 导出当前配置

导出所有 stunnel 组的配置，包含完整出站信息。

```bash
# 输出到 stdout
sing-box stunnel export

# 写入文件
sing-box stunnel export <output-file>
```

示例：

```bash
$ sing-box stunnel export
{
  "proxy-auto": {
    "outbounds": [
      {
        "type": "vmess",
        "tag": "新加坡1",
        "server": "example.com",
        "server_port": 443,
        "uuid": "...",
        "tls": { "enabled": true }
      }
    ],
    "url": "https://cp.cloudflare.com/"
  }
}
Note: Export shows full outbound configs usable with 'stunnel apply'.
```

**注意:** 导出只包含协议出站（internal 类型如 `direct`/`block` 已排除）。导出的配置可直接用于 `stunnel apply`。

## 错误处理

- `clash_api not configured`: 在配置中添加 `experimental.clash_api.external_controller`
- `unauthorized`: 检查 `clash_api.secret` 设置
- `not found`: 指定的组或出站不存在
- `is not a stunnel group`: 指定的组不是 stunnel 类型出站
- `missing 'type'/'tag' field`: 出站配置必须同时包含这两个字段