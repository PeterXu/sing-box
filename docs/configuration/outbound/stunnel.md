### Structure

```json
{
  "type": "stunnel",
  "tag": "auto",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "url": "",
  "interval": "",
  "tolerance": 0,
  "idle_timeout": "",
  "cooldown": "",
  "interrupt_exist_connections": false
}
```

!!! quote ""

    Stunnel is a dynamic outbound group that selects the lowest-latency outbound via health checks and automatically fails over to the next available outbound when a connection fails. Unlike urltest, stunnel retries connections through alternative outbounds on failure.

### Fields

#### outbounds

==Required==

List of outbound tags to select from.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

#### tolerance

The test tolerance in milliseconds. `50` will be used if empty. If a new outbound has a delay within tolerance of the current best, the selection won't change.

#### idle_timeout

The idle timeout. `30m` will be used if empty. Health checks stop when no connections have been made for this duration.

#### cooldown

!!! question "Since sing-box 1.11.0"

The cooldown period after an outbound fails. `30s` will be used if empty. Failed outbounds are temporarily excluded from selection for this duration.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.