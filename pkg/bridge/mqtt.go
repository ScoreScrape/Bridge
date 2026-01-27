package bridge

import (
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// TODO: Probably not set this statically
// in the future this URL may change or there will be different regions
const MQTTBroker = "mqtts://broker.scorescrape.io:8883"

// Also certificate pinning????

type MQTTClient struct {
	client           mqtt.Client
	onConnectionLost func(error)

	mu                sync.RWMutex
	disconnectTime    time.Time
	reconnectAttempts int
	reconnecting      bool
}

func NewMQTTClient(broker, clientID, lwtTopic string, lwtPayload []byte) *MQTTClient {
	m := &MQTTClient{}

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(time.Minute).
		SetConnectRetryInterval(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second).
		SetConnectTimeout(30 * time.Second).
		SetWriteTimeout(10 * time.Second).
		SetMessageChannelDepth(1000).
		SetCleanSession(false).
		SetResumeSubs(true)

	if lwtTopic != "" && lwtPayload != nil {
		opts.SetWill(lwtTopic, string(lwtPayload), 0, true)
	}

	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		m.mu.Lock()
		m.disconnectTime = time.Now()
		m.reconnecting = true
		m.reconnectAttempts = 0
		m.mu.Unlock()

		if m.onConnectionLost != nil {
			m.onConnectionLost(err)
		}
	})

	opts.SetReconnectingHandler(func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		m.mu.Lock()
		m.reconnectAttempts++
		attempts := m.reconnectAttempts
		m.mu.Unlock()

		if attempts%10 == 1 {
			fmt.Printf("MQTT reconnect attempt %d...\n", attempts)
		}
	})

	opts.SetOnConnectHandler(func(_ mqtt.Client) {
		m.mu.Lock()
		wasReconnecting := m.reconnecting
		attempts := m.reconnectAttempts
		m.reconnecting = false
		m.reconnectAttempts = 0
		m.mu.Unlock()

		if wasReconnecting && attempts > 0 {
			fmt.Printf("MQTT reconnected after %d attempts\n", attempts)
		}
	})

	m.client = mqtt.NewClient(opts)
	return m
}

func (m *MQTTClient) Connect() error {
	token := m.client.Connect()
	token.Wait()
	return token.Error()
}

func (m *MQTTClient) Subscribe(topic string, handler func([]byte)) error {
	token := m.client.Subscribe(topic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		handler(msg.Payload())
	})
	token.Wait()
	return token.Error()
}

func (m *MQTTClient) Publish(topic string, data []byte) {
	m.client.Publish(topic, 0, false, data)
}

func (m *MQTTClient) PublishRetained(topic string, data []byte) error {
	return m.client.Publish(topic, 0, true, data).Error()
}

func (m *MQTTClient) IsConnected() bool {
	return m.client != nil && m.client.IsConnected()
}

func (m *MQTTClient) Disconnect() {
	m.client.Disconnect(250)
}

func (m *MQTTClient) IsReconnecting() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reconnecting
}

func (m *MQTTClient) ReconnectInfo() (duration time.Duration, attempts int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.disconnectTime.IsZero() {
		duration = time.Since(m.disconnectTime)
	}
	return duration, m.reconnectAttempts
}
