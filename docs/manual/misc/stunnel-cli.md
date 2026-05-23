# Stunnel CLI

The `sing-box stunnel` command provides CLI tools for managing stunnel outbound groups dynamically via the Clash API.

## Prerequisites

The stunnel CLI requires `experimental.clash_api.external_controller` to be configured in your sing-box config:

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

## Commands

### List groups or show details

```bash
# List all stunnel groups
sing-box stunnel list

# Show details of a specific group
sing-box stunnel list <group>
```

Examples:

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

### Remove outbounds from a group

```bash
sing-box stunnel remove <group> <outbound...>
```

**Note:** Internal outbounds (`direct`, `block`, `stunnel`, `selector`, `urltest`, `dns`) cannot be removed. They will be skipped with a warning.

Use `--force` to proceed even if removing the currently active outbound:

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

### Update health check URL

```bash
sing-box stunnel url <group> <url>
```

Example:

```bash
$ sing-box stunnel url proxy-auto https://cp.cloudflare.com/
Done.
```

### Apply config from file

Apply stunnel group configuration from a JSON file. Creates/deletes outbounds and sets URLs.

```bash
sing-box stunnel apply <config-file> [--force] [--mode <mode>]
```

**Modes:**

| Mode | Description |
|------|-------------|
| `replace` (default) | Replace protocol outbounds with config (internal outbounds like direct/block are preserved) |
| `add` | Add new outbounds to existing (skip duplicate tags) |
| `remove` | Remove matching outbounds from group (internal outbounds are preserved) |

**Config file format:**

Only full outbound objects are allowed. Tag strings are not supported.

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

**Allowed outbound types:**

Protocol types (can be dynamically managed): `vmess`, `vless`, `trojan`, `shadowsocks`, `shadowtls`, `socks`, `http`, `wireguard`, `tuic`, `hysteria2`, `hysteria`, `naive`, `anytls`

**Internal types (pre-configured, cannot be modified):** `block`, `direct`, `stunnel`, `selector`, `urltest`, `dns`

**Examples:**

```bash
# Replace mode (default) - replace protocol outbounds, keep internal
$ sing-box stunnel apply groups.json
Applying config for group: proxy-auto (mode: replace)
  Deleting outbound: old-proxy-1
  Keeping existing outbound: direct
  Created outbound: 新加坡1
  Created outbound: 香港1
  Updated outbounds: [direct 新加坡1 香港1]

# Add mode - add new outbounds
$ sing-box stunnel apply groups.json --mode add
Applying config for group: proxy-auto (mode: add)
  Outbound 香港1 already exists, skipping
  Created outbound: 美国1
  Adding outbound: 美国1
  Updated outbounds: [direct 香港1 美国1]

# Remove mode - remove matching outbounds
$ sing-box stunnel apply groups.json --mode remove
Applying config for group: proxy-auto (mode: remove)
  Removing outbound: 香港1
  Updated outbounds: [direct 新加坡1]
```

Use `--force` to proceed even if removing active outbound.

### Export current config

Export all stunnel groups' configuration.

```bash
# Print to stdout
sing-box stunnel export

# Write to file
sing-box stunnel export <output-file>
```

Example:

```bash
$ sing-box stunnel export
{
  "proxy-auto": {
    "outbounds": ["新加坡1", "香港1"],
    "url": "https://cp.cloudflare.com/"
  }
}
Note: Export shows tag names only. For 'stunnel apply', you need full outbound configs.
```

**Note:** The export only shows protocol outbound tag names (internal types like `direct`/`block` are excluded). The Clash API doesn't provide full outbound configurations. For `stunnel apply`, you need to maintain full outbound configs separately.

## Error Handling

- `clash_api not configured`: Add `experimental.clash_api.external_controller` to your config
- `unauthorized`: Check your `clash_api.secret` setting
- `not found`: The specified group or outbound doesn't exist
- `is not a stunnel group`: The specified group is not a stunnel type outbound
- `missing 'type'/'tag' field`: Outbound config must have both fields