package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// actionByCommand maps a command word to the action it runs.
//
// A closed list rather than a prefix match: `:st` resolving to stop because it
// happened to be first would be an unforgivable way to lose an instance, and
// every other command in g9s does accept prefixes.
func actionByCommand(verb string) (gcp.Action, bool) {
	switch verb {
	case "start":
		return gcp.ActionStartVM, true
	case "stop":
		return gcp.ActionStopVM, true
	case "reset":
		return gcp.ActionResetVM, true
	}
	return gcp.Action{}, false
}

// pendingAction is an action waiting for confirmation.
//
// Everything needed to execute is captured here at the moment the user asked,
// and nothing is read back out of the table afterwards. The table keeps
// refreshing underneath — rows reorder as fetches land — so resolving the
// target again at execution time would mean confirming one instance and acting
// on whichever one had moved into that position.
type pendingAction struct {
	action  gcp.Action
	target  gcp.ActionTarget
	project config.Project
	// typed is true when the confirmation demands the instance name rather
	// than a keypress.
	typed bool
}

// requestAction opens the confirmation for an action on the selected row.
func (m Model) requestAction(action gcp.Action) (tea.Model, tea.Cmd) {
	r, ok := m.selectedResource()
	if !ok {
		return m, nil
	}
	target, ok := gcp.ResolveActionTarget(r)
	if !ok {
		// Either not an instance, or a name that GCP could not have minted.
		// Refusing beats sending it and finding out.
		return m, flash("this row is not an instance g9s can act on", flashWarn)
	}

	// The action has to still be offered for this row's current state. The
	// keypress and the row it was aimed at can disagree if a refresh landed in
	// between, and "stop" arriving at an instance that already stopped should
	// do nothing rather than something.
	var allowed bool
	for _, a := range gcp.ActionsFor(r) {
		if a.ID == action.ID {
			allowed = true
			break
		}
	}
	if !allowed {
		return m, flash(fmt.Sprintf("%s is %s — %s does not apply",
			target.Name, strings.ToLower(r.Status), strings.ToLower(action.Verb)), flashWarn)
	}

	// Typed confirmation for anything destructive, and for everything in
	// production. A yes/no prompt answered by reflex is not a decision, and
	// production is exactly where reflexes are running.
	typed := action.Destructive || m.active.IsProduction()

	m.pending = &pendingAction{
		action:  action,
		target:  target,
		project: m.active,
		typed:   typed,
	}
	m.confirmInput.SetValue("")
	m.confirmInput.Placeholder = target.Name
	if typed {
		m.confirmInput.Focus()
	}
	m.confirmReturn = m.screen
	m.screen = screenConfirm
	return m, textinput.Blink
}

// handleConfirmKey drives the confirmation screen.
//
// esc is checked before anything else and always cancels. There is no key here
// that both confirms and does something else, and no single letter that
// confirms a destructive action.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		return m.cancelAction()

	case "enter":
		return m.executeAction()
	}

	if m.pending != nil && m.pending.typed {
		var cmd tea.Cmd
		m.confirmInput, cmd = m.confirmInput.Update(msg)
		return m, cmd
	}

	// An untyped confirmation still needs a deliberate key rather than any
	// key: "press enter" is the instruction, and every other key doing
	// nothing is what stops a stray keystroke confirming.
	return m, nil
}

func (m Model) cancelAction() (tea.Model, tea.Cmd) {
	name := ""
	if m.pending != nil {
		name = m.pending.target.Name
	}
	m.pending = nil
	m.confirmInput.Blur()
	m.confirmInput.SetValue("")
	m.screen = m.confirmReturn
	if name == "" {
		return m, nil
	}
	return m, flash("cancelled — "+name+" is unchanged", flashInfo)
}

func (m Model) executeAction() (tea.Model, tea.Cmd) {
	p := m.pending
	if p == nil {
		m.screen = m.confirmReturn
		return m, nil
	}

	if p.typed {
		// Exact match, not a prefix and not case-folded. The whole point is
		// that the name had to be read off the screen and reproduced.
		if strings.TrimSpace(m.confirmInput.Value()) != p.target.Name {
			return m, flash("type the instance name exactly to confirm, or esc to cancel", flashWarn)
		}
	}

	m.pending = nil
	m.confirmInput.Blur()
	m.confirmInput.SetValue("")
	m.screen = m.confirmReturn

	return m, runAction(m.auth, p.project, p.action, p.target)
}

// confirmView renders the confirmation.
//
// It answers the four questions someone should have to answer before changing
// production, in the order they matter: what is about to happen, to which
// exact resource, in which project, and as whom. The account is there because
// a support engineer with several identities configured is the normal case
// here, and "which of my logins is about to do this" is not rhetorical.
func (m Model) confirmView() string {
	p := m.pending
	if p == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")

	if p.project.IsProduction() {
		b.WriteString("  " + badStyle.Render(" PRODUCTION ") + "\n\n")
	}

	fmt.Fprintf(&b, "  %s\n\n", titleStyle.Render(fmt.Sprintf(" %s %s ", p.action.Verb, p.target.Name)))
	b.WriteString(warnStyle.Render("  This "+p.action.Description) + "\n\n")

	rows := [][2]string{
		{"instance", p.target.Name},
		{"zone", p.target.Zone},
		{"project", p.project.Name + "  (" + p.project.ProjectID + ")"},
	}
	if p.project.Account != "" {
		rows = append(rows, [2]string{"as", p.project.Account})
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "  %s  %s\n",
			mutedStyle.Render(pad(row[0], 9)), truncate(row[1], max(10, m.width-16)))
	}
	b.WriteString("\n")

	if p.typed {
		b.WriteString("  Type the instance name to confirm:\n\n")
		m.confirmInput.Width = max(10, min(40, m.width-8))
		b.WriteString("  " + m.confirmInput.View() + "\n")
	} else {
		b.WriteString("  " + mutedStyle.Render("Press enter to confirm, esc to cancel.") + "\n")
	}

	return b.String()
}
