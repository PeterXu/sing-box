### 结构

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

    Stunnel 是一个动态出站组，通过健康检查选择最低延迟的出站，并在连接失败时自动切换到下一个可用出站。与 urltest 不同，stunnel 在失败时会尝试通过其他出站重试连接。

### 字段

#### outbounds

==必填==

要选择的出站标签列表。

#### url

测试 URL。留空时使用 `https://www.gstatic.com/generate_204`。

#### interval

测试间隔。留空时使用 `3m`。

#### tolerance

测试容差（毫秒）。留空时使用 `50`。如果新出站的延迟在当前最佳出站的容差范围内，选择不会改变。

#### idle_timeout

空闲超时。留空时使用 `30m`。当没有连接活动时，健康检查会停止。

#### cooldown

!!! question "sing-box 1.11.0"

出站失败后的冷却期。留空时使用 `30s`。失败的出站在此期间暂时排除在选择之外。

#### interrupt_exist_connections

当选择的出站改变时中断现有连接。

此设置仅影响入站连接，内部连接始终会被中断。