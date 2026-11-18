package main

import (
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type config struct {
	z2mURL       string
	z2mUser      string
	z2mPass      string
	z2mPrefix    string
	pandaxURL    string
	gatewayToken string
	keys         map[string]bool
	deviceMap    map[string]string
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags)

	cfg := config{
		z2mURL:       env("Z2M_URL", "tcp://127.0.0.1:1883"),
		z2mUser:      env("Z2M_USERNAME", "homeassistant"),
		z2mPass:      os.Getenv("Z2M_PASSWORD"),
		z2mPrefix:    env("Z2M_TOPIC_PREFIX", "zigbee2mqtt"),
		pandaxURL:    env("PANDAX_URL", "tcp://127.0.0.1:1883"),
		gatewayToken: os.Getenv("PANDAX_GATEWAY_TOKEN"),
		keys:         map[string]bool{},
		deviceMap:    map[string]string{},
	}
	if cfg.gatewayToken == "" {
		log.Fatal("PANDAX_GATEWAY_TOKEN is required")
	}
	for _, k := range strings.Split(env("FORWARD_KEYS", "temperature,humidity"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			cfg.keys[k] = true
		}
	}
	for _, pair := range strings.Split(os.Getenv("DEVICE_MAP"), ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			cfg.deviceMap[parts[0]] = parts[1]
		}
	}

	pubOpts := mqtt.NewClientOptions().AddBroker(cfg.pandaxURL)
	pubOpts.SetUsername(cfg.gatewayToken)
	pubOpts.SetClientID("pandax-z2m-bridge-pub")
	pubOpts.SetAutoReconnect(true)
	pubOpts.SetConnectRetry(true)
	pubOpts.SetOnConnectHandler(func(mqtt.Client) { log.Println("pandax mqtt connected") })
	publisher := mqtt.NewClient(pubOpts)
	if t := publisher.Connect(); t.Wait() && t.Error() != nil {
		log.Fatalf("pandax mqtt connect failed: %v", t.Error())
	}

	topic := cfg.z2mPrefix + "/+"
	subOpts := mqtt.NewClientOptions().AddBroker(cfg.z2mURL)
	subOpts.SetUsername(cfg.z2mUser)
	subOpts.SetPassword(cfg.z2mPass)
	subOpts.SetClientID("pandax-z2m-bridge-sub")
	subOpts.SetAutoReconnect(true)
	subOpts.SetConnectRetry(true)
	subOpts.SetCleanSession(true)
	subOpts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Printf("z2m mqtt connected, subscribing %s", topic)
		if t := c.Subscribe(topic, 0, func(_ mqtt.Client, m mqtt.Message) {
			handle(cfg, publisher, m)
		}); t.Wait() && t.Error() != nil {
			log.Printf("subscribe failed: %v", t.Error())
		}
	})
	subscriber := mqtt.NewClient(subOpts)
	if t := subscriber.Connect(); t.Wait() && t.Error() != nil {
		log.Fatalf("z2m mqtt connect failed: %v", t.Error())
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	subscriber.Disconnect(500)
	publisher.Disconnect(500)
}

var (
	seenMu sync.Mutex
	seenAt = map[string]time.Time{}
)

const onlineRefresh = 10 * time.Minute

func handle(cfg config, pub mqtt.Client, m mqtt.Message) {
	topic := m.Topic()
	if strings.HasPrefix(topic, cfg.z2mPrefix+"/bridge/") || strings.HasSuffix(topic, "/availability") {
		return
	}
	parts := strings.Split(topic, "/")
	name := parts[len(parts)-1]
	code, ok := cfg.deviceMap[name]
	if !ok {
		if len(cfg.deviceMap) > 0 {
			return
		}
		code = name
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(m.Payload(), &raw); err != nil {
		return
	}
	values := map[string]any{}
	for k := range cfg.keys {
		v, ok := raw[k]
		if !ok {
			continue
		}
		var val any
		if err := json.Unmarshal(v, &val); err == nil {
			values[k] = val
		}
	}
	if len(values) == 0 {
		return
	}

	payload, _ := json.Marshal(map[string]any{
		code: map[string]any{
			"ts":     time.Now().UnixMilli(),
			"values": values,
		},
	})
	t := pub.Publish("v1/gateway/telemetry", 0, false, payload)
	if t.WaitTimeout(5*time.Second) && t.Error() != nil {
		log.Printf("publish failed: %v", t.Error())
		return
	}
	log.Printf("forwarded %s -> %s %s", topic, code, string(payload))
	markOnline(pub, code)
}

// markOnline 棣栨涓婃姤鎴栬秴杩囧埛鏂伴棿闅旀椂锛屽悜缃戝叧涓婚鍙戝竷鍦ㄧ嚎鐘舵€?
func markOnline(pub mqtt.Client, code string) {
	seenMu.Lock()
	last, ok := seenAt[code]
	if ok && time.Since(last) < onlineRefresh {
		seenMu.Unlock()
		return
	}
	seenAt[code] = time.Now()
	seenMu.Unlock()

	payload, _ := json.Marshal(map[string]string{code: "online"})
	t := pub.Publish("v1/gateway/connect", 0, false, payload)
	if t.WaitTimeout(5*time.Second) && t.Error() != nil {
		log.Printf("publish online failed: %v", t.Error())
		return
	}
	log.Printf("marked online: %s", code)
}
