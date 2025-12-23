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

//go:embed Icon.png
var IconBytes []byte

//go:embed DarkLogo.png
var DarkLogoBytes []byte

type ConnectionState int

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateError
)

type App struct {
	fyneApp       fyne.App
	window        fyne.Window
	bridge        *bridge.Bridge
	portSelect    *widget.Select
	bridgeIDEntry *widget.Entry
	connectBtn    *widget.Button
	statusLabel   *canvas.Text
	statusDot     *canvas.Circle
	rxIndicator   *canvas.Circle
	leftContent   *fyne.Container
	portLabel     *widget.Label
	portContainer *fyne.Container
	state         ConnectionState
	rxTimer       *time.Timer
	rxTimerMutex  sync.Mutex
}

func NewApp() *App {
	a := app.NewWithID("io.scorescrape.bridge")
	a.Settings().SetTheme(&ShadcnTheme{})
	w := a.NewWindow("ScoreScrape Bridge")
	w.Resize(fyne.NewSize(380, 420))
	w.CenterOnScreen()
	w.SetFixedSize(true)
	if len(IconBytes) > 0 {
		icon := fyne.NewStaticResource("Icon.png", IconBytes)
		a.SetIcon(icon)
		w.SetIcon(icon)
	}
	ui := &App{fyneApp: a, window: w, state: StateDisconnected}
	ui.buildUI()
	return ui
}

func (a *App) Run() {
	a.loadPreferences()
	a.refreshPorts()
	a.window.ShowAndRun()
}

func (a *App) buildUI() {
	logo := canvas.NewImageFromReader(bytes.NewReader(DarkLogoBytes), "logo.png")
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(180, 45))

	a.statusDot = canvas.NewCircle(color.RGBA{100, 100, 100, 255})
	a.statusDot.Resize(fyne.NewSize(10, 10))
	a.rxIndicator = canvas.NewCircle(color.RGBA{100, 100, 100, 255})
	a.rxIndicator.Resize(fyne.NewSize(8, 8))

	a.statusLabel = canvas.NewText("DISCONNECTED", color.RGBA{150, 150, 150, 255})
	a.statusLabel.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	a.statusLabel.TextSize = 12

	a.portSelect = widget.NewSelect([]string{}, nil)
	a.portSelect.PlaceHolder = "Select Serial Port..."
	a.portLabel = widget.NewLabel("")
	a.portLabel.TextStyle = fyne.TextStyle{Bold: true}
	a.portLabel.Alignment = fyne.TextAlignCenter
	a.portLabel.Hide()
	a.portContainer = container.NewMax(a.portSelect, container.NewCenter(a.portLabel))

	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { a.refreshPorts() })
	refreshBtn.Importance = widget.MediumImportance

	a.bridgeIDEntry = widget.NewEntry()
	a.bridgeIDEntry.PlaceHolder = "Paste Bridge ID"

	a.connectBtn = widget.NewButton("Connect Bridge", func() { a.toggleConnection() })
	a.connectBtn.SetIcon(theme.LoginIcon())
	a.connectBtn.Importance = widget.HighImportance

	// Improved spacing and layout
	statusRow := container.NewHBox(
		layout.NewSpacer(),
		container.NewPadded(a.statusDot),
		container.NewPadded(a.statusLabel),
		container.NewPadded(a.rxIndicator),
		container.NewPadded(widget.NewLabel("RX")),
		layout.NewSpacer(),
	)

	form := container.NewVBox(
		widget.NewLabelWithStyle("SERIAL PORT", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, nil, refreshBtn, a.portContainer),
		container.NewPadded(widget.NewLabel("")),
		widget.NewLabelWithStyle("BRIDGE ID", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		a.bridgeIDEntry,
	)

	card := widget.NewCard("Configuration", "", form)

	a.leftContent = container.NewVBox(
		container.NewPadded(logo),
		container.NewPadded(statusRow),
		container.NewPadded(widget.NewSeparator()),
		container.NewPadded(card),
		container.NewPadded(a.connectBtn),
		layout.NewSpacer(),
	)
	a.window.SetContent(a.leftContent)
}

func (a *App) updateState(s ConnectionState) {
	colors := map[ConnectionState]color.Color{StateDisconnected: color.RGBA{100, 100, 100, 255}, StateConnected: color.RGBA{34, 197, 94, 255}, StateError: color.RGBA{239, 68, 68, 255}, StateConnecting: color.RGBA{103, 103, 228, 255}}
	texts := map[ConnectionState]string{StateDisconnected: "DISCONNECTED", StateConnected: "CONNECTED", StateError: "ERROR", StateConnecting: "CONNECTING"}

	// Batch UI updates to avoid multiple refreshes
	a.statusDot.FillColor = colors[s]
	a.statusLabel.Text = texts[s]
	a.statusLabel.Color = colors[s]
	a.state = s

	// Single refresh call for better performance
	a.statusDot.Refresh()
	a.statusLabel.Refresh()
	a.updateButtonState()
}

