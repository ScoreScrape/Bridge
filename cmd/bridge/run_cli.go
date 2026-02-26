package main

import (
	"bridge/pkg/bridge"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	serialPort         = "/dev/ttyUSB0"
	maxFailures        = 10
	circuitBreakerWait = 15 * time.Minute
	healthCheckPeriod  = 30 * time.Second
)

func runCLI() {
	bridgeID := os.Getenv("BRIDGE_ID")
	if bridgeID == "" {
		log.Fatal("BRIDGE_ID required")
	}

	if _, err := os.Stat(serialPort); os.IsNotExist(err) {
		log.Printf("Serial port %s not found. Check your docker device mapping.", serialPort)
		log.Fatal("Ex: --device=/dev/YOUR_PORT:/dev/ttyUSB0")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	b, err := bridge.New(bridgeID)
	if err != nil {
		log.Fatal(err)
	}

	var lastDisconnect time.Time
	var rapidCount int

	b.SetConnectionLostHandler(func(err error) {
		if !lastDisconnect.IsZero() && time.Since(lastDisconnect) < 5*time.Second {
			rapidCount++
		} else {
			rapidCount = 0
		}
		lastDisconnect = time.Now()

		if rapidCount >= 2 {
			log.Printf("Connection lost: %v (rapid disconnects - possible session takeover)", err)
		} else {
			log.Printf("Connection lost: %v (will auto-reconnect)", err)
		}
	})

	backoff := newBackoff()

	for {
		if backoff.failures >= maxFailures {
			log.Printf("Circuit breaker: %d failures, waiting %v", backoff.failures, circuitBreakerWait)
			if waitOrSignal(sig, circuitBreakerWait) {
				return
			}
			backoff.reset()
		}

		log.Printf("Connecting to %s (bridge: %s, attempt %d)", serialPort, bridgeID, backoff.failures+1)

		if err := b.Connect(serialPort, bridge.DefaultBaudRate); err != nil {
			backoff.fail()
			log.Printf("Connect failed: %v", err)
			if backoff.failures < maxFailures {
				log.Printf("Retry in %v", backoff.delay)
			}
			if waitOrSignal(sig, backoff.delay) {
				return
			}
			continue
		}

		log.Println("Connected")
		backoff.reset()
		connectTime := time.Now()

		done := make(chan error, 1)
		go func() {
			done <- b.Start(nil)
		}()

		health := time.NewTicker(healthCheckPeriod)

	loop:
		for {
			select {
			case <-sig:
				log.Println("Shutting down...")
				b.Disconnect()
				health.Stop()
				return

			case <-health.C:
				if b.IsMQTTReconnecting() {
					dur, attempts := b.GetMQTTReconnectionInfo()
					if dur > 5*time.Minute {
						log.Printf("MQTT stuck reconnecting (%v, %d attempts), forcing restart", dur, attempts)
						break loop
					}
					if attempts > 0 {
						log.Printf("MQTT reconnecting (%v, %d attempts)", dur, attempts)
					}
				} else if !b.IsHealthy() {
					log.Println("Bridge unhealthy, forcing restart")
					break loop
				}

			case err := <-done:
				uptime := time.Since(connectTime)
				if err != nil {
					log.Printf("Bridge error after %v: %v", uptime, err)
				}
				b.Disconnect()

				if uptime < 30*time.Second {
					backoff.fail()
					log.Printf("Short connection (%v), failure %d/%d", uptime, backoff.failures, maxFailures)
				} else if backoff.failures > 0 {
					backoff.failures--
				}

				wait := 2 * time.Second
				if backoff.failures > 3 {
					wait = 10 * time.Second
				}
				if waitOrSignal(sig, wait) {
					health.Stop()
					return
				}
				break loop
			}
		}
		health.Stop()
	}
}

type backoff struct {
	delay    time.Duration
	failures int
}

func newBackoff() *backoff {
	return &backoff{delay: 5 * time.Second}
}

func (b *backoff) fail() {
	b.failures++
	b.delay = time.Duration(float64(b.delay) * 1.5)
	if b.delay > 5*time.Minute {
		b.delay = 5 * time.Minute
	}
}

func (b *backoff) reset() {
	b.delay = 5 * time.Second
	b.failures = 0
}

func waitOrSignal(sig chan os.Signal, d time.Duration) bool {
	select {
	case <-sig:
		log.Println("Shutdown signal received")
		return true
	case <-time.After(d):
		return false
	}
}
