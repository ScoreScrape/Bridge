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

	// Docker default device
	serialPort := "/dev/ttyUSB0"

	// Check if it exists, if not give the user a semi-helpful error message
	// TODO: probably come back and re-word this, im not sure it it's 100% clear to non-technical users..
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

	// Enhanced reconnection logic with circuit breaker
	reconnectDelay := 5 * time.Second // Start with longer initial delay
	maxReconnectDelay := 5 * time.Minute // Increase max delay significantly
	minReconnectDelay := 5 * time.Second
	consecutiveFailures := 0
	maxConsecutiveFailures := 10 // Circuit breaker threshold
	circuitBreakerDelay := 15 * time.Minute // Long delay when circuit is open
	lastSuccessTime := time.Now()

	for {
		// Circuit breaker: if too many consecutive failures, wait longer
		if consecutiveFailures >= maxConsecutiveFailures {
			log.Printf("Circuit breaker activated after %d consecutive failures. Waiting %v before retry...", 
				consecutiveFailures, circuitBreakerDelay)
			
			select {
			case <-sigChan:
				log.Println("Shutdown signal received")
				return
			case <-time.After(circuitBreakerDelay):
				// Reset failure count when circuit breaker timeout expires
				consecutiveFailures = 0
				reconnectDelay = minReconnectDelay
			}
		}

		log.Printf("Connecting to serial port %s (bridge ID: %s)... (attempt %d)", 
			serialPort, bridgeID, consecutiveFailures+1)

		if err := b.Connect(serialPort, bridge.DefaultBaudRate); err != nil {
			consecutiveFailures++
			log.Printf("Connection failed: %v (failure %d/%d)", err, consecutiveFailures, maxConsecutiveFailures)
			
			// Don't log retry message if circuit breaker will activate
			if consecutiveFailures < maxConsecutiveFailures {
				log.Printf("Retrying in %v...", reconnectDelay)
			}

			select {
			case <-sigChan:
				log.Println("Shutdown signal received")
				return
			case <-time.After(reconnectDelay):
				// Exponential backoff with jitter
				reconnectDelay = time.Duration(float64(reconnectDelay) * 1.5)
				if reconnectDelay > maxReconnectDelay {
					reconnectDelay = maxReconnectDelay
				}
				continue
			}
		}

		// Connection successful - reset failure tracking
		log.Println("Connected successfully")
		consecutiveFailures = 0
		reconnectDelay = minReconnectDelay
		lastSuccessTime = time.Now()

		errChan := make(chan error, 1)
		healthCheckTicker := time.NewTicker(30 * time.Second)
		defer healthCheckTicker.Stop()
		
		go func() {
			err := b.Start(nil, func(msg string) {
				log.Println(msg)
			})
			errChan <- err
		}()

		// Health monitoring loop
		healthCheckFailures := 0
		maxHealthCheckFailures := 5

		for {
			select {
			case <-sigChan:
				log.Println("Shutting down...")
				b.Disconnect()
				time.Sleep(500 * time.Millisecond)
				log.Println("Bridge stopped")
				return
			case err := <-errChan:
				connectionDuration := time.Since(lastSuccessTime)
				
				if err != nil {
					log.Printf("Bridge error after %v: %v", connectionDuration, err)
				}
				
				b.Disconnect()
				
				// If connection was very short-lived, treat as failure
				if connectionDuration < 30*time.Second {
					consecutiveFailures++
					log.Printf("Short-lived connection detected (%v), treating as failure %d/%d", 
						connectionDuration, consecutiveFailures, maxConsecutiveFailures)
				} else {
					// Connection lasted reasonable time, reset some failure tracking
					if consecutiveFailures > 0 {
						consecutiveFailures = max(0, consecutiveFailures-1)
					}
				}
				
				// Add minimum delay before reconnection to prevent tight loops
				minWait := 2 * time.Second
				if consecutiveFailures > 3 {
					minWait = 10 * time.Second
				}
				
				log.Printf("Waiting %v before reconnection attempt...", minWait)
				select {
				case <-sigChan:
					log.Println("Shutdown signal received")
					return
				case <-time.After(minWait):
					// Break out of inner loop to attempt reconnection
					goto reconnectLoop
				}
			case <-healthCheckTicker.C:
				// Periodic health check
				if !b.IsHealthy() {
					healthCheckFailures++
					log.Printf("Health check failed (%d/%d)", healthCheckFailures, maxHealthCheckFailures)
					
					if healthCheckFailures >= maxHealthCheckFailures {
						log.Printf("Device appears to be in unrecoverable state after %d failed health checks", healthCheckFailures)
						b.Disconnect()
						
						// Force a longer delay and reset to try recovery
						consecutiveFailures = maxConsecutiveFailures - 1 // Trigger near-circuit-breaker behavior
						goto reconnectLoop
					}
				} else {
					// Reset health check failures on successful check
					if healthCheckFailures > 0 {
						log.Printf("Health check recovered")
						healthCheckFailures = 0
					}
				}
			}
		}
		
		reconnectLoop:
		// Continue to outer loop for reconnection
	}
}

// Helper function for max (Go 1.21+)
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
