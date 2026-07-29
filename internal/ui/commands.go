package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// --- messages ---

type authCheckedMsg struct {
	project string
	status  auth.Status
}

type resourcesMsg struct {
	project string
	kind    string
	// token guards against a slow refresh overwriting a newer one.
	token  int
	result gcp.Result
	err    error
}

type loginFinishedMsg struct {
	project string
	err     error
}

type flashMsg struct {
	text  string
	level flashLevel
}

type clearFlashMsg struct{ id int }

type flashLevel int

const (
	flashInfo flashLevel = iota
	flashWarn
	flashError
)

// --- commands ---

// checkAuth verifies a project's credentials in the background.
func checkAuth(mgr *auth.Manager, p config.Project) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return authCheckedMsg{project: p.Name, status: mgr.Check(ctx, p)}
	}
}

// listResources fetches one resource kind for one project.
func listResources(cfg *config.Config, mgr *auth.Manager, p config.Project, lister gcp.Lister, token int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Defaults.ListTimeout.Duration())
		defer cancel()

		result, err := lister.List(ctx, cfg, p, mgr.ClientOptions(p))
		return resourcesMsg{
			project: p.Name,
			kind:    lister.Kind().ID,
			token:   token,
			result:  result,
			err:     err,
		}
	}
}

// login suspends the TUI and hands the terminal to gcloud.
//
// This is the whole reason the auth story is simple: gcloud owns stdin and
// stdout for the duration, so the browser handoff, the IdP password prompt and
// the MFA challenge all happen in gcloud's and the browser's own UI. g9s never
// touches a credential.
func login(mgr *auth.Manager, p config.Project, noBrowser bool) tea.Cmd {
	cmd, err := mgr.LoginCmd(p, noBrowser)
	if err != nil {
		return func() tea.Msg { return loginFinishedMsg{project: p.Name, err: err} }
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return loginFinishedMsg{project: p.Name, err: err}
	})
}

// sshTo suspends the TUI and opens an interactive SSH session to a VM.
func sshTo(mgr *auth.Manager, p config.Project, name, zone string) tea.Cmd {
	cmd := mgr.GcloudCmd(p, "compute", "ssh", name, "--zone="+zone)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return flashMsg{text: fmt.Sprintf("ssh %s: %v", name, err), level: flashError}
		}
		return flashMsg{text: "ssh session closed", level: flashInfo}
	})
}

// openURL launches the system browser without blocking the TUI.
func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		if url == "" {
			return flashMsg{text: "no URL for this resource", level: flashWarn}
		}

		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		// Detach: the browser outliving the TUI is the point.
		cmd.Stdout, cmd.Stderr = nil, nil
		if err := cmd.Start(); err != nil {
			return flashMsg{text: "could not open browser: " + err.Error(), level: flashError}
		}
		go cmd.Wait()
		return flashMsg{text: "opened " + truncate(url, 60), level: flashInfo}
	}
}

// copyToClipboard uses the OSC 52 terminal escape, which works over SSH and
// needs no platform clipboard binary.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", encoded)
		return flashMsg{text: "copied: " + truncate(text, 50), level: flashInfo}
	}
}

// flash shows a transient message and schedules its removal.
func flash(text string, level flashLevel) tea.Cmd {
	return func() tea.Msg { return flashMsg{text: text, level: level} }
}

func clearFlashAfter(id int, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return clearFlashMsg{id: id} })
}

// renderDetail converts a resource's raw API object into YAML, the way
// `gcloud describe` presents it.
//
// The objects are protobuf messages, so they go through protojson first:
// marshalling them directly would leak the generated struct's internal fields.
func renderDetail(r gcp.Resource) string {
	msg, ok := r.Raw.(proto.Message)
	if !ok {
		out, err := yaml.Marshal(r.Raw)
		if err != nil {
			return fmt.Sprintf("%+v", r.Raw)
		}
		return string(out)
	}

	jsonBytes, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(msg)
	if err != nil {
		return "could not render resource: " + err.Error()
	}

	var intermediate any
	if err := json.Unmarshal(jsonBytes, &intermediate); err != nil {
		return string(jsonBytes)
	}

	out, err := yaml.Marshal(intermediate)
	if err != nil {
		return string(jsonBytes)
	}
	return string(out)
}
