package wa

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HaltFilePath is written when WhatsApp has told us to stop. Its presence
// blocks startup until an operator removes it.
//
// The bridge previously treated every disconnect as something to retry
// through. WhatsApp's rejections are not all retryable: a logged-out session,
// a temporary ban, or an outdated client are the server saying "do not come
// back", and whatsmeow deliberately suppresses its own auto-reconnect for
// exactly those cases (see connectionevents.go handleConnectFailure). Retrying
// anyway — especially under a systemd Restart policy — turns a recoverable
// block into a permanent ban, because an unattended process re-registering
// every few seconds looks like abuse no matter how it is spelled.
const HaltFilePath = "store/.halted"

// haltFileMode matches the token file: owner-only, since the reason text can
// name the account state.
const haltFileMode = 0o600

// HaltReason identifies why the bridge stopped. Stored verbatim in the halt
// file so an operator can tell a ban from a stream fight without reading logs.
type HaltReason string

const (
	HaltLoggedOut      HaltReason = "logged-out"
	HaltTemporaryBan   HaltReason = "temporary-ban"
	HaltClientOutdated HaltReason = "client-outdated"
	HaltConnectFailure HaltReason = "connect-failure"
	HaltStreamReplaced HaltReason = "stream-replaced"
)

// remediation maps each reason to the operator action that clears it. Printed
// into the halt file because the right response differs sharply: a temporary
// ban wants patience, an outdated client wants a rebuild, and a replaced
// stream wants the competing WhatsApp Web session closed.
var remediation = map[HaltReason]string{
	HaltLoggedOut: "The session was terminated by WhatsApp or unlinked from the phone. " +
		"Check Linked Devices on the phone. Re-pairing requires WHATSAPP_ALLOW_PAIRING=true " +
		"and someone present to scan the QR code.",
	HaltTemporaryBan: "The account is temporarily banned. Wait for the ban to expire before " +
		"restarting; reconnecting during a ban is what escalates it to a permanent one.",
	HaltClientOutdated: "WhatsApp rejected the client version. Update the whatsmeow dependency " +
		"(go get -u go.mau.fi/whatsmeow) and rebuild before restarting.",
	HaltConnectFailure: "WhatsApp refused the connection for a non-transient reason. " +
		"Do not restart in a loop; investigate the reason code first.",
	HaltStreamReplaced: "Another WhatsApp Web session repeatedly took this device's slot. " +
		"Close the competing session (browser or desktop app) before restarting.",
}

// Halter records a terminal condition once and signals the main goroutine to
// shut down. Every method is safe to call from whatsmeow's event goroutines.
//
// Halting is deliberately one-way and sticky: the first reason wins, later
// events are ignored, and the process is expected to exit rather than resume.
type Halter struct {
	// Dir is the directory the halt file is written to. Empty means the
	// default location relative to the working directory; tests set it.
	Dir string

	once   sync.Once
	halted chan struct{}
	mu     sync.Mutex
	reason HaltReason
	detail string
}

// NewHalter returns a Halter whose Halted channel is ready to select on.
func NewHalter() *Halter {
	return &Halter{halted: make(chan struct{})}
}

// Halted is closed when a terminal condition has been recorded.
func (h *Halter) Halted() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.halted == nil {
		h.halted = make(chan struct{})
	}
	return h.halted
}

// Reason reports the recorded halt reason and detail, if any.
func (h *Halter) Reason() (HaltReason, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reason, h.detail
}

// path resolves the halt file location, honouring Dir when set.
func (h *Halter) path() string {
	if h.Dir == "" {
		return HaltFilePath
	}
	return filepath.Join(h.Dir, filepath.Base(HaltFilePath))
}

// Halt records reason (with a human-readable detail) and closes the Halted
// channel. Only the first call has any effect, so a burst of related events —
// a ConnectFailure followed by a Disconnected, say — produces one shutdown and
// one halt file naming the true first cause.
func (h *Halter) Halt(reason HaltReason, detail string) {
	h.once.Do(func() {
		h.mu.Lock()
		h.reason, h.detail = reason, detail
		if h.halted == nil {
			h.halted = make(chan struct{})
		}
		ch := h.halted
		h.mu.Unlock()

		if err := h.write(reason, detail); err != nil {
			fmt.Printf("⚠ Failed to write halt file: %v\n", err)
		}
		close(ch)
	})
}

// write persists the halt file. Failure to write is reported but never blocks
// the shutdown: stopping matters more than recording why.
func (h *Halter) write(reason HaltReason, detail string) error {
	body := fmt.Sprintf(
		"reason: %s\ntime:   %s\ndetail: %s\n\n%s\n\nDelete this file to allow the bridge to start again.\n",
		reason,
		time.Now().Format(time.RFC3339),
		detail,
		remediation[reason],
	)
	if err := os.MkdirAll(filepath.Dir(h.path()), 0o755); err != nil {
		return fmt.Errorf("create halt dir: %w", err)
	}
	return os.WriteFile(h.path(), []byte(body), haltFileMode)
}

// CheckHaltFile reports the contents of an existing halt file, or "" when the
// bridge is clear to start. Called before any connection attempt so a halted
// deployment never reaches WhatsApp at all.
func CheckHaltFile() (string, error) {
	data, err := os.ReadFile(HaltFilePath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", HaltFilePath, err)
	}
	return string(data), nil
}

// PrintHaltBanner prints a high-visibility notice explaining that the bridge
// refuses to start, mirroring the token banner's format.
func PrintHaltBanner(contents string) {
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════════════════════")
	fmt.Println("  WHATSAPP BRIDGE HALTED — refusing to start")
	fmt.Println("════════════════════════════════════════════════════════════════════")
	fmt.Print(contents)
	fmt.Printf("  Halt file: %s\n", HaltFilePath)
	fmt.Println("════════════════════════════════════════════════════════════════════")
	fmt.Println()
}
