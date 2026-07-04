package main

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// maps to the same session. It is derived from the tty device behind stdin
// (unique per pts, incl. tmux panes). Falls back to the parent shell pid when
// stdin is not a tty (piped/scripted), and to $LOREWIRE_SESSION_TOKEN when set.
func terminalToken() string {
	if s := os.Getenv(envSessionToken); s != "" {
		return s
	}
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			return fmt.Sprintf("tty-%d", st.Rdev)
		}
	}
	return fmt.Sprintf("ppid-%d", os.Getppid())
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

// ── Config file (.lorewire.jsonc) ───────────────────────────────────────────

const configFileName = ".lorewire.jsonc"

// FileConfig is the parsed project config. Dir is where the file was found (for
// reporting); empty Path means no config file was located.
type FileConfig struct {
	UserID string `json:"userId"`
	Room   string `json:"room"`
	Role   string `json:"role"`
	Path   string `json:"-"`
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
func writeConfig(dir, userID, room, role string) (string, error) {
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
  // lorewire project config. Env vars ($LOREWIRE_USER_ID / _ROOM / _ROLE) and
  // command flags override these values. userId is your stable identity —
  // claim it with `+"`lorewire user create <name>`"+`.
  "userId": %q,
  "room": %q,
  "role": %q
}
`, userID, room, role)
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
