// Package prompt asks which account to use: on the terminal, or with a
// small native popup when git runs without one. Each popup is a short-lived
// process started only for the question.
package prompt

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

// Terminal asks on the user's terminal. git owns stdin/stdout (the SSH
// protocol runs over them), so we talk to the terminal device directly.
func Terminal(t *terminal.Terminal, label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision {
	w := t.Out
	fmt.Fprintf(w, "\nGit account for %s?\n", label)
	fmt.Fprintf(w, "  [Enter] %s   (default)\n", def.ID)
	for i, a := range cands {
		fmt.Fprintf(w, "  %d  %-16s %s\n", i+1, a.ID, a.Email)
	}
	fmt.Fprintf(w, "Type a number for this time, or add ! to always use it for this repo (e.g. 2!): ")

	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(t.In).ReadString('\n')
		line <- s
	}()
	var ans string
	select {
	case ans = <-line:
	case <-time.After(time.Duration(timeout) * time.Second):
		fmt.Fprintf(w, "\n(no answer in %ds, using %s)\n", timeout, def.ID)
		return &resolve.Decision{Account: def, Reason: resolve.Default}
	}
	ans = strings.TrimSpace(ans)
	save := strings.HasSuffix(ans, "!")
	ans = strings.TrimSuffix(ans, "!")
	if ans == "" {
		return &resolve.Decision{Account: def, Reason: resolve.Prompt, Save: save}
	}
	if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(cands) {
		return &resolve.Decision{Account: &cands[n-1], Reason: resolve.Prompt, Save: save}
	}
	for i := range cands {
		if strings.EqualFold(cands[i].ID, ans) {
			return &resolve.Decision{Account: &cands[i], Reason: resolve.Prompt, Save: save}
		}
	}
	fmt.Fprintf(w, "Not a choice, using %s.\n", def.ID)
	return &resolve.Decision{Account: def, Reason: resolve.Default}
}

// Dialog shows a small native dialog when git runs with no terminal
// (IDE source-control panels, auto-fetch). It starts a short-lived process
// only for the question, so nothing runs in the background.
func Dialog(label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision {
	if process.NoSystemChanges() {
		return nil // tests: never open a window
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	var choice string
	switch runtime.GOOS {
	case "darwin":
		choice = dialogMac(ctx, label, cands, def)
	case "windows":
		choice = dialogWindows(ctx, label, cands, def, timeout)
	default:
		choice = dialogLinux(ctx, label, cands, def, timeout)
	}
	if choice == "" {
		return nil
	}
	id, mode, _ := strings.Cut(choice, "|")
	for i := range cands {
		if cands[i].ID == id {
			return &resolve.Decision{Account: &cands[i], Reason: resolve.Dialog, Save: mode == "always"}
		}
	}
	return nil
}

func dialogItems(cands []account.Account) (items []string, values []string) {
	for _, a := range cands {
		items = append(items, a.ID+" (this time)")
		values = append(values, a.ID+"|once")
	}
	for _, a := range cands {
		items = append(items, a.ID+" (always for this repo)")
		values = append(values, a.ID+"|always")
	}
	return
}

func pick(items, values []string, chosen string) string {
	chosen = strings.TrimSpace(chosen)
	for i, it := range items {
		if it == chosen {
			return values[i]
		}
	}
	return ""
}

func asQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func dialogMac(ctx context.Context, label string, cands []account.Account, def *account.Account) string {
	items, values := dialogItems(cands)
	var q []string
	for _, it := range items {
		q = append(q, asQuote(it))
	}
	script := fmt.Sprintf(`choose from list {%s} with title "GitSwitch" with prompt %s default items {%s} OK button name "Use"`,
		strings.Join(q, ","), asQuote("Git account for "+label+"?"), asQuote(def.ID+" (this time)"))
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return ""
	}
	return pick(items, values, string(out))
}

func dialogLinux(ctx context.Context, label string, cands []account.Account, def *account.Account, timeout int) string {
	items, values := dialogItems(cands)
	if p, err := exec.LookPath("zenity"); err == nil {
		args := []string{"--list", "--title=GitSwitch", "--text=Git account for " + label + "?",
			"--column=Account", "--hide-header", "--height=320", "--timeout=" + strconv.Itoa(timeout)}
		args = append(args, items...)
		out, err := exec.CommandContext(ctx, p, args...).Output()
		if err != nil {
			return ""
		}
		return pick(items, values, string(out))
	}
	if p, err := exec.LookPath("kdialog"); err == nil {
		args := []string{"--title", "GitSwitch", "--menu", "Git account for " + label + "?"}
		for i := range items {
			args = append(args, values[i], items[i])
		}
		out, err := exec.CommandContext(ctx, p, args...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return ""
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func dialogWindows(ctx context.Context, label string, cands []account.Account, def *account.Account, timeout int) string {
	var ids []string
	for _, a := range cands {
		ids = append(ids, psQuote(a.ID))
	}
	script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$f = New-Object Windows.Forms.Form
$f.Text = 'GitSwitch'; $f.TopMost = $true; $f.StartPosition = 'CenterScreen'
$f.FormBorderStyle = 'FixedDialog'; $f.MaximizeBox = $false; $f.MinimizeBox = $false
$f.ClientSize = New-Object Drawing.Size(340, 260)
$l = New-Object Windows.Forms.Label; $l.Text = ` + psQuote("Git account for "+label+"?") + `
$l.SetBounds(12, 12, 316, 36)
$b = New-Object Windows.Forms.ListBox; $b.SetBounds(12, 52, 316, 120)
foreach ($i in @(` + strings.Join(ids, ",") + `)) { [void]$b.Items.Add($i) }
$b.SelectedItem = ` + psQuote(def.ID) + `
$c = New-Object Windows.Forms.CheckBox; $c.Text = 'Always use it for this repo'; $c.SetBounds(12, 180, 316, 24)
$ok = New-Object Windows.Forms.Button; $ok.Text = 'Use'; $ok.SetBounds(248, 216, 80, 30)
$ok.DialogResult = [Windows.Forms.DialogResult]::OK; $f.AcceptButton = $ok
$b.Add_DoubleClick({ $f.DialogResult = [Windows.Forms.DialogResult]::OK; $f.Close() })
$t = New-Object Windows.Forms.Timer; $t.Interval = ` + strconv.Itoa(timeout*1000) + `
$t.Add_Tick({ $f.Close() }); $t.Start()
$f.Controls.AddRange(@($l, $b, $c, $ok))
if ($f.ShowDialog() -eq [Windows.Forms.DialogResult]::OK -and $b.SelectedItem) {
  $m = 'once'; if ($c.Checked) { $m = 'always' }
  [Console]::Out.Write($b.SelectedItem + '|' + $m)
}`
	u := utf16.Encode([]rune(script))
	buf := make([]byte, len(u)*2)
	for i, r := range u {
		buf[i*2] = byte(r)
		buf[i*2+1] = byte(r >> 8)
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(buf))
	process.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
