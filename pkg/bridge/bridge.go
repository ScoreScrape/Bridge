package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.bug.st/serial"
)

const (
	STX                 byte = 0x02
	ETX                 byte = 0x03
	MaxFrameBufferSize       = 8192
	DefaultBaudRate          = 9600
	MQTTBroker               = "mqtts://broker.scorescrape.io:8883"
	DataActivityTimeout      = 5 * time.Second
	SerialReadTimeout        = 100 * time.Millisecond
)

// Version can be set at build time via ldflags:
// go build -ldflags="-X bridge/pkg/bridge.Version=1.0.0"
var Version = ""

// Message structures for MQTT communication
type SettingsMessage struct {
	ConnectedBaud int `json:"connected_baud"`
}

type StatusUpdate struct {
	Status string `json:"status"`
}

type VersionMessage struct {
	Version string `json:"version"`
}

type TypeMessage struct {
	Type string `json:"type"`
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
	// Set read timeout to prevent blocking forever on Windows
	// This is critical - without a timeout, Read() can hang indefinitely on Windows
	if err := port.SetReadTimeout(SerialReadTimeout); err != nil {
		port.Close()
		return nil, fmt.Errorf("failed to set read timeout: %w", err)
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

// getAppVersion returns the application version
func getAppVersion() string {
	// First check if version was set at build time
	if Version != "" {
		return Version
	}

	// Fall back to reading from VERSION file (for local development)
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)

	possiblePaths := []string{
		"VERSION",
		filepath.Join(execDir, "VERSION"),
		filepath.Join(execDir, "..", "VERSION"),
		filepath.Join(execDir, "..", "..", "VERSION"),
		filepath.Join(execDir, "..", "..", "..", "VERSION"),
		"../VERSION",
		"../../VERSION",
		"../../../VERSION",
		"./VERSION",
	}

	for _, path := range possiblePaths {
		if data, err := ioutil.ReadFile(path); err == nil {
			version := strings.TrimSpace(string(data))
			if version != "" {
				return version
			}
		}
	}

	return "unknown"
}

// getAppType determines if this is a Docker, Windows, or macOS deployment
func getAppType() string {
	// Check if running in Docker environment (CLI only, no GUI)
	if os.Getenv("BRIDGE_GUI") == "false" {
		return "docker"
	}

	// Detect macOS by checking for .app bundle structure or darwin OS
	if strings.Contains(os.Args[0], ".app/Contents/MacOS/") {
		return "macos"
	}

	// Check runtime OS as fallback
	if runtime.GOOS == "darwin" {
		return "macos"
	}

	// Default to windows for GUI mode on other platforms
	return "windows"
}

// MQTTClient handles broker communication
type MQTTClient struct {
	client               mqtt.Client
	onConnectionLost     func(error)
	mu                   sync.RWMutex
	lastDisconnectTime   time.Time
	reconnectionAttempts int
	isReconnecting       bool
}

func NewMQTTClient(broker, clientID, lwtTopic string, lwtPayload []byte) *MQTTClient {
	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(1 * time.Minute).  // Longer max interval
		SetConnectRetryInterval(5 * time.Second).  // Initial retry interval
		SetKeepAlive(30 * time.Second).            // More frequent keepalive to detect issues faster
		SetPingTimeout(10 * time.Second).          // Ping timeout
		SetConnectTimeout(30 * time.Second).       // Connect timeout
		SetWriteTimeout(10 * time.Second).         // Write timeout
		SetMessageChannelDepth(1000).              // Message buffer
		SetCleanSession(false).                    // Persist session to survive reconnects
		SetResumeSubs(true)                        // Auto-resubscribe on reconnect

	if lwtTopic != "" && lwtPayload != nil {
		opts.SetWill(lwtTopic, string(lwtPayload), 0, true)
	}

	m := &MQTTClient{}
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		m.mu.Lock()
		m.lastDisconnectTime = time.Now()
		m.isReconnecting = true
		m.reconnectionAttempts = 0
		m.mu.Unlock()

		if m.onConnectionLost != nil {
			m.onConnectionLost(err)
		}
	})

	// Track reconnection attempts with logging
	opts.SetReconnectingHandler(func(c mqtt.Client, options *mqtt.ClientOptions) {
		m.mu.Lock()
		m.reconnectionAttempts++
		attempts := m.reconnectionAttempts
		m.isReconnecting = true
		m.mu.Unlock()

		// Log every 10 attempts to avoid spam
		if attempts%10 == 1 {
			fmt.Printf("MQTT reconnection attempt %d...\n", attempts)
		}
	})

	// Track successful reconnection with logging
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		m.mu.Lock()
		wasReconnecting := m.isReconnecting
		attempts := m.reconnectionAttempts
		m.isReconnecting = false
		m.reconnectionAttempts = 0
		m.mu.Unlock()

		if wasReconnecting && attempts > 0 {
			fmt.Printf("MQTT reconnected successfully after %d attempts\n", attempts)
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
	// Don't wait for completion to avoid blocking - fire and forget for better performance
	// The MQTT client will handle retries internally
	_ = m.client.Publish(topic, 0, false, data)
	return nil
}

