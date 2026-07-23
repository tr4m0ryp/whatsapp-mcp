package wa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// ErrPairingRequired is returned when there is no stored session and pairing
// has not been explicitly enabled.
//
// Pairing is opt-in because it is only ever useful with a human watching. An
// unattended process that pairs on demand will, after any logout, sit in a
// loop requesting fresh registration codes that nobody will scan — traffic
// that is indistinguishable from an attack on the pairing endpoint.
var ErrPairingRequired = errors.New("no WhatsApp session stored and pairing is disabled (set WHATSAPP_ALLOW_PAIRING=true and run interactively to pair)")

// connectRetries is how many times Connect retries a failing dial before
// giving up and letting the process exit. Retrying more here does not help:
// a session WhatsApp is willing to accept connects on the first or second
// attempt, and anything persistent is a condition retries cannot fix.
const connectRetries = 3

// Connect establishes the WhatsApp connection, running the QR pairing flow
// only when pairing is explicitly allowed. Returns an error when no stable
// connection could be established within the retry budget.
func Connect(client *whatsmeow.Client, logger waLog.Logger, allowPairing bool) error {
	if client.Store.ID == nil {
		if !allowPairing {
			return ErrPairingRequired
		}
		if err := pairWithQR(client, logger); err != nil {
			return err
		}
	} else {
		var lastErr error
		for attempt := 1; attempt <= connectRetries; attempt++ {
			logger.Infof("Connection attempt %d/%d...", attempt, connectRetries)
			lastErr = client.Connect()
			if lastErr == nil {
				break
			}
			logger.Errorf("Failed to connect (attempt %d): %v", attempt, lastErr)
			if attempt < connectRetries {
				time.Sleep(5 * time.Second)
			}
		}
		if lastErr != nil {
			return fmt.Errorf("connect after %d attempts: %w", connectRetries, lastErr)
		}
	}

	// Wait a moment for connection to stabilize.
	time.Sleep(2 * time.Second)

	if !client.IsConnected() {
		return errors.New("connection did not stabilize")
	}
	return nil
}

// pairWithQR runs the QR pairing flow: prints the code, waits for the scan,
// and reports whether the session authenticated. Single-shot on purpose —
// a scan either happens while someone is watching or it does not, and
// re-issuing codes unattended is the behaviour this package exists to avoid.
func pairWithQR(client *whatsmeow.Client, logger waLog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("get QR channel: %w", err)
	}

	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect for pairing: %w", err)
	}

	qrCodeShown := false
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			if !qrCodeShown {
				fmt.Println("\nScan this QR code with your WhatsApp app:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				fmt.Println("\nWaiting for QR code scan...")
				qrCodeShown = true
			}
		case "success":
			fmt.Println("\nSuccessfully connected and authenticated!")
			return nil
		case "timeout":
			client.Disconnect()
			return errors.New("QR code expired without being scanned")
		}
	}

	client.Disconnect()
	return errors.New("pairing ended without authenticating")
}
