# zigbee-bridge

把 Zigbee2MQTT 的消息转换成 PandaX iothub 认识的网关主题，让第三方 Zigbee 传感器（如 HOBEIAN ZG-227Z / 涂鸦 ZG-203Z）接入 PandaX。

```
Zigbee2MQTT  --zigbee2mqtt/<ieee>-->  zigbee-bridge  --v1/gateway/telemetry + v1/gateway/connect-->  EMQX -> PandaX
```

## 命令

```powershell
go build -o zigbee-bridge.exe .
$env:Z2M_URL="tcp://192.168.21.151:1883"      # Zigbee2MQTT 所在 Mosquitto
$env:Z2M_USERNAME="homeassistant"
$env:Z2M_PASSWORD="123"
$env:Z2M_TOPIC_PREFIX="zigbee2mqtt"
$env:PANDAX_URL="tcp://127.0.0.1:1883"        # PandaX 的 EMQX
$env:PANDAX_GATEWAY_TOKEN="<网关设备 AuthToken>"   # 必填
$env:DEVICE_MAP="0xa4c138a8230ea245:sensor01"   # Z2M 设备名 -> PandaX 设备编码
$env:FORWARD_KEYS="temperature,humidity"        # 默认值
.\zigbee-bridge.exe
```

日志（stdout/stderr 分开重定向）：`bridge.log`、`bridge.err`。

## 行为

- 订阅 `<prefix>/+`，忽略 `bridge/*` 与 `*/availability`
- 从 payload 里挑 `FORWARD_KEYS` 指定的字段，按 `{"<设备编码>":{"ts":毫秒,"values":{...}}}` 发到 `v1/gateway/telemetry`
- 首次见到设备、或距上次超过 10 分钟，额外发 `v1/gateway/connect`：`{"<设备编码>":"online"}`，让 PandaX 把子设备标记为在线
- `DEVICE_MAP` 为空时直接用 Z2M 设备名当 PandaX 设备编码；填了则只转发映射中的设备

## 前置条件（PandaX 侧）

- 产品：网关产品（`deviceType=gateway`）+ 子设备产品（`gatewayS`），都绑 Root Rule Chain
- 设备：网关设备（拿 AuthToken）+ 子设备（编码与 `DEVICE_MAP` 的 value 一致，遥测物模型 key 与 `FORWARD_KEYS` 一致）
- EMQX 需加载 exhook 指向 PandaX iothub（见 `../PandaX/AGENTS.md`）

## 注意

- `PANDAX_GATEWAY_TOKEN` 是 MQTT 用户名，缺失直接 Fatal 退出
- Z2M 侧不要随意设置 `humidity_calibration` / `temperature_unit`，否则会得到 -3% 这种异常湿度
- PandaX 只在收到消息后才写 `iot.<设备名>_telemetry`，设备未上线时查询会得到空结果
