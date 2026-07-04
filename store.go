package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// defaultRoom is the room every session lands in when none is specified. Its
// existence is what makes rooms optional: plain register/send/recv all operate
// in "main" so the tool works with zero room ceremony.
const defaultRoom = "main"

// roleGuest is the role assigned when a session joins without one.
const roleGuest = "guest"

// Message kinds. Normal chatter is msgKind; the request/grant/deny trio powers
// the "ask a role-holder for a secret" flow, and secretKind payloads are
// consume-once + hard-deleted on read.
const (
	msgKind     = "msg"
	requestKind = "request"
	grantKind   = "grant"
	denyKind    = "deny"
	secretKind  = "secret"
)

const timeFmt = time.RFC3339Nano

// ── Domain types ────────────────────────────────────────────────────────────

// User is a stable identity: a userId that never changes and a unique username
// that can be renamed. One user owns many sessions (one per terminal).
type User struct {
	ID        string
	Username  string
	CreatedAt time.Time
	Sessions  int // populated by ListUsers
}

// Session is one open terminal, owned by a user. session_id is username~<tty>.
type Session struct {
	ID       string
	OwnerID  string
	Username string // resolved from users at read time
	// Terminal / process context, captured at register (and refreshed on use):
	CWD         string
	TTY         string
	PID         int    // parent shell pid
	Host        string // hostname
	Client      string // claude-code | shell | … (best-effort)
	OSUser      string // OS login that owns the terminal
	OS          string // runtime.GOOS
	Arch        string // runtime.GOARCH
	Shell       string // basename of $SHELL
	TermProgram string // $TERM_PROGRAM (iTerm.app, vscode, Apple_Terminal, …)
	SSH         bool   // running over SSH ($SSH_CONNECTION/$SSH_TTY)
	Tmux        bool   // inside tmux ($TMUX)
	GitBranch   string // current branch of the cwd repo (if any)
	GitRepo     string // basename of the cwd repo root (if any)
	Version     string // lorewire binary version
	IDSource    string // where the session id was derived from (agent:VAR / tty / ppid / env:VAR)
	CreatedAt   time.Time
	LastSeen    time.Time
}

// Member is a session's membership of one room, with its role there. owner_id is
// denormalized for grouping without a join (safe: a session's owner is fixed).
type Member struct {
	Room      string
	SessionID string
	OwnerID   string
	Username  string
	Role      string
	JoinedAt  time.Time
}

// Room is a named channel. OwnerID is the user that created it ("" for the
// system default room).
type Room struct {
	Name      string
	OwnerID   string
	Owner     string // resolved username
	CreatedAt time.Time
	Members   int
}

// Message is one delivered row. Fan-out (broadcast / @role / username) resolves
// at send time to concrete recipient session ids, so recv stays a simple
// "unread rows for my session" query.
type Message struct {
	ID        int64
	Room      string
	From      string // sender session id (delivery address)
	To        string // recipient session id
	FromOwner string // sender userId (identity — for user-level history)
	ToOwner   string // recipient userId
	Kind      string
	Body      string
	RefID     *int64
	CreatedAt time.Time
	ReadAt    *time.Time
}

// recipient is one resolved delivery target: the session id it lands on plus the
// userId that owns it, so a message records the recipient's identity (not just
// its transient session).
type recipient struct {
	ID    string // session id
	Owner string // userId that owns the session ("" if the session is unknown)
}

// Store wraps the SQLite handle.
type Store struct{ db *sql.DB }

// withRetry re-runs a write that failed because another session held the SQLite
// lock. Sessions are separate processes, so busy_timeout is the cross-process
// backpressure; this loop is belt-and-suspenders on top of it.
func withRetry(fn func() error) error {
	const attempts = 15
	var err error
	for i := range attempts {
		err = fn()
		if err == nil {
			return nil
		}
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "locked") && !strings.Contains(msg, "busy") {
			return err
		}
		time.Sleep(time.Duration(20*(i+1)) * time.Millisecond)
	}
	return err
}

// ── Open / migrate ──────────────────────────────────────────────────────────

func dbPath() (string, error) {
	if p := os.Getenv(envDB); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lorewire", "lorewire.db"), nil
}

func dsnFor(path string) string {
	// busy_timeout: wait for the write lock under cross-process contention.
	// journal_mode=WAL: readers proceed during a writer.
	// synchronous=NORMAL: safe under WAL (no corruption on crash — at most the
	// last committed transaction is lost on power failure) and markedly faster
	// for the many small writes a message bus does.
	return fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
}

