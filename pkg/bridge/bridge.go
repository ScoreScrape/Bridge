package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.bug.st/serial"
)

const (
	STX                byte = 0x02
	ETX                byte = 0x03
	MaxFrameBufferSize      = 8192
	DefaultBaudRate         = 9600
	MQTTBroker              = "mqtts://broker.scorescrape.io:8883"
)

// Message structures for MQTT communication
type SettingsMessage struct {
	ConnectedBaud int `json:"connected_baud"`
}

type StatusUpdate struct {
	Status string `json:"status"`
}

// SerialPort handles the serial interface
type SerialPort struct {
	port   serial.Port
	reader *bufio.Reader
}

func OpenSerialPort(portName string, baudRate int) (*SerialPort, error) {
	mode := &serial.Mode{BaudRate: baudRate}
	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to open serial port: %w", err)
	}
	return &SerialPort{port: port, reader: bufio.NewReader(port)}, nil
}

func (s *SerialPort) Read(buffer []byte) (int, error) { return s.reader.Read(buffer) }
func (s *SerialPort) Close() error                    { return s.port.Close() }

// GetAvailablePorts returns a list of usable serial ports
func GetAvailablePorts() ([]string, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("failed to get ports list: %w", err)
	}
	var usable []string
	for _, p := range ports {
		if !isUnusablePort(p) {
			usable = append(usable, p)
		}
	}
	return usable, nil
}

func isUnusablePort(name string) bool {
	low := strings.ToLower(name)
	if strings.Contains(low, "bluetooth-incoming-port") || strings.Contains(low, "modem") {
		return true
	}
	return strings.Contains(low, "bluetooth") && !strings.Contains(low, "usb")
}

// MQTTClient handles broker communication
type MQTTClient struct {
	client           mqtt.Client
	onConnectionLost func(error)
}

func NewMQTTClient(broker, clientID, lwtTopic string, lwtPayload []byte) *MQTTClient {
	opts := mqtt.NewClientOptions().AddBroker(broker).SetClientID(clientID).SetAutoReconnect(false)
	if lwtTopic != "" && lwtPayload != nil {
		opts.SetWill(lwtTopic, string(lwtPayload), 0, true)
	}
	m := &MQTTClient{}
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		if m.onConnectionLost != nil {
			m.onConnectionLost(err)
		}
	})
	m.client = mqtt.NewClient(opts)
	return m
}