func (m *MQTTClient) PublishSync(topic string, data []byte) error {
	return m.client.Publish(topic, 0, false, data).Error()
}

func (m *MQTTClient) PublishRetained(topic string, data []byte) error {
	return m.client.Publish(topic, 0, true, data).Error()
}

func (m *MQTTClient) IsConnected() bool { return m.client != nil && m.client.IsConnected() }
func (m *MQTTClient) Disconnect()       { m.client.Disconnect(250) }

// IsReconnecting returns true if MQTT is actively attempting to reconnect
func (m *MQTTClient) IsReconnecting() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isReconnecting
}

// GetReconnectionInfo returns disconnection time and attempt count
func (m *MQTTClient) GetReconnectionInfo() (time.Time, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastDisconnectTime, m.reconnectionAttempts
}

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
	stopChan         chan struct{}
	publishQueue     chan []byte

	// Data activity monitoring fields
	dataActivityTimer    *time.Timer
	currentStatus        string
	statusMutex          sync.Mutex // Changed from RWMutex for simplicity
	dataTimeoutDuration  time.Duration
	dataActivityStopChan chan struct{}
}

func New(bridgeID string) *Bridge {
	return &Bridge{
		bridgeID:             bridgeID,
		dataTopic:            fmt.Sprintf("bridges/%s/data", bridgeID),
		frameBuffer:          make([]byte, 0, 1024),
		lastDataTime:         time.Now().UnixNano(),
		stopChan:             make(chan struct{}),
		publishQueue:         make(chan []byte, 100), // Buffer up to 100 frames
		currentStatus:        "offline",
		dataTimeoutDuration:  DataActivityTimeout,
		dataActivityStopChan: make(chan struct{}),
	}
}

func (b *Bridge) SetConnectionLostHandler(h func(error)) { b.onConnectionLost = h }

// IsHealthy checks if the bridge is in a healthy state
// A bridge is healthy if both serial and MQTT connections are active
func (b *Bridge) IsHealthy() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Check if we have both serial and MQTT connections
	if b.serialPort == nil {
		return false
	}

	if b.mqttClient == nil {
		return false
	}

	// MQTT connected or actively reconnecting counts as healthy
	if !b.mqttClient.IsConnected() && !b.mqttClient.IsReconnecting() {
		return false
	}

	return true
}

// IsMQTTReconnecting returns true if MQTT is actively attempting to auto-reconnect
// This helps distinguish between "temporarily disconnected but recovering" vs "broken"
func (b *Bridge) IsMQTTReconnecting() bool {
	b.mu.RLock()
	mqttClient := b.mqttClient
	b.mu.RUnlock()

	if mqttClient == nil {
		return false
	}

	return mqttClient.IsReconnecting()
}

// GetMQTTReconnectionInfo returns how long MQTT has been disconnected and attempt count
func (b *Bridge) GetMQTTReconnectionInfo() (disconnectedDuration time.Duration, attempts int) {
	b.mu.RLock()
	mqttClient := b.mqttClient
	b.mu.RUnlock()

	if mqttClient == nil {
		return 0, 0
	}

	disconnectTime, attempts := mqttClient.GetReconnectionInfo()
	if !disconnectTime.IsZero() {
		disconnectedDuration = time.Since(disconnectTime)
	}
	return disconnectedDuration, attempts
}

// updateStatus updates the bridge status with proper retention control
// LOCK ORDER: Always acquire mu.RLock BEFORE statusMutex to prevent deadlock with Disconnect()
func (b *Bridge) updateStatus(status string, retained bool) error {
	// First, get MQTT client reference with mu lock
	b.mu.RLock()
	mqtt := b.mqttClient
	bridgeID := b.bridgeID
	b.mu.RUnlock()

	// Then update status with statusMutex
	b.statusMutex.Lock()
	// Don't update if status hasn't changed
	if b.currentStatus == status {
		b.statusMutex.Unlock()
		return nil
	}
	b.currentStatus = status
	b.statusMutex.Unlock()

	// Publish status update to MQTT (outside of lock)
	if mqtt != nil && mqtt.IsConnected() {
		statusTopic := fmt.Sprintf("bridges/%s/status", bridgeID)
		statusPayload, _ := json.Marshal(StatusUpdate{Status: status})

		if retained {
			return mqtt.PublishRetained(statusTopic, statusPayload)
		} else {
			return mqtt.PublishSync(statusTopic, statusPayload)
		}
	}

	return nil
}

