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
	ID        string
	OwnerID   string
	Username  string // resolved from users at read time
	CWD       string
	TTY       string
	PID       int
	Host      string
	Client    string
	CreatedAt time.Time
	LastSeen  time.Time
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
	From      string // sender session id
	To        string // recipient session id
	Kind      string
	Body      string
	RefID     *int64
	CreatedAt time.Time
	ReadAt    *time.Time
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
	if p := os.Getenv("LOREWIRE_DB"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lorewire", "lorewire.db"), nil
}

func dsnFor(path string) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", path)
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
	session_id TEXT PRIMARY KEY,
	owner_id   TEXT NOT NULL,
	cwd        TEXT,
	tty        TEXT,
	pid        INTEGER,
	host       TEXT,
	client     TEXT,
	meta       TEXT,
	created_at TEXT NOT NULL,
	last_seen  TEXT NOT NULL
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
	from_id    TEXT NOT NULL,
	to_id      TEXT NOT NULL,
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
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(timeFmt)
	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO rooms (name, owner_id, created_at) VALUES (?, '', ?)`, defaultRoom, now)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// ── Users ───────────────────────────────────────────────────────────────────

// CreateUser claims a username. If userID is empty a fresh one is minted; a
// non-empty userID re-imports an existing identity (e.g. on a new machine).
// Fails if the username is already taken by a different userId.
func (s *Store) CreateUser(username, userID string) (string, error) {
	if username == "" || strings.ContainsAny(username, "~ ") {
		return "", fmt.Errorf("invalid username %q (no spaces or '~', non-empty)", username)
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
	if strings.ContainsAny(newName, "~") || newName == "" {
		return fmt.Errorf("invalid username %q (no '~', non-empty)", newName)
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
		// (usernames can't contain "~"), so LIKE 'old~%' is safe.
		like := oldName + "~%"
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
		_, err := s.db.Exec(`
INSERT INTO sessions (session_id, owner_id, cwd, tty, pid, host, client, created_at, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  owner_id=excluded.owner_id,
  cwd=COALESCE(NULLIF(excluded.cwd,''), sessions.cwd),
  tty=COALESCE(NULLIF(excluded.tty,''), sessions.tty),
  pid=CASE WHEN excluded.pid>0 THEN excluded.pid ELSE sessions.pid END,
  host=COALESCE(NULLIF(excluded.host,''), sessions.host),
  client=COALESCE(NULLIF(excluded.client,''), sessions.client),
  last_seen=excluded.last_seen`,
			sess.ID, sess.OwnerID, sess.CWD, sess.TTY, sess.PID, sess.Host, sess.Client, now, now)
		return err
	})
}

// Touch refreshes last_seen for a session (heartbeat on send/recv).
func (s *Store) Touch(sessionID string) {
	now := time.Now().UTC().Format(timeFmt)
	s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE session_id = ?`, now, sessionID)
}

func (s *Store) Sessions() ([]Session, error) {
	rows, err := s.db.Query(`
SELECT se.session_id, se.owner_id, u.username, se.cwd, se.tty, se.pid, se.host, se.client, se.created_at, se.last_seen
FROM sessions se JOIN users u ON u.user_id = se.owner_id
ORDER BY u.username, se.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var se Session
		var cwd, tty, host, client sql.NullString
		var pid sql.NullInt64
		var created, seen string
		if err := rows.Scan(&se.ID, &se.OwnerID, &se.Username, &cwd, &tty, &pid, &host, &client, &created, &seen); err != nil {
			return nil, err
		}
		se.CWD, se.TTY, se.Host, se.Client = cwd.String, tty.String, host.String, client.String
		se.PID = int(pid.Int64)
		se.CreatedAt, _ = time.Parse(timeFmt, created)
		se.LastSeen, _ = time.Parse(timeFmt, seen)
		out = append(out, se)
	}
	return out, rows.Err()
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

func (s *Store) Rooms() ([]Room, error) {
	rows, err := s.db.Query(`
SELECT r.name, r.owner_id, COALESCE(u.username, ''), r.created_at, COUNT(m.session_id)
FROM rooms r
LEFT JOIN users u ON u.user_id = r.owner_id
LEFT JOIN members m ON m.room = r.name
GROUP BY r.name, r.owner_id, u.username, r.created_at ORDER BY r.name`)
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
func (s *Store) resolveRecipients(room, fromSession, to string) ([]string, error) {
	switch {
	case strings.HasPrefix(to, "@"):
		return s.queryNames(
			`SELECT session_id FROM members WHERE room = ? AND role = ? AND session_id != ? ORDER BY session_id`,
			room, strings.TrimPrefix(to, "@"), fromSession)
	case to == "all" || to == "*":
		return s.queryNames(
			`SELECT session_id FROM members WHERE room = ? AND session_id != ? ORDER BY session_id`, room, fromSession)
	case strings.Contains(to, "~"):
		return []string{to}, nil
	default:
		// username → that user's sessions that are members of this room
		return s.queryNames(`
SELECT m.session_id FROM members m
JOIN users u ON u.user_id = m.owner_id
WHERE m.room = ? AND u.username = ? AND m.session_id != ? ORDER BY m.session_id`,
			room, to, fromSession)
	}
}

func (s *Store) queryNames(q string, args ...any) ([]string, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Send delivers body from->to within room. Returns concrete recipient session
// ids so the caller can warn on an empty fan-out.
func (s *Store) Send(room, fromSession, to, body, kind string, ref *int64) ([]string, error) {
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
				`INSERT INTO messages (room, from_id, to_id, kind, body, ref_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				room, fromSession, r, kind, body, ref, now); err != nil {
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
// message and marks the request consumed. Returns requester session id + room.
func (s *Store) answerRequest(reqID int64, granterSession, replyKind, body string) (requester, room string, err error) {
	now := time.Now().UTC().Format(timeFmt)
	err = withRetry(func() error {
		requester, room = "", ""
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var kind string
		if err := tx.QueryRow(
			`SELECT room, from_id, kind FROM messages WHERE id = ?`, reqID).Scan(&room, &requester, &kind); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("no request with id %d", reqID)
			}
			return err
		}
		if kind != requestKind {
			return fmt.Errorf("message %d is not a request (kind=%s)", reqID, kind)
		}
		if _, err := tx.Exec(
			`INSERT INTO messages (room, from_id, to_id, kind, body, ref_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			room, granterSession, requester, replyKind, body, reqID, now); err != nil {
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

func (s *Store) Grant(reqID int64, granterSession, secret string) (string, string, error) {
	return s.answerRequest(reqID, granterSession, secretKind, secret)
}

func (s *Store) Deny(reqID int64, granterSession, reason string) (string, string, error) {
	return s.answerRequest(reqID, granterSession, denyKind, reason)
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

// Inbox returns messages for a session without consuming them. Secret bodies are
// masked so a peek can't leak a key.
func (s *Store) Inbox(sessionID, room string, all bool) ([]Message, error) {
	q := `SELECT id, room, from_id, to_id, kind, body, ref_id, created_at, read_at
	      FROM messages WHERE to_id = ?`
	args := []any{sessionID}
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
