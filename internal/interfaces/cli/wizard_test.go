package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/macdipu/gitswitch/internal/bootstrap"
)

// Setup with no input (a closed pipe, Ctrl+D) stops with a message instead
// of asking for an account forever.
func TestWizardStopsAtEndOfInput(t *testing.T) {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": home + "/.gitconfig", "GITSWITCH_TEST_NO_SYSTEM": "1"} {
		t.Setenv(k, v)
	}
	var out bytes.Buffer
	x := &CLI{app: bootstrap.New(true, &out), in: strings.NewReader(""), out: &out}
	done := make(chan error, 1)
	go func() { done <- x.wizard() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no more input") {
			t.Fatalf("wizard() = %v, want an end-of-input error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wizard kept asking after the input ended")
	}
}
