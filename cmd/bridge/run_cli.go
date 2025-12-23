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

	// Hardcode the device path inside the container
	serialPort := "/dev/ttyUSB0"

	// Verify the specified port exists
	if _, err := os.Stat(serialPort); os.IsNotExist(err) {
		log.Fatalf("Serial port does not exist: %s. Ensure the device is properly mapped in docker-compose.yml", serialPort)
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