// getCurrentStatus returns the current bridge status thread-safely
func (b *Bridge) getCurrentStatus() string {
	b.statusMutex.Lock()
	defer b.statusMutex.Unlock()
	return b.currentStatus
}

// resetDataActivityTimer resets the data activity timer
func (b *Bridge) resetDataActivityTimer() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Stop existing timer if it exists
	if b.dataActivityTimer != nil {
		if !b.dataActivityTimer.Stop() {
			// Timer already fired, drain the channel if needed
			select {
			case <-b.dataActivityTimer.C:
			default:
			}
		}
	}

	// Create new timer that will trigger status change to "waiting"
	b.dataActivityTimer = time.AfterFunc(b.dataTimeoutDuration, func() {
		// Recover from any panics in timer callback
		defer func() {
			if r := recover(); r != nil {
				// Log error but continue operation
				fmt.Printf("Data activity timer panic recovered: %v\n", r)
			}
		}()

		// Only transition to waiting if currently online
		if b.getCurrentStatus() == "online" {
			if err := b.updateStatus("waiting", false); err != nil {
				// Log error but continue operation
				fmt.Printf("Failed to update status to waiting: %v\n", err)
			}
		}
	})
}

// stopDataActivityTimer stops the data activity timer
// MUST be called with b.mu already locked
func (b *Bridge) stopDataActivityTimerLocked() {
	if b.dataActivityTimer != nil {
		b.dataActivityTimer.Stop()
		b.dataActivityTimer = nil
	}
}

// onDataReceived handles data reception for activity monitoring
func (b *Bridge) onDataReceived() {
	// Recover from any panics
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Data reception handler panic recovered: %v\n", r)
		}
	}()

	// If we were waiting, transition back to online
	if b.getCurrentStatus() == "waiting" {
		if err := b.updateStatus("online", true); err != nil {
			// Log error but continue operation
			fmt.Printf("Failed to update status to online: %v\n", err)
		}
	}

	// Reset the activity timer
	b.resetDataActivityTimer()
}

func (b *Bridge) Connect(portName string, baudRate int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Recreate channels if they were closed (e.g., after Disconnect)
	select {
	case <-b.stopChan:
		// Channel was closed, recreate it
		b.stopChan = make(chan struct{})
		b.publishQueue = make(chan []byte, 100)
		b.dataActivityStopChan = make(chan struct{})
	default:
		// Channel is still open, check if publishQueue needs recreation
		if b.publishQueue == nil {
			b.publishQueue = make(chan []byte, 100)
		}
		if b.dataActivityStopChan == nil {
			b.dataActivityStopChan = make(chan struct{})
		}
	}

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
		b.serialPort = nil
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

	// Send version and type information on first connection
	b.sendStartupInfo(m)

	// Initialize status for data activity monitoring (update without lock since we hold mu)
	b.statusMutex.Lock()
	b.currentStatus = "online"
	b.statusMutex.Unlock()

	return nil
}

// sendStartupInfo sends version and type information when first connecting to MQTT
func (b *Bridge) sendStartupInfo(mqttClient *MQTTClient) {
	statusTopic := fmt.Sprintf("bridges/%s/status", b.bridgeID)

	// Send version information
	version := getAppVersion()
	versionPayload, _ := json.Marshal(VersionMessage{Version: version})
	mqttClient.Publish(statusTopic, versionPayload)

	// Send type information
	appType := getAppType()
	typePayload, _ := json.Marshal(TypeMessage{Type: appType})
	mqttClient.Publish(statusTopic, typePayload)
}