func openStore() (*Store, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return nil, err
	}
	// A pre-identity database (has messages but no users table) is not
	// forward-compatible with the session-id schema. Rather than wipe it
	// silently, move it aside and start fresh — the old file is preserved.
	incompat, err := legacyIncompatible(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if incompat {
		db.Close()
		if err := backupLegacyDB(path); err != nil {
			return nil, err
		}
		if db, err = sql.Open("sqlite", dsnFor(path)); err != nil {
			return nil, err
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func legacyIncompatible(db *sql.DB) (bool, error) {
	hasMessages, err := tableExists(db, "messages")
	if err != nil {
		return false, err
	}
	if !hasMessages {
		return false, nil // fresh DB
	}
	hasUsers, err := tableExists(db, "users")
	if err != nil {
		return false, err
	}
	return !hasUsers, nil // messages without users == legacy shape
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func backupLegacyDB(path string) error {
	stamp := time.Now().Format("20060102-150405")
	bak := path + ".bak-" + stamp
	if err := os.Rename(path, bak); err != nil {
		return fmt.Errorf("backing up legacy DB: %w", err)
	}
	// Sidecar WAL/SHM files, if present, are best-effort.
	os.Rename(path+"-wal", bak+"-wal")
	os.Rename(path+"-shm", bak+"-shm")
	fmt.Fprintf(os.Stderr,
		"lorewire: migrated to a new schema; previous database saved to %s (re-register your sessions)\n", bak)
	return nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS users (
	user_id    TEXT PRIMARY KEY,
	username   TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rooms (
	name       TEXT PRIMARY KEY,
	owner_id   TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	session_id   TEXT PRIMARY KEY,
	owner_id     TEXT NOT NULL,
	cwd          TEXT,
	tty          TEXT,
	pid          INTEGER,
	host         TEXT,
	client       TEXT,
	os_user      TEXT,
	os           TEXT,
	arch         TEXT,
	shell        TEXT,
	term_program TEXT,
	ssh          INTEGER,
	tmux         INTEGER,
	git_branch   TEXT,
	git_repo     TEXT,
	version      TEXT,
	id_source    TEXT,
	meta         TEXT,
	created_at   TEXT NOT NULL,
	last_seen    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
	room       TEXT NOT NULL,
	session_id TEXT NOT NULL,
	owner_id   TEXT NOT NULL,
	role       TEXT NOT NULL,
	joined_at  TEXT NOT NULL,
	PRIMARY KEY (room, session_id)
);
CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	room       TEXT NOT NULL DEFAULT 'main',
	from_id    TEXT NOT NULL,      -- sender session id (the delivery address)
	to_id      TEXT NOT NULL,      -- recipient session id
	from_owner TEXT,               -- sender userId (the identity — for user-level history)
	to_owner   TEXT,               -- recipient userId
	kind       TEXT NOT NULL DEFAULT 'msg',
	body       TEXT NOT NULL,
	ref_id     INTEGER,
	created_at TEXT NOT NULL,
	read_at    TEXT
);
CREATE INDEX IF NOT EXISTS idx_messages_to_unread ON messages(to_id, read_at);
CREATE INDEX IF NOT EXISTS idx_members_room ON members(room);
CREATE INDEX IF NOT EXISTS idx_sessions_owner ON sessions(owner_id);
`)
	// NOTE: indexes on the owner columns are created AFTER the additive migration
	// below — on an existing DB those columns don't exist until the ALTER runs.
	if err != nil {
		return err
	}
	// Additive migration: add newer session-context columns to an existing DB
	// only if missing (SQLite ALTER ADD COLUMN is safe/cheap), so upgrading keeps
	// data. Fresh DBs already have them from the CREATE above.
	for col, decl := range map[string]string{
		"os_user":      "os_user TEXT",
		"os":           "os TEXT",
		"arch":         "arch TEXT",
		"shell":        "shell TEXT",
		"term_program": "term_program TEXT",
		"ssh":          "ssh INTEGER",
		"tmux":         "tmux INTEGER",
		"git_branch":   "git_branch TEXT",
		"git_repo":     "git_repo TEXT",
		"version":      "version TEXT",
		"id_source":    "id_source TEXT",
	} {
		ok, err := s.columnExists("sessions", col)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := s.db.Exec("ALTER TABLE sessions ADD COLUMN " + decl); err != nil {
				return err
			}
		}
	}
	// messages owner columns (identity-level history), added + backfilled once.
	for col := range map[string]struct{}{"from_owner": {}, "to_owner": {}} {
		ok, err := s.columnExists("messages", col)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := s.db.Exec("ALTER TABLE messages ADD COLUMN " + col + " TEXT"); err != nil {
				return err
			}
		}
	}
	// Backfill owners for pre-existing rows by mapping the "username~" prefix of
	// the session id to a userId. Runs only against still-NULL rows, so it's a
	// one-time, idempotent fill; renamed/removed users just stay NULL.
	for _, pair := range [][2]string{{"from_owner", "from_id"}, {"to_owner", "to_id"}} {
		if _, err := s.db.Exec(fmt.Sprintf(
			`UPDATE messages SET %[1]s = (
			   SELECT u.user_id FROM users u
			   WHERE u.username = substr(%[2]s, 1, instr(%[2]s, '~') - 1)
			 ) WHERE %[1]s IS NULL AND instr(%[2]s, '~') > 0`, pair[0], pair[1])); err != nil {
			return err
		}
	}
	// Now that the owner columns are guaranteed to exist, index them.
	if _, err := s.db.Exec(`
CREATE INDEX IF NOT EXISTS idx_messages_to_owner ON messages(to_owner, read_at);
CREATE INDEX IF NOT EXISTS idx_messages_from_owner ON messages(from_owner);`); err != nil {
		return err
	}
	now := time.Now().UTC().Format(timeFmt)
	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO rooms (name, owner_id, created_at) VALUES (?, '', ?)`, defaultRoom, now)
	return err
}

// columnExists reports whether a table already has a column (for additive
// migrations that must not re-add an existing column).
func (s *Store) columnExists(table, col string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) Close() error { return s.db.Close() }

// ── Users ───────────────────────────────────────────────────────────────────

// CreateUser claims a username. If userID is empty a fresh one is minted; a
// non-empty userID re-imports an existing identity (e.g. on a new machine).
// Fails if the username is already taken by a different userId.
func (s *Store) CreateUser(username, userID string) (string, error) {
	if username == "" || strings.ContainsAny(username, sessionSep+" ") {
		return "", fmt.Errorf("invalid username %q (no spaces or %q, non-empty)", username, sessionSep)
	}
	if userID == "" {
		userID = newUserID()
	}
	now := time.Now().UTC().Format(timeFmt)
	err := withRetry(func() error {
		// If this exact user already exists, treat as idempotent success.
		var existingID string
		switch err := s.db.QueryRow(`SELECT user_id FROM users WHERE username = ?`, username).Scan(&existingID); err {
		case nil:
			if existingID != userID {
				return fmt.Errorf("username %q is already taken by %s", username, existingID)
			}
			return nil
		case sql.ErrNoRows:
			// fall through to insert
		default:
			return err
		}
		// A supplied userId that already maps to a different username is an
		// aliasing attempt — one userId owns exactly one username.
		if name, ok, err := s.UserByID(userID); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("userId %s already belongs to username %q", userID, name)
		}
		if _, err := s.db.Exec(
			`INSERT INTO users (user_id, username, created_at) VALUES (?, ?, ?)`,
			userID, username, now); err != nil {
			return err
		}
		return nil
	})
	return userID, err
}

// EnsureUser is the quick-mode helper: return the userId for username, creating
// it if the name is free. Used when no config exists but $LOREWIRE_NAME is set.
func (s *Store) EnsureUser(username string) (string, error) {
	if id, ok, err := s.UserByName(username); err != nil {
		return "", err
	} else if ok {
		return id, nil
	}
	return s.CreateUser(username, "")
}

func (s *Store) UserByName(username string) (string, bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT user_id FROM users WHERE username = ?`, username).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return id, err == nil, err
}

func (s *Store) UserByID(userID string) (string, bool, error) {
	var name string
	err := s.db.QueryRow(`SELECT username FROM users WHERE user_id = ?`, userID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return name, err == nil, err
}

// RenameUser changes a username. Because a session_id embeds the username
// (`bob~a1f`) for readability, the rename cascades the id prefix across the
// user's sessions/members and the messages referencing them, so a live terminal
// keeps its session after the rename. The userId (what configs reference) never
// changes, so committed .lorewire.jsonc files are unaffected. Rare operation;
// the extra UPDATEs are bounded to one user's rows.
func (s *Store) RenameUser(oldName, newName string) error {
	if strings.ContainsAny(newName, sessionSep) || newName == "" {
		return fmt.Errorf("invalid username %q (no %q, non-empty)", newName, sessionSep)
	}
	return withRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		var userID string
		if err := tx.QueryRow(`SELECT user_id FROM users WHERE username = ?`, oldName).Scan(&userID); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("no user named %q", oldName)
			}
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET username = ? WHERE user_id = ?`, newName, userID); err != nil {
			return err
		}
		// Re-prefix session ids: newName || the "~hash" tail of the old id.
		tail := len(oldName) + 1 // substr() is 1-based; skip "oldName" chars
		if _, err := tx.Exec(
			`UPDATE sessions SET session_id = ? || substr(session_id, ?) WHERE owner_id = ?`,
			newName, tail, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE members SET session_id = ? || substr(session_id, ?) WHERE owner_id = ?`,
			newName, tail, userID); err != nil {
			return err
		}
		// Messages reference session ids by string; the "old~" prefix is unique
		// (usernames can't contain sessionSep), so LIKE 'old~%' is safe.
		like := oldName + sessionSep + "%"
		if _, err := tx.Exec(
			`UPDATE messages SET from_id = ? || substr(from_id, ?) WHERE from_id LIKE ?`,
			newName, tail, like); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE messages SET to_id = ? || substr(to_id, ?) WHERE to_id LIKE ?`,
			newName, tail, like); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`
SELECT u.user_id, u.username, u.created_at, COUNT(se.session_id)
FROM users u LEFT JOIN sessions se ON se.owner_id = u.user_id
GROUP BY u.user_id, u.username, u.created_at ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &created, &u.Sessions); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(timeFmt, created)
		out = append(out, u)
	}
	return out, rows.Err()
}

// ── Sessions ────────────────────────────────────────────────────────────────

// RegisterSession upserts this terminal's session row and refreshes last_seen.
func (s *Store) RegisterSession(sess Session) error {
	now := time.Now().UTC().Format(timeFmt)
	return withRetry(func() error {
		// On conflict we keep any non-empty prior value for the subprocess-
		// derived columns (tty, git_*), because light commands (send/recv) don't
		// capture those and would otherwise wipe what `register` recorded. The
		// cheap always-captured columns just overwrite with the fresh value.
		_, err := s.db.Exec(`
INSERT INTO sessions (session_id, owner_id, cwd, tty, pid, host, client,
  os_user, os, arch, shell, term_program, ssh, tmux, git_branch, git_repo, version, id_source,
  created_at, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?,  ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,  ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  owner_id=excluded.owner_id,
  cwd=COALESCE(NULLIF(excluded.cwd,''), sessions.cwd),
  tty=COALESCE(NULLIF(excluded.tty,''), sessions.tty),
  pid=CASE WHEN excluded.pid>0 THEN excluded.pid ELSE sessions.pid END,
  host=COALESCE(NULLIF(excluded.host,''), sessions.host),
  client=COALESCE(NULLIF(excluded.client,''), sessions.client),
  os_user=COALESCE(NULLIF(excluded.os_user,''), sessions.os_user),
  os=COALESCE(NULLIF(excluded.os,''), sessions.os),
  arch=COALESCE(NULLIF(excluded.arch,''), sessions.arch),
  shell=COALESCE(NULLIF(excluded.shell,''), sessions.shell),
  term_program=COALESCE(NULLIF(excluded.term_program,''), sessions.term_program),
  ssh=excluded.ssh,
  tmux=excluded.tmux,
  git_branch=COALESCE(NULLIF(excluded.git_branch,''), sessions.git_branch),
  git_repo=COALESCE(NULLIF(excluded.git_repo,''), sessions.git_repo),
  version=COALESCE(NULLIF(excluded.version,''), sessions.version),
  id_source=COALESCE(NULLIF(excluded.id_source,''), sessions.id_source),
  last_seen=excluded.last_seen`,
			sess.ID, sess.OwnerID, sess.CWD, sess.TTY, sess.PID, sess.Host, sess.Client,
			sess.OSUser, sess.OS, sess.Arch, sess.Shell, sess.TermProgram, boolToInt(sess.SSH), boolToInt(sess.Tmux),
			sess.GitBranch, sess.GitRepo, sess.Version, sess.IDSource, now, now)
		return err
	})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sessionSelectCols is the column list (with the users join alias) shared by
// SessionByID and Sessions, so the SELECT and the scanner never drift.
const sessionSelectCols = `se.session_id, se.owner_id, u.username, se.cwd, se.tty, se.pid, se.host, se.client,
  se.os_user, se.os, se.arch, se.shell, se.term_program, se.ssh, se.tmux, se.git_branch, se.git_repo, se.version, se.id_source,
  se.created_at, se.last_seen`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// scanSession reads one sessions+users row (selected via sessionSelectCols).
func scanSession(sc rowScanner) (Session, error) {
	var se Session
	var cwd, tty, host, client, osu, oss, arch, shell, tp, gb, gr, ver, idsrc sql.NullString
	var pid, ssh, tmux sql.NullInt64
	var created, seen string
	if err := sc.Scan(&se.ID, &se.OwnerID, &se.Username, &cwd, &tty, &pid, &host, &client,
		&osu, &oss, &arch, &shell, &tp, &ssh, &tmux, &gb, &gr, &ver, &idsrc, &created, &seen); err != nil {
		return se, err
	}
	se.CWD, se.TTY, se.Host, se.Client = cwd.String, tty.String, host.String, client.String
	se.OSUser, se.OS, se.Arch, se.Shell, se.TermProgram = osu.String, oss.String, arch.String, shell.String, tp.String
	se.GitBranch, se.GitRepo, se.Version, se.IDSource = gb.String, gr.String, ver.String, idsrc.String
	se.PID = int(pid.Int64)
	se.SSH = ssh.Int64 != 0
	se.Tmux = tmux.Int64 != 0
	se.CreatedAt, _ = time.Parse(timeFmt, created)
	se.LastSeen, _ = time.Parse(timeFmt, seen)
	return se, nil
}

// Touch refreshes last_seen for a session (heartbeat on send/recv).
func (s *Store) Touch(sessionID string) {
	now := time.Now().UTC().Format(timeFmt)
	s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE session_id = ?`, now, sessionID)
}

// SessionByID returns one session's stored row (with its owner username), or
// ok=false if this terminal hasn't registered a session yet. Used by `whoami`
// to show the current terminal's full detail.
func (s *Store) SessionByID(id string) (*Session, bool, error) {
	row := s.db.QueryRow(
		`SELECT `+sessionSelectCols+`
FROM sessions se JOIN users u ON u.user_id = se.owner_id
WHERE se.session_id = ?`, id)
	se, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &se, true, nil
}

// MembershipsForSession returns every room this session belongs to, with its
// role in each — the "member of" view for `whoami`.
func (s *Store) MembershipsForSession(sessionID string) ([]Member, error) {
	rows, err := s.db.Query(`
SELECT m.room, m.session_id, m.owner_id, u.username, m.role, m.joined_at
FROM members m JOIN users u ON u.user_id = m.owner_id
WHERE m.session_id = ? ORDER BY m.room`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var joined string
		if err := rows.Scan(&m.Room, &m.SessionID, &m.OwnerID, &m.Username, &m.Role, &joined); err != nil {
			return nil, err
		}
		m.JoinedAt, _ = time.Parse(timeFmt, joined)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Sessions lists live sessions. ownerID "" returns every session (the global
// view); a non-empty ownerID scopes to one user's sessions (the `--me` view).
func (s *Store) Sessions(ownerID string) ([]Session, error) {
	q := `SELECT ` + sessionSelectCols + `
FROM sessions se JOIN users u ON u.user_id = se.owner_id`
	var args []any
	if ownerID != "" {
		q += ` WHERE se.owner_id = ?`
		args = append(args, ownerID)
	}
	q += ` ORDER BY u.username, se.created_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		se, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, se)
	}
	return out, rows.Err()
}

// UserSessions returns a user's session ids: the currently-live ones (full rows)
// plus historical session ids that only survive in message history (a session
// that has since left/been pruned but sent or received messages). Historical
// excludes any id that is still live. This is how "all sessions a user ever had"
// is answered — identity outlives any single session.
func (s *Store) UserSessions(ownerID string) (live []Session, historical []string, err error) {
	live, err = s.Sessions(ownerID)
	if err != nil {
		return nil, nil, err
	}
	liveSet := make(map[string]struct{}, len(live))
	for _, se := range live {
		liveSet[se.ID] = struct{}{}
	}
	rows, err := s.db.Query(`
SELECT sid FROM (
  SELECT from_id AS sid FROM messages WHERE from_owner = ?
  UNION
  SELECT to_id AS sid FROM messages WHERE to_owner = ?
) ORDER BY sid`, ownerID, ownerID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, nil, err
		}
		if _, isLive := liveSet[sid]; !isLive {
			historical = append(historical, sid)
		}
	}
	return live, historical, rows.Err()
}

// ── Rooms & membership ──────────────────────────────────────────────────────

// Join adds a session to a room with a role, creating the room if new (the
// joiner's owner becomes room owner). Rejoining updates the role. Returns
// whether the room was freshly created and its owner username.
func (s *Store) Join(room, sessionID, ownerID, role string) (created bool, owner string, err error) {
	if role == "" {
		role = roleGuest
	}
	now := time.Now().UTC().Format(timeFmt)
	err = withRetry(func() error {
		created, owner = false, ""
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO rooms (name, owner_id, created_at) VALUES (?, ?, ?)`, room, ownerID, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			created = true
		}
		var ownerID2 string
		if err := tx.QueryRow(`SELECT owner_id FROM rooms WHERE name = ?`, room).Scan(&ownerID2); err != nil {
			return err
		}
		if _, err := tx.Exec(`
INSERT INTO members (room, session_id, owner_id, role, joined_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(room, session_id) DO UPDATE SET role = excluded.role`,
			room, sessionID, ownerID, role, now); err != nil {
			return err
		}
		if ownerID2 != "" {
			tx.QueryRow(`SELECT username FROM users WHERE user_id = ?`, ownerID2).Scan(&owner)
		}
		return tx.Commit()
	})
	return created, owner, err
}

// EnsureMember makes a session a member of a room WITHOUT changing its role if
// it is already a member. Used by incidental commands (send/recv/watch/inbox)
// so that merely messaging or listening never downgrades a role that an
// explicit `join`/`role set` established. Creates the room (owned by this user)
// if it doesn't exist yet. Contrast Store.Join, which upserts the role because
// register/join are deliberate "set my role here" actions.
func (s *Store) EnsureMember(room, sessionID, ownerID, role string) error {
	if role == "" {
		role = roleGuest
	}
	now := time.Now().UTC().Format(timeFmt)
	return withRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO rooms (name, owner_id, created_at) VALUES (?, ?, ?)`,
			room, ownerID, now); err != nil {
			return err
		}
		// INSERT OR IGNORE: if the session is already a member, this is a no-op
		// and the existing role is preserved.
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO members (room, session_id, owner_id, role, joined_at) VALUES (?, ?, ?, ?, ?)`,
			room, sessionID, ownerID, role, now); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// LeaveRoom removes one session from one room. purge also deletes that session's
// inbox in that room.
func (s *Store) LeaveRoom(room, sessionID string, purge bool) (existed bool, purged int64, err error) {
	err = withRetry(func() error {
		existed, purged = false, 0
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		res, err := tx.Exec(`DELETE FROM members WHERE room = ? AND session_id = ?`, room, sessionID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			existed = true
		}
		if purge {
			pres, err := tx.Exec(`DELETE FROM messages WHERE room = ? AND to_id = ?`, room, sessionID)
			if err != nil {
				return err
			}
			purged, _ = pres.RowsAffected()
		}
		return tx.Commit()
	})
	return existed, purged, err
}

// LeaveSession fully removes one terminal's session: from every room and from
// the sessions table. purge also deletes its whole inbox. This is what the
// SessionEnd hook calls (one terminal closed — not the whole user).
func (s *Store) LeaveSession(sessionID string, purge bool) (rooms int64, err error) {
	err = withRetry(func() error {
		rooms = 0
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		res, err := tx.Exec(`DELETE FROM members WHERE session_id = ?`, sessionID)
		if err != nil {
			return err
		}
		rooms, _ = res.RowsAffected()
		if _, err := tx.Exec(`DELETE FROM sessions WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		if purge {
			if _, err := tx.Exec(`DELETE FROM messages WHERE to_id = ?`, sessionID); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	return rooms, err
}

// Prune removes sessions idle past the cutoff (janitor for crashed terminals)
// and their memberships. Returns the removed session ids.
func (s *Store) Prune(cutoff time.Time) ([]string, error) {
	cut := cutoff.UTC().Format(timeFmt)
	var removed []string
	err := withRetry(func() error {
		removed = nil
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		rows, err := tx.Query(`SELECT session_id FROM sessions WHERE last_seen < ? ORDER BY session_id`, cut)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			removed = append(removed, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM members WHERE session_id IN (SELECT session_id FROM sessions WHERE last_seen < ?)`, cut); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE last_seen < ?`, cut); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// Counts returns row counts per table for the `reset` dry-run summary.
func (s *Store) Counts() (users, sessions, rooms, messages int, err error) {
	q := func(sql string) int {
		var n int
		if err == nil {
			err = s.db.QueryRow(sql).Scan(&n)
		}
		return n
	}
	users = q(`SELECT count(*) FROM users`)
	sessions = q(`SELECT count(*) FROM sessions`)
	rooms = q(`SELECT count(*) FROM rooms`)
	messages = q(`SELECT count(*) FROM messages`)
	return users, sessions, rooms, messages, err
}

// DeleteAllSessions removes every session and its room memberships (clears all
// presence) but keeps users, rooms, and messages. Returns the number of
// sessions deleted.
func (s *Store) DeleteAllSessions() (int64, error) {
	var n int64
	err := withRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`DELETE FROM members`); err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM sessions`)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return tx.Commit()
	})
	return n, err
}

