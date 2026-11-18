# zigbee-bridge

把 **Zigbee2MQTT** 上报的第三方 Zigbee 传感器数据转换成 **PandaX iothub** 能识别的网关报文，让涂鸦/HOBEIAN 等 Zigbee 温湿度传感器（如 ZG-227Z、ZG-203Z）接入 PandaX。

```
Zigbee 传感器 ──Zigbee──> Zigbee2MQTT ──MQTT──> Mosquitto
   ──本项目──> EMQX(用户名=网关设备 AuthToken) ──exhook──> PandaX iothub ──> 规则链 ──> TDengine
```

## 为什么需要它

Zigbee2MQTT 和 PandaX 的 MQTT 约定完全不同，中间必须转换：

| | Zigbee2MQTT | PandaX iothub |
|---|---|---|
| 主题 | `zigbee2mqtt/0xa4c138a8230ea245` | `v1/gateway/telemetry`（网关代子设备上报） |
| 报文 | `{"temperature":24.8,"humidity":58.2,"battery":90}` | `{"sensor01":{"ts":1700000000000,"values":{"temperature":24.8,"humidity":58.2}}}` |
| 认证 | 用户名/密码（Mosquitto 账号） | 用户名必须是网关设备的 `AuthToken` |
| 在线状态 | `zigbee2mqtt/<设备>/availability` | `v1/gateway/connect` → `{"sensor01":"online"}` |

本项目只做三件事：**选字段 → 改结构 → 换主题**，另外补发在线状态。

## 快速开始

### 1. 前置条件

- Zigbee2MQTT 已在运行，设备已配对并能通过 `zigbee2mqtt/<ieee>` 收到状态
- PandaX 后端 + EMQX 已运行，且 EMQX 已加载 exhook 指向 iothub
- PandaX 中已创建网关设备（拿到 **AuthToken**）与子设备

### 2. 构建

```bash
go build -o zigbee-bridge .
```

无第三方依赖之外的额外要求（仅 `github.com/eclipse/paho.mqtt.golang`）。

### 3. 配置并运行

```bash
export Z2M_URL="tcp://192.168.21.151:1883"      # Zigbee2MQTT 所在 Mosquitto
export Z2M_USERNAME="homeassistant"
export Z2M_PASSWORD="123"
export PANDAX_URL="tcp://127.0.0.1:1883"        # PandaX 的 EMQX
export PANDAX_GATEWAY_TOKEN="<网关设备 AuthToken>"
export DEVICE_MAP="0xa4c138a8230ea245:sensor01"  # Z2M 设备名 → PandaX 设备编码
./zigbee-bridge
```

Windows PowerShell：

```powershell
$env:Z2M_URL="tcp://192.168.21.151:1883"; $env:Z2M_USERNAME="homeassistant"; $env:Z2M_PASSWORD="123"
$env:PANDAX_URL="tcp://127.0.0.1:1883"; $env:PANDAX_GATEWAY_TOKEN="<token>"
$env:DEVICE_MAP="0xa4c138a8230ea245:sensor01"
.\zigbee-bridge.exe
```

### 环境变量

| 变量 | 默认值 | 必填 | 说明 |
|---|---|---|---|
| `PANDAX_GATEWAY_TOKEN` | 无 | **是** | PandaX 网关设备的 AuthToken，作为 MQTT 用户名；缺失直接退出 |
| `PANDAX_URL` | `tcp://127.0.0.1:1883` | 否 | PandaX 的 EMQX 地址 |
| `Z2M_URL` | `tcp://127.0.0.1:1883` | 否 | Zigbee2MQTT 使用的 Broker |
| `Z2M_USERNAME` | `homeassistant` | 否 | Z2M Broker 账号 |
| `Z2M_PASSWORD` | 空 | 否 | Z2M Broker 密码 |
| `Z2M_TOPIC_PREFIX` | `zigbee2mqtt` | 否 | 订阅 `<prefix>/+` |
| `DEVICE_MAP` | 空 | 建议 | `ieee:设备编码` 逗号分隔；为空时直接用 Z2M 设备名当编码 |
| `FORWARD_KEYS` | `temperature,humidity` | 否 | 需要转发的字段，逗号分隔 |

