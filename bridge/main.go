// Command bridge links a WhatsApp account (via whatsmeow) to a local SQLite
// archive and a loopback REST API that the MCP server consumes. All the
// logic lives in internal/ packages; this file only wires them together.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/bridge/internal/api"
	"whatsapp-mcp/bridge/internal/auth"
	"whatsapp-mcp/bridge/internal/config"
	"whatsapp-mcp/bridge/internal/media"
	"whatsapp-mcp/bridge/internal/store"
	"whatsapp-mcp/bridge/internal/wa"
	"whatsapp-mcp/bridge/internal/webhook"
)

// CLI flag: request a full history sync at pair time.
// Only meaningful on a fresh pair (whatsapp.db deleted).
var fullHistoryPairFlag = flag.Bool("full-history-pair", false,
	"Request full history at pair time (only effective when re-pairing; no-op for existing sessions)")

// History-sync limits requested at pair time under --full-history-pair, and
// the OS version this device reports. Kept within the range a real linked
// desktop client would ask for.
const (
	fullSyncDaysLimit   = 365
	fullSyncSizeMbLimit = 2048
	deviceOSVersion     = "1.0.0"
)

func main() {
	flag.Parse()

	// Set up logger with DEBUG level for more detailed logging
	logger := waLog.Stdout("Client", "DEBUG", true)
	logger.Infof("Starting WhatsApp client...")

	// Refuse to start if a previous run halted on a terminal WhatsApp
	// condition. This runs before anything touches the network: the whole
	// point is that a banned or logged-out deployment stops reaching WhatsApp
	// at all, rather than being resurrected every few seconds by the service
	// manager.
	if halted, err := wa.CheckHaltFile(); err != nil {
		logger.Errorf("Failed to check halt file: %v", err)
		os.Exit(1)
	} else if halted != "" {
		wa.PrintHaltBanner(halted)
		return
	}

	forwardSelf := config.ForwardSelf()
	if forwardSelf {
		logger.Infof("FORWARD_SELF enabled: forwarding self messages to webhook")
	} else {
		logger.Infof("FORWARD_SELF disabled: self messages will NOT be forwarded")
	}

	// Create database connection for storing session data
	dbLog := waLog.Stdout("Database", "INFO", true)

	// Create directory for database if it doesn't exist
	if err := os.MkdirAll(config.StoreDir, 0755); err != nil {
		logger.Errorf("Failed to create store directory: %v", err)
		return
	}

	container, err := sqlstore.New(context.Background(), "sqlite3", "file:"+config.WhatsmeowDBPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		logger.Errorf("Failed to connect to database: %v", err)
		return
	}

	// Get device store - This contains session information
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			// No device exists, create one
			deviceStore = container.NewDevice()
			logger.Infof("Created new device")
		} else {
			logger.Errorf("Failed to get device: %v", err)
			return
		}
	}

	// Identify this linked device honestly.
	//
	// whatsmeow's defaults register the session as Os="whatsmeow",
	// PlatformType=UNKNOWN, OsVersion="0.1". The first is the library's own
	// name, which is what shows under Linked Devices on the phone; UNKNOWN is a
	// platform value no real client sends. Both are transmitted in the pairing
	// handshake, so this only takes effect when a new device is paired — an
	// existing session keeps whatever it registered with.
	wmstore.DeviceProps.Os = proto.String(config.DeviceName())
	wmstore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
	wmstore.DeviceProps.OsVersion = proto.String(deviceOSVersion)

	// Optionally request a full history sync at pair time.
	//
	// whatsmeow's default DeviceProps has RequireFullSync=false, which asks the
	// primary device for "recent" history only (typically ~3 months, decided by
	// the phone). Setting RequireFullSync=true with a larger FullSyncDaysLimit
	// flips the handshake to request full-history mode. The phone still decides
	// the actual cap. Only meaningful at pair time: for an already-paired
	// session (whatsapp.db present), this is a no-op because no new pair
	// handshake fires.
	//
	// The limits are deliberately modest. Asking for a decade of history and a
	// 100 GB quota — as this once did — is not something any real client does,
	// and the request itself is visible at the handshake.
	if *fullHistoryPairFlag {
		wmstore.DeviceProps.RequireFullSync = proto.Bool(true)
		wmstore.DeviceProps.HistorySyncConfig = &waCompanionReg.DeviceProps_HistorySyncConfig{
			FullSyncDaysLimit:   proto.Uint32(fullSyncDaysLimit),
			FullSyncSizeMbLimit: proto.Uint32(fullSyncSizeMbLimit),
			StorageQuotaMb:      proto.Uint32(fullSyncSizeMbLimit),
		}
		logger.Infof("--full-history-pair enabled: requesting history (days=%d, sizeMb=%d)",
			fullSyncDaysLimit, fullSyncSizeMbLimit)
	}

	// Create client instance
	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		logger.Errorf("Failed to create WhatsApp client")
		return
	}

	// Initialize message store
	messageStore, err := store.NewMessageStore()
	if err != nil {
		logger.Errorf("Failed to initialize message store: %v", err)
		return
	}
	defer func() { _ = messageStore.Close() }()

	if err := messageStore.MigrateLegacyLIDChatsToPhoneJIDs(config.WhatsmeowDBPath, logger); err != nil {
		logger.Errorf("Failed to migrate legacy LID chat rows: %v", err)
		return
	}

	if err := messageStore.MigrateLegacyLIDSendersToPhones(config.WhatsmeowDBPath, logger); err != nil {
		logger.Errorf("Failed to migrate legacy LID sender rows: %v", err)
		return
	}

	// Resolve the REST API port early: failing fast here means we don't run a
	// QR-pairing flow only to error out on an invalid port afterwards.
	port, err := config.Port()
	if err != nil {
		logger.Errorf("%v", err)
		return
	}

	// Load (or generate on first run) the bearer token used to authenticate
	// REST callers, and attach it to outbound webhook POSTs so a fail-closed
	// receiver accepts them. This MUST happen before the event handlers below
	// are registered: WhatsApp can deliver messages — including a burst of
	// history-sync backlog — as soon as the connection succeeds, and any
	// message handled before this assignment would go out with no bridge
	// token attached.
	bridgeToken, fresh, tokErr := auth.LoadOrCreateToken()
	if tokErr != nil {
		logger.Errorf("Failed to initialize bridge token: %v", tokErr)
		return
	}

	// Print the one-time setup banner immediately, before attempting to
	// connect/pair. LoadOrCreateToken already persisted the token to disk as
	// soon as it generated one; if the banner instead waited until after a
	// successful connection, a QR-pairing timeout or early exit would leave a
	// token on disk that was never shown to the user — and LoadOrCreateToken
	// would report fresh=false on every later run, so the banner would never
	// get a second chance to print it.
	if fresh {
		auth.PrintTokenBanner(bridgeToken, port)
	}

	halter := wa.NewHalter()

	handler := &wa.Handler{
		Client:      client,
		Store:       messageStore,
		Webhook:     &webhook.Sender{Token: bridgeToken},
		ForwardSelf: forwardSelf,
		Log:         logger,
		Halter:      halter,
	}
	handler.RegisterEventHandlers()

	if err := wa.Connect(client, logger, config.AllowPairing()); err != nil {
		logger.Errorf("Could not connect to WhatsApp: %v", err)
		os.Exit(1)
	}

	fmt.Println("\n✓ Connected to WhatsApp!")

	// Resolve the allow-listed roots that media_path values in /api/send must
	// live under. See the media package for the rationale.
	allowedMediaRoots, mrErr := media.ResolveRoots()
	if mrErr != nil {
		logger.Errorf("Failed to resolve media roots: %v", mrErr)
		return
	}
	logger.Infof("Allowed media roots: %v", allowedMediaRoots)

	restServer := &api.Server{
		Client:       client,
		Store:        messageStore,
		Port:         port,
		Token:        bridgeToken,
		MediaRoots:   allowedMediaRoots,
		SendLimiter:  ratelimit.New(messageStore, config.ColdMinInterval(), config.ColdDailyCap()),
	}
	restServer.Start()

	// Create a channel to keep the main goroutine alive
	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("REST server is running. Press Ctrl+C to disconnect and exit.")

	// Shut down on an operator signal or on a terminal WhatsApp condition.
	// Reconnection through transient drops is whatsmeow's job; the only thing
	// left for this goroutine to decide is when to stop trying altogether.
	select {
	case <-exitChan:
		fmt.Println("Disconnecting...")
	case <-halter.Halted():
		reason, detail := halter.Reason()
		logger.Errorf("Halting: %s (%s)", reason, detail)
		fmt.Printf("\nBridge halted: %s — see %s\n", reason, wa.HaltFilePath)
	}

	client.Disconnect()
}