func (a *App) updateButtonState() {
	if a.connectBtn == nil {
		return
	}
	// Fyne widgets are thread-safe, can update directly
	if a.state == StateConnected || a.state == StateConnecting {
		a.connectBtn.SetText("Disconnect Bridge")
		a.connectBtn.SetIcon(theme.LogoutIcon())
		// Low importance for disconnect action (grey color)
		a.connectBtn.Importance = widget.LowImportance
	} else {
		a.connectBtn.SetText("Connect Bridge")
		a.connectBtn.SetIcon(theme.LoginIcon())
		// High importance for primary action (blue/green color)
		a.connectBtn.Importance = widget.HighImportance
	}
	// Refresh button to ensure visual changes take effect
	a.connectBtn.Refresh()
}

func (a *App) refreshPorts() {
	// Run port refresh in background to avoid blocking UI
	go func() {
		ports, _ := bridge.GetAvailablePorts()
		// Fyne widgets are thread-safe
		if a.portSelect != nil {
			a.portSelect.SetOptions(ports)
		}
	}()
}

func (a *App) loadPreferences() {
	a.bridgeIDEntry.SetText(a.fyneApp.Preferences().String("bridge_id"))
}

func (a *App) toggleConnection() {
	if a.state == StateConnected || a.state == StateConnecting {
		if a.bridge != nil {
			a.bridge.Disconnect()
		}
		a.resetRxIndicator()
		a.updateState(StateDisconnected)
	} else {
		go a.connectAndRun(a.bridgeIDEntry.Text, a.portSelect.Selected)
	}
}

func (a *App) connectAndRun(id, port string) {
	if id == "" || port == "" {
		return
	}
	a.fyneApp.Preferences().SetString("bridge_id", id)
	a.fyneApp.Preferences().SetString("serial_port", port)

	a.bridge = bridge.New(id)
	a.updateState(StateConnecting)

	a.bridge.SetConnectionLostHandler(func(err error) {
		a.updateState(StateError)
		a.reconnect(id, port)
	})

	if err := a.bridge.Connect(port, 9600); err != nil {
		a.updateState(StateError)
		return
	}

	a.updateState(StateConnected)

	err := a.bridge.Start(func(d []byte) {
		a.onRxData()
	}, func(m string) {
		// Logging disabled - app is simplified to just send data
	})

	if err != nil && a.state == StateConnected {
		a.updateState(StateError)
		a.reconnect(id, port)
	}
}

func (a *App) reconnect(id, port string) {
	if a.state != StateError {
		return
	}
	time.Sleep(2 * time.Second)
	if a.state == StateError {
		go a.connectAndRun(id, port)
	}
}

func (a *App) onRxData() {
	a.rxTimerMutex.Lock()
	defer a.rxTimerMutex.Unlock()

	// Stop existing timer if it exists
	if a.rxTimer != nil {
		a.rxTimer.Stop()
	}

	// Only update indicator if it's not already green (avoid unnecessary refreshes)
	if a.rxIndicator.FillColor != (color.RGBA{34, 197, 94, 255}) {
		a.rxIndicator.FillColor = color.RGBA{34, 197, 94, 255}
		a.rxIndicator.Refresh()
	}

	// Create a new timer that will turn off the indicator after 3 seconds
	a.rxTimer = time.AfterFunc(3*time.Second, func() {
		a.rxTimerMutex.Lock()
		defer a.rxTimerMutex.Unlock()
		// Turn off RX indicator (gray)
		a.rxIndicator.FillColor = color.RGBA{100, 100, 100, 255}
		a.rxIndicator.Refresh()
		a.rxTimer = nil
	})
}

func (a *App) resetRxIndicator() {
	a.rxTimerMutex.Lock()
	defer a.rxTimerMutex.Unlock()

	// Stop timer if it exists
	if a.rxTimer != nil {
		a.rxTimer.Stop()
		a.rxTimer = nil
	}

	// Reset RX indicator to gray - Fyne handles thread safety
	a.rxIndicator.FillColor = color.RGBA{100, 100, 100, 255}
	a.rxIndicator.Refresh()
}

// ShadcnTheme implementation
type ShadcnTheme struct{}

func (t *ShadcnTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	black := color.RGBA{0, 0, 0, 255}
	secondary := color.RGBA{103, 103, 228, 255}
	zinc50 := color.RGBA{250, 250, 250, 255}
	zinc800 := color.RGBA{39, 39, 42, 255}
	switch n {
	case theme.ColorNameBackground:
		return black
	case theme.ColorNameForeground:
		return zinc50
	case theme.ColorNamePrimary:
		return secondary
	case theme.ColorNameButton:
		return zinc800
	}
	return theme.DefaultTheme().Color(n, v)
}

func (t *ShadcnTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (t *ShadcnTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (t *ShadcnTheme) Size(n fyne.ThemeSizeName) float32       { return theme.DefaultTheme().Size(n) }
