package main

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
)

// ── Identity ids ────────────────────────────────────────────────────────────

// nanoidAlphabet is lowercase alphanumeric — no hyphen (only the `usr_`
// separator uses an underscore), so ids read cleanly.
const nanoidAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// nanoID returns an n-char random id from nanoidAlphabet using crypto/rand.
func nanoID(n int) string {
	buf := make([]byte, n)
	if _, err := crand.Read(buf); err != nil {
		// crypto/rand failing is catastrophic and unrecoverable; surface it.
		panic("lorewire: crypto/rand unavailable: " + err.Error())
	}
	for i, b := range buf {
		buf[i] = nanoidAlphabet[int(b)%len(nanoidAlphabet)]
	}
	return string(buf)
}

// newUserID mints a fresh, stable identity id: the userId prefix + a nanoid.
func newUserID() string { return userIDPrefix + nanoID(userIDNanoID) }

// ── Terminal / session handle ───────────────────────────────────────────────

// terminalToken returns a value that is stable for the life of one terminal and
// distinct across terminals, so every lorewire invocation in the same terminal
// maps to the same session.
//
// It is derived from the CONTROLLING TERMINAL (/dev/tty), NOT stdin. This is the
// crucial detail for agent use: when Claude Code (or any tool) runs `lorewire`
// as a subprocess — a Bash tool-call or a hook — that subprocess's stdin is
// usually redirected (a pipe or /dev/null), so keying off stdin's device, or
// off the parent pid, produces a DIFFERENT id on every call and the terminal
// accumulates a new session each time. The controlling terminal is inherited by
// every process in the session regardless of stdin redirection, so /dev/tty
// yields one stable id for the whole session. Falls back to stdin's tty (plain
// interactive shell) and finally the parent pid; $LOREWIRE_SESSION_TOKEN
// overrides everything.
func terminalToken() string {
	t, _ := terminalTokenSourced()
	return t
}

// terminalTokenSourced is terminalToken plus a short label of WHERE the token
// came from (for the sessions.id_source column and `whoami`, so it's obvious
// and debuggable why a terminal maps to a given session).
func terminalTokenSourced() (token, source string) {
	// 1) Explicit override — any integration can set this directly.
	if s := os.Getenv(envSessionToken); s != "" {
		return s, "env:" + envSessionToken
	}
	// 2) Inside an agent, prefer its stable per-session id: a tty is unreliable
	// there (piped stdin, /dev/tty often "device not configured"), so keying on
	// the agent's session id is what keeps every tool-call and hook of one agent
	// session mapped to a single lorewire session. Tool-agnostic: check any env
	// var names the user configured via LOREWIRE_SESSION_ENV first, then the
	// built-in known-list.
	if id, src := agentSessionSourced(); id != "" {
		return "agent-" + id, src
	}
	// 3) Plain terminal: the controlling tty device (stable per window/pane).
	if dev, ok := ttyRdev("/dev/tty"); ok {
		return fmt.Sprintf("tty-%d", dev), "tty:/dev/tty"
	}
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			return fmt.Sprintf("tty-%d", st.Rdev), "tty:stdin"
		}
	}
	return fmt.Sprintf("ppid-%d", os.Getppid()), "ppid"
}

// agentSessionID returns a stable per-session id from the environment, or "" if
// none is present. It checks, in order: the user-configured var names in
// $LOREWIRE_SESSION_ENV (comma-separated), then the built-in known-list
// (agentSessionEnvs). This is the tool-agnostic seam — new agent runtimes are
// supported by naming their session-id env var, no code change required.
// agentSessionSourced returns the agent session id and the name of the env var
// it came from ("agent:<VAR>"), or ("","") if none is present.
func agentSessionSourced() (id, source string) {
	if names := os.Getenv(envSessionEnv); names != "" {
		for _, k := range strings.Split(names, ",") {
			k = strings.TrimSpace(k)
			if v := os.Getenv(k); v != "" {
				return v, "agent:" + k
			}
		}
	}
	for _, k := range agentSessionEnvs {
		if v := os.Getenv(k); v != "" {
			return v, "agent:" + k
		}
	}
	return "", ""
}

// ttyRdev opens a terminal device and returns its rdev (the underlying pts
// device number, stable across all processes sharing that controlling
// terminal). ok=false when there is no controlling terminal (e.g. a daemon or
// a fully detached process).
func ttyRdev(path string) (uint64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Rdev), true
}

