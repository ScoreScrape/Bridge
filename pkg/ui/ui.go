package ui

import (
	"bridge/pkg/bridge"
	"bytes"
	_ "embed"
	"image/color"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed ../../assets/Icon.png
var iconBytes []byte

//go:embed ../../assets/DarkLogo.png
var logoBytes []byte

var (
	gray   = color.RGBA{100, 100, 100, 255}
	green  = color.RGBA{34, 197, 94, 255}
	red    = color.RGBA{239, 68, 68, 255}
	purple = color.RGBA{103, 103, 228, 255}
)

type state int

const (
	disconnected state = iota
	connecting
	connected
	errored
	reconnecting
)

var stateColors = map[state]color.Color{
	disconnected: gray,
	connecting:   purple,
	connected:    green,
	errored:      red,
	reconnecting: color.RGBA{251, 191, 36, 255}, // amber/orange
}

var stateLabels = map[state]string{
	disconnected: "DISCONNECTED",
	connecting:   "CONNECTING",
	connected:    "CONNECTED",
	errored:      "ERROR",
	reconnecting: "RECONNECTING",
}

type App struct {
	fyne   fyne.App
	window fyne.Window
	bridge *bridge.Bridge

	portSelect *widget.Select
	idEntry    *widget.Entry
	connectBtn *widget.Button
	statusDot  *canvas.Circle
	statusText *canvas.Text
	rxDot      *canvas.Circle
	errorLabel *widget.Label

	mu                sync.Mutex
	state             state
	busy              bool
	rxTimer           *time.Timer
	reconnectAttempts int
}

func NewApp() *App {
	a := app.NewWithID("io.scorescrape.bridge")
	a.Settings().SetTheme(&darkTheme{})

	w := a.NewWindow("ScoreScrape Bridge")
	w.Resize(fyne.NewSize(380, 420))
	w.CenterOnScreen()
	w.SetFixedSize(true)

	if len(iconBytes) > 0 {
		icon := fyne.NewStaticResource("Icon.png", iconBytes)
		a.SetIcon(icon)
		w.SetIcon(icon)
	}

	ui := &App{fyne: a, window: w}
	ui.build()
	return ui
}

func (a *App) Run() {
	a.idEntry.SetText(a.fyne.Preferences().String("bridge_id"))
	a.refreshPorts()
	a.window.ShowAndRun()
}

func (a *App) build() {
	logo := canvas.NewImageFromReader(bytes.NewReader(logoBytes), "logo.png")
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(180, 45))

	a.statusDot = canvas.NewCircle(gray)
	a.statusDot.Resize(fyne.NewSize(10, 10))

	a.statusText = canvas.NewText("DISCONNECTED", gray)
	a.statusText.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	a.statusText.TextSize = 12

	a.rxDot = canvas.NewCircle(gray)
	a.rxDot.Resize(fyne.NewSize(8, 8))

	a.portSelect = widget.NewSelect(nil, nil)
	a.portSelect.PlaceHolder = "Select Serial Port..."

	refresh := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { a.refreshPorts() })
	refresh.Importance = widget.MediumImportance

	a.idEntry = widget.NewEntry()
	a.idEntry.PlaceHolder = "Paste Bridge ID"

	a.connectBtn = widget.NewButton("Connect Bridge", a.toggle)
	a.connectBtn.SetIcon(theme.LoginIcon())
	a.connectBtn.Importance = widget.HighImportance

	a.errorLabel = widget.NewLabel("")
	a.errorLabel.Wrapping = fyne.TextWrapWord
	a.errorLabel.TextStyle = fyne.TextStyle{Italic: true}
	a.errorLabel.Hide()

	statusRow := container.NewHBox(
		layout.NewSpacer(),
		container.NewPadded(a.statusDot),
		container.NewPadded(a.statusText),
		container.NewPadded(a.rxDot),
		container.NewPadded(widget.NewLabel("RX")),
		layout.NewSpacer(),
	)

	form := container.NewVBox(
		widget.NewLabelWithStyle("SERIAL PORT", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, nil, refresh, a.portSelect),
		container.NewPadded(widget.NewLabel("")),
		widget.NewLabelWithStyle("BRIDGE ID", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		a.idEntry,
	)

	a.window.SetContent(container.NewVBox(
		container.NewPadded(logo),
		container.NewPadded(statusRow),
		container.NewPadded(widget.NewSeparator()),
		container.NewPadded(widget.NewCard("Configuration", "", form)),
		container.NewPadded(a.errorLabel),
		container.NewPadded(a.connectBtn),
		layout.NewSpacer(),
	))
}

