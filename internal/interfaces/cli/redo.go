package cli

import (
	"encoding/json"
	"errors"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// cmdRedo does again the change the last undo took back, after saying what
// it does. --show only says it, --json as JSON.
func (x *CLI) cmdRedo(args []string) error {
	var yes, show, asJSON bool
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			yes = true
		case "--show":
			show = true
		case "--json":
			asJSON = true
		default:
			return usageErr("gitswitch redo [-y] [--show [--json]]")
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	st, err := x.app.Undo.NextRedo(gd)
	if show && asJSON {
		if errors.Is(err, undo.ErrNothingToRedo) {
			return json.NewEncoder(x.out).Encode(nil)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(x.out).Encode(st)
	}
	if err != nil {
		return err
	}
	x.sayf("Undone: %s (%s)\n", history.CleanText(st.Label), ago(st.Time))
	if st.Blocked != "" {
		return errors.New("it can't be redone: " + st.Blocked)
	}
	x.say("Redo:", st.Effect)
	if show {
		return nil
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Redo it?"); err != nil {
			return err
		}
	}
	if _, err := x.app.Undo.Redo(gd); err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.say("Redone. `gitswitch undo` takes it back again.")
	}
	return nil
}