func (m *MQTTClient) Connect() error {
	if token := m.client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func (m *MQTTClient) Subscribe(topic string, h func([]byte)) error {
	if token := m.client.Subscribe(topic, 0, func(c mqtt.Client, msg mqtt.Message) {
		h(msg.Payload())
	}); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func (m *MQTTClient) Publish(topic string, data []byte) error {
	return m.client.Publish(topic, 0, false, data).Error()
}

func (m *MQTTClient) PublishRetained(topic string, data []byte) error {
	return m.client.Publish(topic, 0, true, data).Error()
}

func (m *MQTTClient) IsConnected() bool { return m.client != nil && m.client.IsConnected() }
func (m *MQTTClient) Disconnect()       { m.client.Disconnect(250) }

// Bridge is the main coordinator
type Bridge struct {
	mu               sync.RWMutex
	serialPort       *SerialPort
	mqttClient       *MQTTClient
	bridgeID         string
	dataTopic        string
	onConnectionLost func(error)
	onBaudRateChange func(int)
	frameBuffer      []byte
	inFrame          bool
	lastDataTime     int64
	currentBaudRate  int
	currentPortName  string
}

func New(bridgeID string) *Bridge {
	return &Bridge{
		bridgeID:     bridgeID,
		dataTopic:    fmt.Sprintf("bridges/%s/data", bridgeID),
		frameBuffer:  make([]byte, 0, 1024),
		lastDataTime: time.Now().UnixNano(),
	}
}

func (b *Bridge) SetConnectionLostHandler(h func(error)) { b.onConnectionLost = h }

func (b *Bridge) Connect(portName string, baudRate int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if baudRate == 0 {
		baudRate = DefaultBaudRate
	}

	b.currentBaudRate = baudRate
	b.currentPortName = portName

	statusTopic := fmt.Sprintf("bridges/%s/status", b.bridgeID)
	lwtPayload, _ := json.Marshal(StatusUpdate{Status: "offline"})

	port, err := OpenSerialPort(portName, baudRate)
	if err != nil {
		return err
	}
	b.serialPort = port

	m := NewMQTTClient(MQTTBroker, b.bridgeID, statusTopic, lwtPayload)
	m.onConnectionLost = b.onConnectionLost
	if err := m.Connect(); err != nil {
		b.serialPort.Close()
		return err
	}
	b.mqttClient = m

	m.Subscribe(fmt.Sprintf("bridges/%s/settings", b.bridgeID), func(data []byte) {
		var msg SettingsMessage
		if err := json.Unmarshal(data, &msg); err == nil && msg.ConnectedBaud > 0 && msg.ConnectedBaud != b.currentBaudRate {
			b.reconnectWithBaudRate(msg.ConnectedBaud)
		}
	})

	onlinePayload, _ := json.Marshal(StatusUpdate{Status: "online"})
	m.PublishRetained(statusTopic, onlinePayload)

	return nil
}

func (b *Bridge) reconnectWithBaudRate(baud int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.serialPort == nil {
		return nil
	}

	b.serialPort.Close()
	port, err := OpenSerialPort(b.currentPortName, baud)
	if err != nil {
		return err
	}
	b.serialPort = port
	b.currentBaudRate = baud
	return nil
}

func (b *Bridge) Start(onData func([]byte), onLog func(string)) error {
	buf := make([]byte, 1024)
	for {
		b.mu.RLock()
		port, mqtt, topic := b.serialPort, b.mqttClient, b.dataTopic
		b.mu.RUnlock()

		if port == nil || mqtt == nil {
			return fmt.Errorf("bridge disconnected")
		}

		n, err := port.Read(buf)
		if err != nil {
			return err
		}

		if n > 0 {
			data := buf[:n]
			b.mu.Lock()
			b.lastDataTime = time.Now().UnixNano()
			b.mu.Unlock()

			if onData != nil {
				onData(data)
			}
			for _, frame := range b.processSerialData(data) {
				if len(frame) > 0 {
					mqtt.Publish(topic, frame)
				}
			}
		}

		// Flush buffer on timeout (100ms)
		b.mu.Lock()
		if len(b.frameBuffer) > 0 && (time.Now().UnixNano()-b.lastDataTime) > 100*int64(time.Millisecond) {
			mqtt.Publish(topic, b.frameBuffer)
			b.frameBuffer = b.frameBuffer[:0]
			b.inFrame = false
		}
		b.mu.Unlock()
	}
}

func (b *Bridge) processSerialData(data []byte) [][]byte {
	var frames [][]byte
	for _, db := range data {
		switch db {
		case STX:
			if b.inFrame && len(b.frameBuffer) > 0 {
				frames = append(frames, append([]byte(nil), b.frameBuffer...))
				b.frameBuffer = b.frameBuffer[:0]
			}
			b.frameBuffer = append(b.frameBuffer, db)
			b.inFrame = true
		case ETX:
			b.frameBuffer = append(b.frameBuffer, db)
			frames = append(frames, append([]byte(nil), b.frameBuffer...))
			b.frameBuffer = b.frameBuffer[:0]
			b.inFrame = false
		default:
			if len(b.frameBuffer) >= MaxFrameBufferSize {
				frames = append(frames, append([]byte(nil), b.frameBuffer...))
				b.frameBuffer = b.frameBuffer[:0]
				b.inFrame = false
			}
			b.frameBuffer = append(b.frameBuffer, db)
		}
	}
	return frames
}

func (b *Bridge) Disconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mqttClient != nil && b.mqttClient.IsConnected() {
		statusTopic := fmt.Sprintf("bridges/%s/status", b.bridgeID)
		offlinePayload, _ := json.Marshal(StatusUpdate{Status: "offline"})
		b.mqttClient.PublishRetained(statusTopic, offlinePayload)
		b.mqttClient.Disconnect()
	}
	if b.serialPort != nil {
		b.serialPort.Close()
	}
	return nil
}

func (b *Bridge) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.serialPort != nil && b.mqttClient != nil && b.mqttClient.IsConnected()
}