// DeleteSessionsForOwner removes just one user's sessions and their memberships
// (leaving other users, rooms, and messages untouched). Returns the count.
func (s *Store) DeleteSessionsForOwner(ownerID string) (int64, error) {
	var n int64
	err := withRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`DELETE FROM members WHERE owner_id = ?`, ownerID); err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM sessions WHERE owner_id = ?`, ownerID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return tx.Commit()
	})
	return n, err
}

// DeleteAllMessages removes every message (inboxes and history) but keeps users,
// rooms, sessions, and memberships. Returns the number of messages deleted.
func (s *Store) DeleteAllMessages() (int64, error) {
	var n int64
	err := withRetry(func() error {
		res, err := s.db.Exec(`DELETE FROM messages`)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

// ResetAll wipes every table (users, rooms, sessions, members, messages) and
// re-seeds the default room — a from-scratch database without deleting the file.
func (s *Store) ResetAll() error {
	now := time.Now().UTC().Format(timeFmt)
	return withRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, t := range []string{"messages", "members", "sessions", "rooms", "users"} {
			if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO rooms (name, owner_id, created_at) VALUES (?, '', ?)`, defaultRoom, now); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (s *Store) RoleSet(room, sessionID, role string) (bool, error) {
	var existed bool
	err := withRetry(func() error {
		res, err := s.db.Exec(`UPDATE members SET role = ? WHERE room = ? AND session_id = ?`, role, room, sessionID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		existed = n > 0
		return nil
	})
	return existed, err
}

// Rooms lists rooms with their member counts and owner. ownerID "" returns every
// room (global view); a non-empty ownerID scopes to rooms that user is a member
// of (the `--me` view), while member counts still reflect the whole room.
func (s *Store) Rooms(ownerID string) ([]Room, error) {
	q := `
SELECT r.name, r.owner_id, COALESCE(u.username, ''), r.created_at, COUNT(m.session_id)
FROM rooms r
LEFT JOIN users u ON u.user_id = r.owner_id
LEFT JOIN members m ON m.room = r.name`
	var args []any
	if ownerID != "" {
		q += ` WHERE EXISTS (SELECT 1 FROM members mm WHERE mm.room = r.name AND mm.owner_id = ?)`
		args = append(args, ownerID)
	}
	q += ` GROUP BY r.name, r.owner_id, u.username, r.created_at ORDER BY r.name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		var r Room
		var created string
		if err := rows.Scan(&r.Name, &r.OwnerID, &r.Owner, &created, &r.Members); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(timeFmt, created)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Members(room string) ([]Member, error) {
	rows, err := s.db.Query(`
SELECT m.room, m.session_id, m.owner_id, u.username, m.role, m.joined_at
FROM members m JOIN users u ON u.user_id = m.owner_id
WHERE m.room = ? ORDER BY u.username, m.session_id`, room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var joined string
		if err := rows.Scan(&m.Room, &m.SessionID, &m.OwnerID, &m.Username, &m.Role, &joined); err != nil {
			return nil, err
		}
		m.JoinedAt, _ = time.Parse(timeFmt, joined)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ── Messaging ───────────────────────────────────────────────────────────────

// resolveRecipients turns a --to value into concrete recipient session ids in a
// room. Rules (checked in order):
//   - "@role"          → every session in the room with that role
//   - "all" / "*"      → every session in the room
//   - contains "~"     → a literal session id (one terminal)
//   - otherwise        → a username: all that user's sessions in the room
//
// The sender's own session is always excluded from fan-out.
func (s *Store) resolveRecipients(room, fromSession, to string) ([]recipient, error) {
	switch {
	case strings.HasPrefix(to, addrRolePrefix):
		return s.queryRecipients(
			`SELECT session_id, owner_id FROM members WHERE room = ? AND role = ? AND session_id != ? ORDER BY session_id`,
			room, strings.TrimPrefix(to, addrRolePrefix), fromSession)
	case to == addrAll || to == addrAllStar:
		return s.queryRecipients(
			`SELECT session_id, owner_id FROM members WHERE room = ? AND session_id != ? ORDER BY session_id`, room, fromSession)
	case strings.Contains(to, sessionSep):
		// Literal session id — look up its owner (blank if the session isn't
		// registered; the message still delivers and the id embeds the username).
		var owner sql.NullString
		s.db.QueryRow(`SELECT owner_id FROM sessions WHERE session_id = ?`, to).Scan(&owner)
		return []recipient{{ID: to, Owner: owner.String}}, nil
	default:
		// username → that user's sessions that are members of this room
		return s.queryRecipients(`
SELECT m.session_id, m.owner_id FROM members m
JOIN users u ON u.user_id = m.owner_id
WHERE m.room = ? AND u.username = ? AND m.session_id != ? ORDER BY m.session_id`,
			room, to, fromSession)
	}
}

func (s *Store) queryRecipients(q string, args ...any) ([]recipient, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipient
	for rows.Next() {
		var r recipient
		if err := rows.Scan(&r.ID, &r.Owner); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Send delivers body from->to within room. fromOwner is the sender's userId (so
// each message records both parties' identities, not just their sessions).
// Returns the concrete recipients so the caller can warn on an empty fan-out.
func (s *Store) Send(room, fromSession, fromOwner, to, body, kind string, ref *int64) ([]recipient, error) {
	if kind == "" {
		kind = msgKind
	}
	recipients, err := s.resolveRecipients(room, fromSession, to)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(timeFmt)
	err = withRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, r := range recipients {
			if _, err := tx.Exec(
				`INSERT INTO messages (room, from_id, to_id, from_owner, to_owner, kind, body, ref_id, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				room, fromSession, r.ID, fromOwner, r.Owner, kind, body, ref, now); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	s.Touch(fromSession)
	return recipients, nil
}

// answerRequest delivers a reply (secret or deny) to the requester of a request
// message and marks the request consumed. granterOwner is the answering user's
// userId. Returns requester session id + room.
func (s *Store) answerRequest(reqID int64, granterSession, granterOwner, replyKind, body string) (requester, room string, err error) {
	now := time.Now().UTC().Format(timeFmt)
	err = withRetry(func() error {
		requester, room = "", ""
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var kind string
		var reqOwner sql.NullString
		if err := tx.QueryRow(
			`SELECT room, from_id, from_owner, kind FROM messages WHERE id = ?`, reqID).Scan(&room, &requester, &reqOwner, &kind); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("no request with id %d", reqID)
			}
			return err
		}
		if kind != requestKind {
			return fmt.Errorf("message %d is not a request (kind=%s)", reqID, kind)
		}
		if _, err := tx.Exec(
			`INSERT INTO messages (room, from_id, to_id, from_owner, to_owner, kind, body, ref_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			room, granterSession, requester, granterOwner, reqOwner.String, replyKind, body, reqID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE messages SET read_at = ? WHERE id = ?`, now, reqID); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return "", "", err
	}
	s.Touch(granterSession)
	return requester, room, nil
}