## PandaX 侧准备

**产品**（设备管理 → 产品管理）

1. 网关产品：设备类型 `网关设备`、协议 `MQTT`、绑定 Root Rule Chain
2. 子设备产品：设备类型 `网关子设备`、协议 `MQTT`、绑定 Root Rule Chain
   物模型 → 遥测，添加 `temperature`(double, ℃) 与 `humidity`(double, %)

**设备**（设备管理 → 设备管理）

1. 网关设备（如 `zigbeegw`）：保存后到「查看 → 设备详情 → MQTT 认证配置」复制 **AuthToken**
2. 子设备（如 `sensor01`）：设备类型选「网关子设备」，网关设备选上面那台；编码必须与 `DEVICE_MAP` 的值一致

**EMQX 加载 exhook**（容器重建后需重做）

```powershell
docker cp PandaX\deploy\emqx-exhook.conf pandax-emqx:/tmp/exhook.conf
docker exec pandax-emqx sh -c "emqx ctl conf load /tmp/exhook.conf"
docker exec pandax-emqx sh -c "emqx ctl conf show exhook"   # 应指向 host.docker.internal:9001
```

## 工作方式

1. 订阅 `<Z2M_TOPIC_PREFIX>/+`，忽略 `bridge/*`（Z2M 自身状态）与 `*/availability`
2. 从 payload 中挑出 `FORWARD_KEYS` 指定的字段
3. 以 QoS 0 发布到 `v1/gateway/telemetry`：

   ```json
   {"sensor01":{"ts":1794981483295,"values":{"temperature":29.8,"humidity":58.2}}}
   ```

   `ts` 取本机毫秒时间戳；字段缺失则跳过该条
4. 首次见到某设备、或距上次超过 **10 分钟**，额外发布 `v1/gateway/connect`：

   ```json
   {"sensor01":"online"}
   ```

   这样 PandaX 里的子设备状态会从「未激活」变为「在线」

## 排错

| 现象 | 原因与处理 |
|---|---|
| 启动即退出，提示 `PANDAX_GATEWAY_TOKEN is required` | 没设网关 token |
| 日志 `Not authorized` | token 不是该设备在 PandaX 中的 AuthToken，或设备已被删除 |
| 桥接日志有 forwarded，但 PandaX 设备页无数据 | EMQX 未加载 exhook（`emqx ctl conf show exhook` 为空）；或子设备编码与 `DEVICE_MAP` 不一致 |
| PandaX 有数但湿度是负数 | 设备侧 Z2M 配置了 `humidity_calibration`（如 -23），与桥接无关，去 Z2M 里把校准改回 0 |
| 设备一直显示「未激活」 | 桥接未发出 `v1/gateway/connect`（老版本），或设备超过 10 分钟未上报 |
| `MQTT connect failed` | 检查两个 Broker 地址、账号密码、端口是否可达 |

建议把 stdout/stderr 分开重定向，便于排查：

```bash
./zigbee-bridge >bridge.log 2>bridge.err
```

## 与 device-sdk 的区别

- **device-sdk** 是设备**自己**接入 PandaX 的 SDK（直连主题 `v1/devices/me/*`，每个设备一个 MQTT 连接）
- **本项目**是**协议转换器**：设备只认识 Zigbee/Z2M，由网关设备身份统一代发（网关主题 `v1/gateway/*`，一个连接带多个子设备）

## 目录

```
.
├── main.go       # 全部逻辑（约 180 行）
├── go.mod
├── AGENTS.md     # 给 AI/协作者的约定说明
└── README.md
```
