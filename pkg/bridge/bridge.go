package bridge

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const DataActivityTimeout = 5 * time.Second

type Bridge struct {
	id        string
	dataTopic string

	mu       sync.RWMutex
	serial   *SerialPort
	mqtt     *MQTTClient
	parser   *FrameParser
	portName string
	baudRate int

	statusMu      sync.Mutex
	status        string
	activityTimer *time.Timer

	publishQueue     chan []byte
	stop             chan struct{}
	onConnectionLost func(error)
	onMQTTReconnecting func(int)
	onMQTTReconnected  func()
}

func New(bridgeID string) *Bridge {
	return &Bridge{
		id:           bridgeID,
		dataTopic:    fmt.Sprintf("bridges/%s/data", bridgeID),
		parser:       NewFrameParser(),
		status:       "offline",
		publishQueue: make(chan []byte, 100),
		stop:         make(chan struct{}),
	}
}

func (b *Bridge) SetConnectionLostHandler(h func(error)) {
	b.onConnectionLost = h
}

// SetMQTTReconnectingHandler is called on each MQTT reconnect attempt with the attempt count.
func (b *Bridge) SetMQTTReconnectingHandler(h func(int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mqtt != nil {
		b.mqtt.onReconnecting = h
	}
	b.onMQTTReconnecting = h
}

// this is called when MQTT successfully reconnects after a disconnect
func (b *Bridge) SetMQTTReconnectedHandler(h func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onMQTTReconnected = h
}

func (b *Bridge) Connect(portName string, baudRate int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	select {
	case <-b.stop:
		b.stop = make(chan struct{})
		b.publishQueue = make(chan []byte, 100)
	default:
	}

	if baudRate == 0 {
		baudRate = DefaultBaudRate
	}
	b.portName = portName
	b.baudRate = baudRate

	serial, err := OpenSerialPort(portName, baudRate)
	if err != nil {
		return err
	}
	b.serial = serial

	statusTopic := fmt.Sprintf("bridges/%s/status", b.id)
	lwt, _ := json.Marshal(map[string]string{"status": "offline"})

	mqtt := NewMQTTClient(MQTTBroker, b.id, statusTopic, lwt)
	mqtt.onConnectionLost = b.onConnectionLost
	mqtt.onReconnecting = b.onMQTTReconnecting
	mqtt.onReconnect = func() {
		// make sure we resubscribe to settings in case the broker dropped the session
		mqtt.Subscribe(fmt.Sprintf("bridges/%s/settings", b.id), func(data []byte) {
			var msg struct {
				ConnectedBaud int `json:"connected_baud"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.ConnectedBaud > 0 && msg.ConnectedBaud != b.baudRate {
				b.reconnectSerial(msg.ConnectedBaud)
			}
		})

		// republish state so broker retained messages are correct
		b.statusMu.Lock()
		currentStatus := b.status
		b.statusMu.Unlock()

		topic := fmt.Sprintf("bridges/%s/status", b.id)
		payload, _ := json.Marshal(map[string]string{"status": currentStatus})
		mqtt.PublishRetained(topic, payload)

		version, _ := json.Marshal(map[string]string{"version": GetVersion()})
		mqtt.Publish(topic, version)

		appType, _ := json.Marshal(map[string]string{"type": GetAppType()})
		mqtt.Publish(topic, appType)

		fmt.Printf("Bridge status republished as %q after MQTT reconnect\n", currentStatus)

		if b.onMQTTReconnected != nil {
			b.onMQTTReconnected()
		}
	}

	if err := mqtt.Connect(); err != nil {
		b.serial.Close()
		b.serial = nil
		return err
	}
	b.mqtt = mqtt

	mqtt.Subscribe(fmt.Sprintf("bridges/%s/settings", b.id), func(data []byte) {
		var msg struct {
			ConnectedBaud int `json:"connected_baud"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.ConnectedBaud > 0 && msg.ConnectedBaud != b.baudRate {
			b.reconnectSerial(msg.ConnectedBaud)
		}
	})

	online, _ := json.Marshal(map[string]string{"status": "online"})
	mqtt.PublishRetained(statusTopic, online)

	version, _ := json.Marshal(map[string]string{"version": GetVersion()})
	mqtt.Publish(statusTopic, version)

	appType, _ := json.Marshal(map[string]string{"type": GetAppType()})
	mqtt.Publish(statusTopic, appType)

	b.statusMu.Lock()
	b.status = "online"
	b.statusMu.Unlock()

	return nil
}

func (b *Bridge) Start(onData func([]byte)) error {
	b.resetActivityTimer()

	go b.publishLoop()
	go b.flushLoop()

	buf := make([]byte, 1024)
	errCount := 0

	for {
		select {
		case <-b.stop:
			return nil
		default:
		}

		b.mu.RLock()
		serial := b.serial
		b.mu.RUnlock()

		if serial == nil {
			return fmt.Errorf("disconnected")
		}

		n, err := serial.Read(buf)
		if err != nil {
			errCount++
			if errCount > 50 {
				return fmt.Errorf("serial read failed: %w", err)
			}
			time.Sleep(time.Duration(min(errCount*10, 500)) * time.Millisecond)
			continue
		}
		errCount = 0

		if n == 0 {
			continue
		}

		if onData != nil {
			onData(buf[:n])
		}

		b.onDataReceived()

		for _, frame := range b.parser.Parse(buf[:n]) {
			select {
			case b.publishQueue <- frame:
			case <-b.stop:
				return nil
			default:
			}
		}
	}
}

func (b *Bridge) Disconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.activityTimer != nil {
		b.activityTimer.Stop()
		b.activityTimer = nil
	}

	select {
	case <-b.stop:
	default:
		close(b.stop)
	}

	if b.mqtt != nil {
		if b.mqtt.IsConnected() {
			statusTopic := fmt.Sprintf("bridges/%s/status", b.id)
			offline, _ := json.Marshal(map[string]string{"status": "offline"})
			b.mqtt.PublishRetained(statusTopic, offline)
		}
		b.mqtt.Disconnect()
	}

	if b.serial != nil {
		b.serial.Close()
		b.serial = nil
	}

	b.statusMu.Lock()
	b.status = "offline"
	b.statusMu.Unlock()

	return nil
}

func (b *Bridge) IsHealthy() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.serial == nil || b.mqtt == nil {
		return false
	}
	return b.mqtt.IsConnected() || b.mqtt.IsReconnecting()
}

func (b *Bridge) IsMQTTReconnecting() bool {
	b.mu.RLock()
	mqtt := b.mqtt
	b.mu.RUnlock()

	if mqtt == nil {
		return false
	}
	return mqtt.IsReconnecting()
}

func (b *Bridge) GetMQTTReconnectionInfo() (time.Duration, int) {
	b.mu.RLock()
	mqtt := b.mqtt
	b.mu.RUnlock()

	if mqtt == nil {
		return 0, 0
	}
	return mqtt.ReconnectInfo()
}

func (b *Bridge) publishLoop() {
	for {
		select {
		case <-b.stop:
			return
		case frame := <-b.publishQueue:
			b.mu.RLock()
			mqtt := b.mqtt
			topic := b.dataTopic
			b.mu.RUnlock()

			if mqtt != nil && mqtt.IsConnected() && len(frame) > 0 {
				mqtt.Publish(topic, frame)
			}
		}
	}
}

func (b *Bridge) flushLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			if frame := b.parser.Flush(); frame != nil {
				select {
				case b.publishQueue <- frame:
				default:
				}
			}
		}
	}
}

