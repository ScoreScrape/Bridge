package main

import (
	"bridge/pkg/bridge"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func runCLI() {
	bridgeID := os.Getenv("BRIDGE_ID")
	if bridgeID == "" {
		log.Fatal("BRIDGE_ID environment variable is required for CLI mode")
	}

	// Hardcoded expected device path
	serialPort := "/dev/ttyUSB0"

	// Check if serial port exists
	if _, err := os.Stat(serialPort); os.IsNotExist(err) {
		log.Printf(`
Oops! Looks like the serial port is not configured correctly.

In your docker config, make sure the serial port is mapped correctly.
Make sure to only change the FIRST device path.
Ex: /dev/<YOUR_SERIAL_PORT>:/dev/ttyUSB0
`)

		log.Fatal("Serial port configuration error")
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	b := bridge.New(bridgeID)

	b.SetConnectionLostHandler(func(err error) {
		log.Printf("Connection lost: %v", err)
	})

	reconnectDelay := time.Second
	maxReconnectDelay := 60 * time.Second

	for {
		log.Printf("Connecting to serial port %s (bridge ID: %s)...", serialPort, bridgeID)

		if err := b.Connect(serialPort, bridge.DefaultBaudRate); err != nil {
			log.Printf("Connection failed: %v", err)
			log.Printf("Retrying in %v...", reconnectDelay)

			select {
			case <-sigChan:
				log.Println("Shutdown signal received")
				return
			case <-time.After(reconnectDelay):
				reconnectDelay = reconnectDelay * 2
				if reconnectDelay > maxReconnectDelay {
					reconnectDelay = maxReconnectDelay
				}
				continue
			}
		}

		log.Println("Connected successfully")
		reconnectDelay = time.Second

		errChan := make(chan error, 1)
		go func() {
			err := b.Start(nil, func(msg string) {
				log.Println(msg)
			})
			errChan <- err
		}()

		select {
		case <-sigChan:
			log.Println("Shutting down...")
			b.Disconnect()
			time.Sleep(500 * time.Millisecond)
			log.Println("Bridge stopped")
			return
		case err := <-errChan:
			if err != nil {
				log.Printf("Bridge error: %v", err)
			}
			b.Disconnect()
			log.Println("Reconnecting...")
			time.Sleep(reconnectDelay)
		}
	}
}
