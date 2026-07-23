package wa_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-mcp/bridge/internal/wa"
)

// newHalter returns a Halter writing into a temp dir, plus that dir.
func newHalter(t *testing.T) (*wa.Halter, string) {
	t.Helper()
	dir := t.TempDir()
	h := wa.NewHalter()
	h.Dir = dir
	return h, dir
}

func haltFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".halted"))
	if err != nil {
		t.Fatalf("read halt file: %v", err)
	}
	return string(data)
}

func TestHaltWritesFileAndSignals(t *testing.T) {
	h, dir := newHalter(t)

	h.Halt(wa.HaltTemporaryBan, "code=block expire=6h0m0s")

	select {
	case <-h.Halted():
	case <-time.After(time.Second):
		t.Fatal("Halted() was not signalled")
	}

	body := haltFile(t, dir)
	for _, want := range []string{"temporary-ban", "expire=6h0m0s"} {
		if !strings.Contains(body, want) {
			t.Errorf("halt file missing %q:\n%s", want, body)
		}
	}
	// The file must say what to do about it, not just what happened —
	// the response to a ban is the opposite of the response to a stale tab.
	if !strings.Contains(body, "Wait for the ban to expire") {
		t.Errorf("halt file carries no remediation:\n%s", body)
	}

	gotReason, gotDetail := h.Reason()
	if gotReason != wa.HaltTemporaryBan {
		t.Errorf("Reason() = %q, want %q", gotReason, wa.HaltTemporaryBan)
	}
	if gotDetail == "" {
		t.Error("Reason() returned an empty detail")
	}
}

func TestHaltIsStickyToFirstReason(t *testing.T) {
	h, dir := newHalter(t)

	// A burst of related events is normal: a ConnectFailure often arrives
	// alongside a Disconnected. Only the true first cause should be recorded.
	h.Halt(wa.HaltLoggedOut, "first")
	h.Halt(wa.HaltStreamReplaced, "second")
	h.Halt(wa.HaltClientOutdated, "third")

	if reason, detail := h.Reason(); reason != wa.HaltLoggedOut || detail != "first" {
		t.Fatalf("Reason() = (%q, %q), want (logged-out, first)", reason, detail)
	}
	if body := haltFile(t, dir); !strings.Contains(body, "logged-out") || strings.Contains(body, "second") {
		t.Fatalf("halt file was overwritten by a later event:\n%s", body)
	}
}

func TestHaltedChannelIsSafeBeforeHalt(t *testing.T) {
	h, _ := newHalter(t)

	select {
	case <-h.Halted():
		t.Fatal("Halted() fired before any halt")
	default:
	}
}

func TestCheckHaltFileReportsNothingWhenAbsent(t *testing.T) {
	// CheckHaltFile reads the production-relative path; running from a temp
	// working directory keeps the test off any real store/ directory.
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	contents, err := wa.CheckHaltFile()
	if err != nil {
		t.Fatalf("CheckHaltFile: %v", err)
	}
	if contents != "" {
		t.Fatalf("CheckHaltFile = %q, want empty", contents)
	}
}

func TestCheckHaltFileReportsContentsWhenPresent(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	if err := os.MkdirAll(filepath.Dir(wa.HaltFilePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(wa.HaltFilePath, []byte("reason: logged-out\n"), 0o600); err != nil {
		t.Fatalf("write halt file: %v", err)
	}

	contents, err := wa.CheckHaltFile()
	if err != nil {
		t.Fatalf("CheckHaltFile: %v", err)
	}
	if !strings.Contains(contents, "logged-out") {
		t.Fatalf("CheckHaltFile = %q, want the stored reason", contents)
	}
}

func chdir(t *testing.T, dir string) func() {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return func() { _ = os.Chdir(old) }
}
