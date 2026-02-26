package bridge

import (
	"bufio"
	"strings"

	"go.bug.st/serial"
)

const (
	DefaultBaudRate   = 9600
	SerialReadTimeout = 100 // in ms, without this the windows app crashes
)

type SerialPort struct {
	port   serial.Port
	reader *bufio.Reader
}

func OpenSerialPort(name string, baud int) (*SerialPort, error) {
	port, err := serial.Open(name, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, err
	}

	if err := port.SetReadTimeout(SerialReadTimeout); err != nil {
		port.Close()
		return nil, err
	}

	return &SerialPort{
		port:   port,
		reader: bufio.NewReader(port),
	}, nil
}

func (s *SerialPort) Read(buf []byte) (int, error) {
	return s.reader.Read(buf)
}

func (s *SerialPort) Close() error {
	return s.port.Close()
}

func GetAvailablePorts() ([]string, error) {
	all, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}

	var ports []string
	for _, p := range all {
		if !isBluetoothPort(p) {
			ports = append(ports, p)
		}
	}
	return ports, nil
}

// dont show bluetooth ports
func isBluetoothPort(name string) bool {
	low := strings.ToLower(name)
	if strings.Contains(low, "bluetooth-incoming-port") || strings.Contains(low, "modem") {
		return true
	}
	return strings.Contains(low, "bluetooth") && !strings.Contains(low, "usb")
}