func (s *Store) Grant(reqID int64, granterSession, granterOwner, secret string) (string, string, error) {
	return s.answerRequest(reqID, granterSession, granterOwner, secretKind, secret)
}

func (s *Store) Deny(reqID int64, granterSession, granterOwner, reason string) (string, string, error) {
	return s.answerRequest(reqID, granterSession, granterOwner, denyKind, reason)
}

// Recv returns unread messages for a session and consumes them: normal messages
// are marked read; secret payloads are hard-deleted (consume-once). room ""
// drains all rooms; otherwise only that room.
func (s *Store) Recv(sessionID, room string) ([]Message, error) {
	var out []Message
	err := withRetry(func() error {
		out = nil
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		q := `SELECT id, room, from_id, to_id, kind, body, ref_id, created_at
		      FROM messages WHERE to_id = ? AND read_at IS NULL`
		args := []any{sessionID}
		if room != "" {
			q += ` AND room = ?`
			args = append(args, room)
		}
		q += ` ORDER BY id`
		rows, err := tx.Query(q, args...)
		if err != nil {
			return err
		}
		var readIDs, delIDs []int64
		for rows.Next() {
			var m Message
			var created string
			var ref sql.NullInt64
			if err := rows.Scan(&m.ID, &m.Room, &m.From, &m.To, &m.Kind, &m.Body, &ref, &created); err != nil {
				rows.Close()
				return err
			}
			m.CreatedAt, _ = time.Parse(timeFmt, created)
			if ref.Valid {
				m.RefID = &ref.Int64
			}
			out = append(out, m)
			if m.Kind == secretKind {
				delIDs = append(delIDs, m.ID)
			} else {
				readIDs = append(readIDs, m.ID)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := time.Now().UTC().Format(timeFmt)
		for _, id := range readIDs {
			if _, err := tx.Exec(`UPDATE messages SET read_at = ? WHERE id = ?`, now, id); err != nil {
				return err
			}
		}
		for _, id := range delIDs {
			if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	s.Touch(sessionID)
	return out, nil
}

// Inbox returns messages for a USER — across all of that user's sessions —
// without consuming them (peek). session != "" narrows to one of the user's
// sessions; room scopes to a room; all includes already-read history. Secret
// bodies are masked so a peek can't leak a key. Keyed on to_owner (the identity)
// rather than a single session, because a user owns many sessions.
func (s *Store) Inbox(ownerID, session, room string, all bool) ([]Message, error) {
	q := `SELECT id, room, from_id, to_id, kind, body, ref_id, created_at, read_at
	      FROM messages WHERE to_owner = ?`
	args := []any{ownerID}
	if session != "" {
		q += ` AND to_id = ?`
		args = append(args, session)
	}
	if room != "" {
		q += ` AND room = ?`
		args = append(args, room)
	}
	if !all {
		q += ` AND read_at IS NULL`
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		var ref sql.NullInt64
		var read sql.NullString
		if err := rows.Scan(&m.ID, &m.Room, &m.From, &m.To, &m.Kind, &m.Body, &ref, &created, &read); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(timeFmt, created)
		if ref.Valid {
			m.RefID = &ref.Int64
		}
		if read.Valid {
			t, _ := time.Parse(timeFmt, read.String)
			m.ReadAt = &t
		}
		if m.Kind == secretKind {
			m.Body = "<secret — use `lorewire recv` to consume>"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MessagesLog returns a read-only transcript WITHOUT consuming anything — for
// `lorewire log`. It is NOT filtered by recipient (unlike Inbox), so it surfaces
// the full history including messages addressed to session ids that no longer
// exist.
//
//   - room "" (or "all") spans every room; otherwise scopes to that room.
//   - ownerID "" spans all participants; otherwise matches messages the user
//     sent OR received, keyed on the userId (the identity) — so it catches every
//     session that user ever had, robust to session churn and rename.
//   - limit>0 keeps only the most recent N; results are returned oldest-first.
//
// Unconsumed secrets are masked (consumed ones are already hard-deleted).
func (s *Store) MessagesLog(room, ownerID string, limit int) ([]Message, error) {
	q := `SELECT id, room, from_id, to_id, kind, body, ref_id, created_at, read_at FROM messages`
	var conds []string
	var args []any
	if room != "" && room != addrAll {
		conds = append(conds, "room = ?")
		args = append(args, room)
	}
	if ownerID != "" {
		conds = append(conds, "(from_owner = ? OR to_owner = ?)")
		args = append(args, ownerID, ownerID)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	// Most-recent-first for the LIMIT, then reversed below to read oldest-first.
	q += " ORDER BY id DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		var ref sql.NullInt64
		var read sql.NullString
		if err := rows.Scan(&m.ID, &m.Room, &m.From, &m.To, &m.Kind, &m.Body, &ref, &created, &read); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(timeFmt, created)
		if ref.Valid {
			m.RefID = &ref.Int64
		}
		if read.Valid {
			t, _ := time.Parse(timeFmt, read.String)
			m.ReadAt = &t
		}
		if m.Kind == secretKind {
			m.Body = "<secret — consumed on read>"
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse to oldest-first for natural reading order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