// sessionID builds this terminal's session handle: username + "~" + a short
// stable hash of the terminal token → e.g. "bob~a1f2".
func sessionID(username string) string {
	sum := sha256.Sum256([]byte(terminalToken()))
	return username + sessionSep + hex.EncodeToString(sum[:])[:sessionHashLen]
}

// ttyName best-effort resolves a human-readable terminal name for display
// (stored in sessions.tty). Uses the `tty` command with our stdin inherited;
// returns "" if unavailable. Called only at register time, never per command.
func ttyName() string {
	out, err := exec.Command("tty").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// clientKind is a best-effort label for what launched this session, stored for
// display. Honors $LOREWIRE_CLIENT (hooks can set it), else guesses Claude Code
// from its env marker, else "shell".
func clientKind() string {
	if c := os.Getenv(envClient); c != "" {
		return c
	}
	// CLAUDECODE / CLAUDE_CODE are Claude Code's own env markers (an external
	// contract), kept verbatim per the wire-format exception to the no-bare-
	// strings rule; the value we STORE is our own typed constant.
	if os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE") != "" {
		return clientClaudeCode
	}
	return clientShell
}

// ── System/terminal probes (assembled into a Session by captureContext) ──────

// osUser returns the OS login name (for the sessions.os_user column).
func osUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

// lorewireVersion reports the built version from the module build info (a real
// version for `go install pkg@vX`, "(devel)" for a local build).
func lorewireVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return ""
}

// gitOut runs a git command in the current directory and returns its trimmed
// stdout, or "" on any error (not a repo, git missing, …).
func gitOut(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// goOS / goArch expose the compile-time platform for the session columns.
func goOS() string   { return runtime.GOOS }
func goArch() string { return runtime.GOARCH }

// ── Config file (.lorewire.jsonc) ───────────────────────────────────────────

const configFileName = ".lorewire.jsonc"

// FileConfig is the parsed project config. Dir is where the file was found (for
// reporting); empty Path means no config file was located.
type FileConfig struct {
	UserID string `json:"userId"`
	// Username is a portability hint: it lets `lorewire import` re-create this
	// identity on a fresh machine/empty DB (where the username can't be derived
	// from the userId). The DB remains the source of truth; after a `user
	// rename` this value may be stale, but the userId still resolves correctly.
	Username string `json:"username"`
	Room     string `json:"room"`
	Role     string `json:"role"`
	Path     string `json:"-"`
}

// findConfig walks up from the current directory to the filesystem root,
// returning the path of the nearest .lorewire.jsonc (or "" if none).
func findConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, configFileName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached root
		}
		dir = parent
	}
}

// loadConfig reads and parses the nearest config file. Returns an empty (but
// non-nil) FileConfig when none exists, so callers can always dereference it.
func loadConfig() (*FileConfig, error) {
	path := findConfig()
	if path == "" {
		return &FileConfig{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg FileConfig
	if err := json.Unmarshal(stripJSONC(raw), &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.Path = path
	return &cfg, nil
}

// writeConfig writes a .lorewire.jsonc into dir (default: cwd) with a helpful
// comment header. Overwrites any existing file's managed keys.
func writeConfig(dir, userID, username, room, role string) (string, error) {
	if dir == "" {
		d, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = d
	}
	if room == "" {
		room = defaultRoom
	}
	if role == "" {
		role = roleGuest
	}
	path := filepath.Join(dir, configFileName)
	content := fmt.Sprintf(`{
  // lorewire project config. Env vars ($LOREWIRE_USER_ID / _NAME / _ROOM / _ROLE)
  // and command flags override these values. userId is your stable identity —
  // claim it with `+"`lorewire user create <name>`"+`. On a fresh machine run
  // `+"`lorewire import`"+` to re-create this identity from the fields below.
  "userId": %q,
  "username": %q,
  "room": %q,
  "role": %q
}
`, userID, username, room, role)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// stripJSONC removes // line comments and /* */ block comments from JSONC,
// leaving valid JSON. It is string-aware so comment markers inside string
// literals are preserved.
func stripJSONC(b []byte) []byte {
	var out []byte
	inString := false
	escaped := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, b[i]) // keep the newline
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++ // skip the closing '/'
		default:
			out = append(out, c)
		}
	}
	return out
}