func (b *Bridge) reconnectWithBaudRate(baud int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.serialPort == nil {
		return nil
	}

	// Stop data activity timer during reconnection
	b.stopDataActivityTimerLocked()

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
	// Initialize data activity monitoring
	b.resetDataActivityTimer()

	// Start async publisher goroutine
	go b.publishWorker()

	// Start timeout checker goroutine
	timeoutDuration := 100 * time.Millisecond
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-b.stopChan:
				return
			case <-ticker.C:
				b.mu.Lock()
				now := time.Now().UnixNano()
				mqtt := b.mqttClient
				if len(b.frameBuffer) > 0 && (now-b.lastDataTime) > int64(timeoutDuration) {
					// Make a copy of the buffer before publishing
					bufCopy := make([]byte, len(b.frameBuffer))
					copy(bufCopy, b.frameBuffer)
					b.frameBuffer = b.frameBuffer[:0]
					b.inFrame = false
					b.mu.Unlock()

					// Queue the flushed buffer
					if mqtt != nil {
						select {
						case b.publishQueue <- bufCopy:
						default:
							// Queue full, frame dropped
						}
					}
				} else {
					b.mu.Unlock()
				}
			}
		}
	}()

	buf := make([]byte, 1024)
	consecutiveErrors := 0
	maxConsecutiveErrors := 50 // Allow some errors before giving up

	for {
		select {
		case <-b.stopChan:
			return nil
		default:
		}

		b.mu.RLock()
		port, mqtt := b.serialPort, b.mqttClient
		b.mu.RUnlock()

		if port == nil || mqtt == nil {
			return fmt.Errorf("bridge disconnected")
		}

		n, err := port.Read(buf)
		if err != nil {
			consecutiveErrors++

			// Check if it's a timeout (expected on Windows with read timeout set)
			// Timeouts return 0 bytes read, which we handle below
			select {
			case <-b.stopChan:
				return nil
			default:
				// If we have too many consecutive errors, something is seriously wrong
				if consecutiveErrors > maxConsecutiveErrors {
					return fmt.Errorf("too many consecutive read errors (%d), last error: %w", consecutiveErrors, err)
				}

				// Progressive delay based on error count
				var delay time.Duration
				if consecutiveErrors < 10 {
					delay = 10 * time.Millisecond
				} else if consecutiveErrors < 25 {
					delay = 100 * time.Millisecond
				} else {
					delay = 500 * time.Millisecond
				}

				time.Sleep(delay)
				continue
			}
		}

		// Reset error count on successful read (even if n=0 for timeout)
		if err == nil {
			consecutiveErrors = 0
		}

		if n > 0 {
			// Make a copy of the data since buf is reused
			data := make([]byte, n)
			copy(data, buf[:n])

			now := time.Now().UnixNano()
			b.mu.Lock()
			b.lastDataTime = now
			b.mu.Unlock()

			// Trigger data activity monitoring
			b.onDataReceived()

			if onData != nil {
				onData(data)
			}

			// Process frames with proper locking
			frames := b.processSerialData(data)
			for _, frame := range frames {
				if len(frame) > 0 {
					select {
					case b.publishQueue <- frame:
						// Frame queued successfully
					case <-b.stopChan:
						return nil
					default:
						// Queue full, drop frame (or could log warning)
					}
				}
			}
		}
	}
}

func (b *Bridge) publishWorker() {
	for {
		select {
		case <-b.stopChan:
			return
		case frame := <-b.publishQueue:
			b.mu.RLock()
			mqtt := b.mqttClient
			topic := b.dataTopic
			b.mu.RUnlock()

			if mqtt != nil && mqtt.IsConnected() && len(frame) > 0 {
				mqtt.Publish(topic, frame)
			}
		}
	}
}

func (b *Bridge) processSerialData(data []byte) [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	var frames [][]byte
	for _, db := range data {
		switch db {
		case STX:
			if b.inFrame && len(b.frameBuffer) > 0 {
				// Make a copy before clearing buffer
				frameCopy := make([]byte, len(b.frameBuffer))
				copy(frameCopy, b.frameBuffer)
				frames = append(frames, frameCopy)
				b.frameBuffer = b.frameBuffer[:0]
			}
			b.frameBuffer = append(b.frameBuffer, db)
			b.inFrame = true
		case ETX:
			b.frameBuffer = append(b.frameBuffer, db)
			// Make a copy before clearing buffer
			frameCopy := make([]byte, len(b.frameBuffer))
			copy(frameCopy, b.frameBuffer)
			frames = append(frames, frameCopy)
			b.frameBuffer = b.frameBuffer[:0]
			b.inFrame = false
		default:
			if len(b.frameBuffer) >= MaxFrameBufferSize {
				// Make a copy before clearing buffer
				frameCopy := make([]byte, len(b.frameBuffer))
				copy(frameCopy, b.frameBuffer)
				frames = append(frames, frameCopy)
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

	// Stop data activity monitoring (using locked version since we hold mu)
	b.stopDataActivityTimerLocked()

	// Signal stop to worker goroutines (only once)
	select {
	case <-b.stopChan:
		// Already closed
	default:
		close(b.stopChan)
	}

	if b.mqttClient != nil && b.mqttClient.IsConnected() {
		statusTopic := fmt.Sprintf("bridges/%s/status", b.bridgeID)
		offlinePayload, _ := json.Marshal(StatusUpdate{Status: "offline"})
		b.mqttClient.PublishRetained(statusTopic, offlinePayload)
		b.mqttClient.Disconnect()
	}
	if b.serialPort != nil {
		b.serialPort.Close()
		b.serialPort = nil
	}

	// Update internal status (acquire statusMutex while holding mu - same order as everywhere)
	b.statusMutex.Lock()
	b.currentStatus = "offline"
	b.statusMutex.Unlock()

	return nil
}

func (b *Bridge) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.serialPort != nil && b.mqttClient != nil && b.mqttClient.IsConnected()
}