func (b *Bridge) reconnectSerial(baud int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.serial == nil {
		return nil
	}

	if b.activityTimer != nil {
		b.activityTimer.Stop()
	}
	b.serial.Close()

	serial, err := OpenSerialPort(b.portName, baud)
	if err != nil {
		return err
	}

	b.serial = serial
	b.baudRate = baud
	return nil
}

func (b *Bridge) updateStatus(status string, retained bool) {
	b.statusMu.Lock()
	if b.status == status {
		b.statusMu.Unlock()
		return
	}
	b.status = status
	b.statusMu.Unlock()

	b.mu.RLock()
	mqtt := b.mqtt
	id := b.id
	b.mu.RUnlock()

	if mqtt == nil || !mqtt.IsConnected() {
		return
	}

	topic := fmt.Sprintf("bridges/%s/status", id)
	payload, _ := json.Marshal(map[string]string{"status": status})

	if retained {
		mqtt.PublishRetained(topic, payload)
	} else {
		mqtt.Publish(topic, payload)
	}
}

func (b *Bridge) onDataReceived() {
	b.statusMu.Lock()
	wasWaiting := b.status == "waiting"
	b.statusMu.Unlock()

	if wasWaiting {
		b.updateStatus("online", true)
	}
	b.resetActivityTimer()
}

func (b *Bridge) resetActivityTimer() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.activityTimer != nil {
		b.activityTimer.Stop()
	}

	b.activityTimer = time.AfterFunc(DataActivityTimeout, func() {
		b.statusMu.Lock()
		isOnline := b.status == "online"
		b.statusMu.Unlock()

		if isOnline {
			b.updateStatus("waiting", false)
		}
	})
}