func (a *App) refreshPorts() {
	go func() {
		ports, _ := bridge.GetAvailablePorts()
		a.portSelect.SetOptions(ports)
	}()
}

func (a *App) toggle() {
	a.mu.Lock()
	s := a.state
	busy := a.busy
	a.mu.Unlock()

	if s == connected || s == connecting || s == reconnecting {
		go a.disconnect()
		return
	}

	if busy {
		return
	}

	a.mu.Lock()
	a.busy = true
	a.mu.Unlock()

	go a.connect(a.idEntry.Text, a.portSelect.Selected)
}

func (a *App) connect(id, port string) {
	defer func() {
		a.mu.Lock()
		a.busy = false
		a.mu.Unlock()
	}()

	if id == "" {
		a.setError("Please enter a Bridge ID")
		return
	}
	if port == "" {
		a.setError("Please select a serial port")
		return
	}

	a.clearError()
	a.fyne.Preferences().SetString("bridge_id", id)
	a.fyne.Preferences().SetString("serial_port", port)

	// Ensure any previous bridge is fully cleaned up
	a.cleanupBridge()

	a.bridge = bridge.New(id)
	a.setState(connecting)

	a.bridge.SetConnectionLostHandler(func(err error) {
		// Don't auto-reconnect if another bridge with same ID is already connected
		if err != nil && contains(err.Error(), "EOF") {
			errMsg := a.formatConnectionError(err)
			a.cleanupBridge()
			a.setState(disconnected)
			a.setError(errMsg)
			return
		}

		a.mu.Lock()
		a.reconnectAttempts++
		attempts := a.reconnectAttempts
		a.mu.Unlock()

		if attempts <= 2 {
			a.setState(reconnecting)
			a.setError(a.formatConnectionError(err))
			a.reconnect(id, port)
		} else {
			errMsg := a.formatConnectionError(err)
			a.cleanupBridge()
			a.setState(disconnected)
			a.setError(errMsg + " (reconnection failed after 2 attempts)")
		}
	})

	if err := a.bridge.Connect(port, 9600); err != nil {
		errMsg := a.formatConnectionError(err)
		a.cleanupBridge()
		a.setState(disconnected)
		a.setError(errMsg)

		a.mu.Lock()
		a.reconnectAttempts = 0
		a.mu.Unlock()
		return
	}

	a.mu.Lock()
	a.reconnectAttempts = 0
	a.mu.Unlock()

	a.setState(connected)

	err := a.bridge.Start(func(d []byte) { a.onRx() })

	a.mu.Lock()
	s := a.state
	a.mu.Unlock()

	if err != nil && s == connected {
		// Don't auto-reconnect if another bridge with same ID is already connected
		if contains(err.Error(), "EOF") {
			errMsg := a.formatConnectionError(err)
			a.cleanupBridge()
			a.setState(disconnected)
			a.setError(errMsg)
			return
		}

		a.mu.Lock()
		a.reconnectAttempts++
		attempts := a.reconnectAttempts
		a.mu.Unlock()

		if attempts <= 2 {
			a.setState(reconnecting)
			a.setError(a.formatConnectionError(err))
			a.reconnect(id, port)
		} else {
			errMsg := a.formatConnectionError(err)
			a.cleanupBridge()
			a.setState(disconnected)
			a.setError(errMsg + " (reconnection failed after 2 attempts)")
		}
	}
}

func (a *App) cleanupBridge() {
	if a.bridge != nil {
		a.bridge.Disconnect()
		// Give the serial port time to fully close
		time.Sleep(100 * time.Millisecond)
		a.bridge = nil
	}
	a.resetRx()
}

func (a *App) disconnect() {
	a.cleanupBridge()
	a.setState(disconnected)
	a.clearError()
}

