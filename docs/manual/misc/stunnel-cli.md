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

Use `--force` to proceed even if removing the currently active outbound:

```bash
$ sing-box stunnel remove proxy-auto 新加坡1
Error: outbound "新加坡1" is currently active. Use --force to proceed.

$ sing-box stunnel remove proxy-auto 新加坡1 --force
Warning: removing active outbound "新加坡1", server will re-select.
Done.
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

Apply stunnel group configuration from a JSON file. Creates/updates outbounds and sets URLs.

```bash
sing-box stunnel apply <config-file> [--force]
```

The config file format supports:

1. **Tag strings** - reference existing outbounds
2. **Full outbound objects** - create new outbounds dynamically

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

Example:

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

Use `--force` to proceed even if removing active outbounds.

### Export current config

Export all stunnel groups' configuration in the same format as `apply`.

```bash
# Print to stdout
sing-box stunnel export

# Write to file
sing-box stunnel export <output-file>
```

Example:

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

The exported format is compatible with `apply`, so you can:

```bash
sing-box stunnel export current.json
# Edit current.json to add/remove outbounds
sing-box stunnel apply current.json
```

## Error Handling

- `clash_api not configured`: Add `experimental.clash_api.external_controller` to your config
- `unauthorized`: Check your `clash_api.secret` setting
- `not found`: The specified group or outbound doesn't exist
- `is not a stunnel group`: The specified group is not a stunnel type outbound
- `missing 'type'/'tag' field`: Outbound config must have both fields