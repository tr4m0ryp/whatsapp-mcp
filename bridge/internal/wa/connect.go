package wa

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Connect establishes the WhatsApp connection, running the QR pairing flow
// when no session exists yet. Returns false when no stable connection could
// be established within the retry budget.
func Connect(client *whatsmeow.Client, logger waLog.Logger) bool {
	// Create channel to track connection success
	connected := make(chan bool, 1)

	maxRetries := 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		logger.Infof("Connection attempt %d/%d...", attempt, maxRetries)

		if client.Store.ID == nil {
			// No ID stored, this is a new client, need to pair with phone
			if pairWithQR(client, logger, connected, attempt == maxRetries) {
				goto connectionSuccess
			}
			if attempt == maxRetries {
				return false
			}
			continue
		}

		// Already logged in, just connect
		if err := client.Connect(); err != nil {
			logger.Errorf("Failed to connect (attempt %d): %v", attempt, err)
			if attempt == maxRetries {
				return false
			}
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}

connectionSuccess:

	// Wait a moment for connection to stabilize
	time.Sleep(2 * time.Second)

	if !client.IsConnected() {
		logger.Errorf("Failed to establish stable connection")
		return false
	}
	return true
}

// pairWithQR runs one QR pairing attempt: prints the code, waits for the
// scan, and reports whether the session authenticated.
func pairWithQR(client *whatsmeow.Client, logger waLog.Logger, connected chan bool, lastAttempt bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		logger.Errorf("Failed to get QR channel: %v", err)
		if !lastAttempt {
			time.Sleep(5 * time.Second)
		}
		return false
	}

	if err := client.Connect(); err != nil {
		logger.Errorf("Failed to connect: %v", err)
		if !lastAttempt {
			time.Sleep(5 * time.Second)
		}
		return false
	}

	// Print QR code for pairing with phone
	qrCodeShown := false
	for evt := range qrChan {
		if evt.Event == "code" {
			if !qrCodeShown {
				fmt.Println("\nScan this QR code with your WhatsApp app:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				fmt.Println("\nWaiting for QR code scan...")
				qrCodeShown = true
			}
		} else if evt.Event == "success" {
			connected <- true
			break
		} else if evt.Event == "timeout" {
			logger.Warnf("QR code timed out")
			break
		}
	}

	// Wait for connection with timeout
	select {
	case <-connected:
		fmt.Println("\nSuccessfully connected and authenticated!")
		return true
	case <-ctx.Done():
		logger.Errorf("Timeout waiting for QR code scan")
		client.Disconnect()
		if !lastAttempt {
			time.Sleep(10 * time.Second)
		}
		return false
	}
}

// RunReconnectLoop retries the connection with exponential backoff whenever
// a reconnect signal arrives, until exitChan closes the loop.
func RunReconnectLoop(client *whatsmeow.Client, logger waLog.Logger, reconnectChan chan bool, exitChan <-chan os.Signal) {
	reconnectBackoff := time.Second * 5
	maxBackoff := time.Minute * 5

	for {
		select {
		case <-reconnectChan:
			logger.Infof("🔄 Attempting to reconnect...")

			// Wait before reconnecting
			time.Sleep(reconnectBackoff)

			// Try to reconnect
			if !client.IsConnected() {
				err := client.Connect()
				if err != nil {
					logger.Errorf("❌ Reconnection failed: %v", err)
					// Increase backoff for next attempt
					reconnectBackoff = reconnectBackoff * 2
					if reconnectBackoff > maxBackoff {
						reconnectBackoff = maxBackoff
					}
					// Signal another reconnection attempt
					select {
					case reconnectChan <- true:
					default:
					}
				} else {
					logger.Infof("✓ Reconnected successfully")
					// Reset backoff on successful connection
					reconnectBackoff = time.Second * 5
				}
			} else {
				logger.Infof("Already connected, skipping reconnection")
				reconnectBackoff = time.Second * 5
			}

		case <-exitChan:
			return
		}
	}
}
