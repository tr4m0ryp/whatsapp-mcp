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

	// Optionally request a full history sync at pair time.
	//
	// whatsmeow's default DeviceProps has RequireFullSync=false, which asks the
	// primary device for "recent" history only (typically ~3 months, decided by
	// the phone). Setting RequireFullSync=true with a large FullSyncDaysLimit
	// flips the handshake to request full-history mode. The phone still decides
	// the actual cap. Only meaningful at pair time: for an already-paired
	// session (whatsapp.db present), this is a no-op because no new pair
	// handshake fires.
	if *fullHistoryPairFlag {
		wmstore.DeviceProps.RequireFullSync = proto.Bool(true)
		wmstore.DeviceProps.HistorySyncConfig = &waCompanionReg.DeviceProps_HistorySyncConfig{
			FullSyncDaysLimit:   proto.Uint32(3650),
			FullSyncSizeMbLimit: proto.Uint32(102400),
			StorageQuotaMb:      proto.Uint32(102400),
		}
		logger.Infof("--full-history-pair enabled: requesting full history (days=3650, sizeMb=102400)")
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

	// Channel to signal reconnection needs
	reconnectChan := make(chan bool, 1)

	handler := &wa.Handler{
		Client:      client,
		Store:       messageStore,
		Webhook:     &webhook.Sender{Token: bridgeToken},
		ForwardSelf: forwardSelf,
		Log:         logger,
	}
	handler.RegisterEventHandlers(reconnectChan)

	if !wa.Connect(client, logger) {
		return
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
		Client:     client,
		Store:      messageStore,
		Port:       port,
		Token:      bridgeToken,
		MediaRoots: allowedMediaRoots,
	}
	restServer.Start()

	// Create a channel to keep the main goroutine alive
	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("REST server is running. Press Ctrl+C to disconnect and exit.")

	go wa.RunReconnectLoop(client, logger, reconnectChan, exitChan)

	// Wait for termination signal
	<-exitChan

	fmt.Println("Disconnecting...")
	client.Disconnect()
}