func (a *App) reconnect(id, port string) {
	a.mu.Lock()
	s := a.state
	a.mu.Unlock()

	if s != reconnecting {
		return
	}

	time.Sleep(2 * time.Second)

	a.mu.Lock()
	s = a.state
	a.mu.Unlock()

	if s == reconnecting {
		go a.connect(id, port)
	}
}

func (a *App) setState(s state) {
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()

	c := stateColors[s]
	a.statusDot.FillColor = c
	a.statusText.Text = stateLabels[s]
	a.statusText.Color = c
	a.statusDot.Refresh()
	a.statusText.Refresh()

	if s == connected || s == connecting || s == reconnecting {
		a.connectBtn.SetText("Disconnect Bridge")
		a.connectBtn.SetIcon(theme.LogoutIcon())
		a.connectBtn.Importance = widget.LowImportance
	} else {
		a.connectBtn.SetText("Connect Bridge")
		a.connectBtn.SetIcon(theme.LoginIcon())
		a.connectBtn.Importance = widget.HighImportance
	}
	a.connectBtn.Refresh()

	if s == connected {
		a.clearError()
	}
}

func (a *App) setError(msg string) {
	a.errorLabel.SetText("⚠️  " + msg)
	a.errorLabel.Show()
}

func (a *App) clearError() {
	a.errorLabel.SetText("")
	a.errorLabel.Hide()
}

func (a *App) formatConnectionError(err error) string {
	errMsg := err.Error()

	// MQTT duplicate connection (EOF means another bridge with same ID is connected)
	if contains(errMsg, "EOF") {
		return "Another bridge with this ID is already connected. Disconnect it first or use a different Bridge ID."
	}

	// Serial port errors
	if contains(errMsg, "busy") || contains(errMsg, "in use") {
		return "Serial port is busy. Wait a moment and try again."
	}
	if contains(errMsg, "permission denied") || contains(errMsg, "access is denied") {
		return "Cannot access serial port. Try closing other apps using this port."
	}
	if contains(errMsg, "no such file") || contains(errMsg, "cannot find the file") {
		return "Serial port not found. Please refresh and select a valid port."
	}
	if contains(errMsg, "device not configured") || contains(errMsg, "port is not open") {
		return "Serial port is not available. Check if device is connected."
	}

	// MQTT errors
	if contains(errMsg, "network is unreachable") || contains(errMsg, "no route to host") {
		return "No internet connection. Check your network settings."
	}
	if contains(errMsg, "connection refused") {
		return "Cannot reach MQTT broker. Check your internet connection."
	}
	if contains(errMsg, "timeout") || contains(errMsg, "timed out") {
		return "Connection timeout. Check your internet connection."
	}
	if contains(errMsg, "not authorized") || contains(errMsg, "bad user name or password") {
		return "Authentication failed. Check your Bridge ID."
	}

	// Generic fallback
	return "Connection failed: " + errMsg
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (a *App) onRx() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.rxTimer != nil {
		a.rxTimer.Stop()
	}

	if a.rxDot.FillColor != green {
		a.rxDot.FillColor = green
		a.rxDot.Refresh()
	}

	a.rxTimer = time.AfterFunc(3*time.Second, func() {
		a.mu.Lock()
		a.rxDot.FillColor = gray
		a.rxDot.Refresh()
		a.rxTimer = nil
		a.mu.Unlock()
	})
}

func (a *App) resetRx() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.rxTimer != nil {
		a.rxTimer.Stop()
		a.rxTimer = nil
	}
	a.rxDot.FillColor = gray
	a.rxDot.Refresh()
}

type darkTheme struct{}

func (t *darkTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return color.Black
	case theme.ColorNameForeground:
		return color.RGBA{250, 250, 250, 255}
	case theme.ColorNamePrimary:
		return purple
	case theme.ColorNameButton:
		return color.RGBA{39, 39, 42, 255}
	case theme.ColorNameInputBackground:
		return color.RGBA{24, 24, 27, 255}
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return color.RGBA{113, 113, 122, 255}
	}
	return theme.DefaultTheme().Color(n, v)
}

func (t *darkTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (t *darkTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (t *darkTheme) Size(n fyne.ThemeSizeName) float32       { return theme.DefaultTheme().Size(n) }
