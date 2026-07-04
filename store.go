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
// in "main" so the tool works with zero room ceremony, and rooms are additive
// scoping on top.
const defaultRoom = "main"

// roleGuest is the role assigned when a session joins without one. A guest can
// read and send but holds no special capability; it's flagged in `members` so
// an owner can promote it. (Trust is cooperative/local for now — roles are
// coordination labels, not enforced ACLs.)
const roleGuest = "guest"

// Message kinds. Normal chatter is msgKind; the request/grant/deny trio powers
// the "agent asks a role-holder for something (e.g. an API key)" flow, and
// secretKind payloads are consume-once + hard-deleted on read so keys don't
// linger in history.
const (
	msgKind     = "msg"
	requestKind = "request"
	grantKind   = "grant"
	denyKind    = "deny"
	secretKind  = "secret"
)

// Message is one delivered message row. A broadcast or @role send fans out at
// send time into one row per recipient, so every row has exactly one concrete
// to_name and its own read_at watermark — recv stays a plain "unread rows for
// me" query. Room scopes the message; Kind selects normal vs request/grant flow.
type Message struct {
	ID        int64
	Room      string
	From      string
	To        string
	Kind      string
	Body      string
	RefID     *int64 // for grant/deny: the request message id being answered
	CreatedAt time.Time
	ReadAt    *time.Time
}

// Session is a live participant, keyed by its human-chosen name. A session is
// global presence; which rooms it belongs to (and with what role) lives in the
// members table, so one session can be in many rooms at once.
type Session struct {
	Name      string
	CreatedAt time.Time
	LastSeen  time.Time
}

// Member is a session's membership of one room, with its role there.
type Member struct {
	Room     string
	Name     string
	Role     string
	JoinedAt time.Time
}

// Room is a named channel. Owner is the name that first created it (empty for
// the system-created default room).
type Room struct {
	Name      string
	Owner     string
	CreatedAt time.Time
	Members   int
}

// Store wraps the SQLite handle. One file, concurrent-write-safe via SQLite's
// own locking plus busy_timeout and a retry-on-lock wrapper, since sessions are
// separate processes.
type Store struct{ db *sql.DB }

const timeFmt = time.RFC3339Nano

// withRetry re-runs a write that failed because another session held the
// SQLite lock. Sessions are separate processes, so busy_timeout is the only
// cross-process backpressure; this loop is belt-and-suspenders on top of it so
// a transient SQLITE_BUSY never surfaces as a user-facing error.
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

// dbPath resolves the lorewire database location: $LOREWIRE_DB, else
// ~/.lorewire/lorewire.db. The parent dir is created on open.
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

func openStore() (*Store, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// _busy_timeout: wait up to 10s for the write lock before failing.
	// _journal_mode=WAL: lets readers proceed during a writer, which matters
	// when several sessions poll/recv concurrently.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
	name       TEXT PRIMARY KEY,
	created_at TEXT NOT NULL,
	last_seen  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rooms (
	name       TEXT PRIMARY KEY,
	owner      TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
	room      TEXT NOT NULL,
	name      TEXT NOT NULL,
	role      TEXT NOT NULL,
	joined_at TEXT NOT NULL,
	PRIMARY KEY (room, name)
);
CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	room       TEXT NOT NULL DEFAULT 'main',
	from_name  TEXT NOT NULL,
	to_name    TEXT NOT NULL,
	kind       TEXT NOT NULL DEFAULT 'msg',
	body       TEXT NOT NULL,
	ref_id     INTEGER,
	created_at TEXT NOT NULL,
	read_at    TEXT
);
CREATE INDEX IF NOT EXISTS idx_messages_to_unread ON messages(to_name, read_at);
`); err != nil {
		return err
	}
	// Additive migration for a pre-rooms messages table (columns added only if
	// missing, so upgrading an old DB keeps its data).
	for col, decl := range map[string]string{
		"room":   "room TEXT NOT NULL DEFAULT 'main'",
		"kind":   "kind TEXT NOT NULL DEFAULT 'msg'",
		"ref_id": "ref_id INTEGER",
	} {
		ok, err := s.columnExists("messages", col)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := s.db.Exec("ALTER TABLE messages ADD COLUMN " + decl); err != nil {
				return err
			}
		}
	}
	// The default room always exists so room-less commands have somewhere to go.
	now := time.Now().UTC().Format(timeFmt)
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO rooms (name, owner, created_at) VALUES (?, '', ?)`, defaultRoom, now)
	return err
}

func (s *Store) columnExists(table, col string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
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

// upsertSession refreshes global presence for a name inside an existing tx.
func upsertSession(tx *sql.Tx, name, now string) error {
	_, err := tx.Exec(`
INSERT INTO sessions (name, created_at, last_seen) VALUES (?, ?, ?)
ON CONFLICT(name) DO UPDATE SET last_seen = excluded.last_seen`, name, now, now)
	return err
}

// Register marks a session present and joins it to the default room (as guest
// if it wasn't already a member), so plain register/send/recv "just work"
// without any room ceremony. Idempotent.
func (s *Store) Register(name string) error {
	now := time.Now().UTC().Format(timeFmt)
	return withRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := upsertSession(tx, name, now); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO members (room, name, role, joined_at) VALUES (?, ?, ?, ?)`,
			defaultRoom, name, roleGuest, now); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// Touch refreshes last_seen without erroring if the name is unknown; used as a
// lightweight heartbeat on send/recv so activity-based views stay accurate.
func (s *Store) Touch(name string) {
	now := time.Now().UTC().Format(timeFmt)
	s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE name = ?`, now, name)
}

// Join adds name to room with role (defaulting to guest), creating the room if
// it doesn't exist yet — in which case the joiner becomes its owner. Rejoining
// updates the role. Returns whether the room was freshly created and its owner.
func (s *Store) Join(room, name, role string) (created bool, owner string, err error) {
	if role == "" {
		role = roleGuest
	}
	now := time.Now().UTC().Format(timeFmt)
	err = withRetry(func() error {
		created, owner = false, "" // reset on retry
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := upsertSession(tx, name, now); err != nil {
			return err
		}
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO rooms (name, owner, created_at) VALUES (?, ?, ?)`, room, name, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			created = true
		}
		if err := tx.QueryRow(`SELECT owner FROM rooms WHERE name = ?`, room).Scan(&owner); err != nil {
			return err
		}
		if _, err := tx.Exec(`
INSERT INTO members (room, name, role, joined_at) VALUES (?, ?, ?, ?)
ON CONFLICT(room, name) DO UPDATE SET role = excluded.role`, room, name, role, now); err != nil {
			return err
		}
		return tx.Commit()
	})
	return created, owner, err
}

// Leave removes name from a single room. When purge is true it also deletes
// that name's inbox *in that room* (messages addressed to it there) — never
// messages it sent to others. Returns whether a membership existed.
func (s *Store) Leave(room, name string, purge bool) (existed bool, purged int64, err error) {
	err = withRetry(func() error {
		existed, purged = false, 0
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		res, err := tx.Exec(`DELETE FROM members WHERE room = ? AND name = ?`, room, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			existed = true
		}
		if purge {
			pres, err := tx.Exec(`DELETE FROM messages WHERE room = ? AND to_name = ?`, room, name)
			if err != nil {
				return err
			}
			purged, _ = pres.RowsAffected()
		}
		return tx.Commit()
	})
	return existed, purged, err
}

// LeaveAll fully unregisters a session: removes it from every room and from the
// sessions table. purge additionally deletes its entire inbox across all rooms.
// This is what the SessionEnd hook calls when a Claude Code session closes.
func (s *Store) LeaveAll(name string, purge bool) (rooms int64, err error) {
	err = withRetry(func() error {
		rooms = 0
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		res, err := tx.Exec(`DELETE FROM members WHERE name = ?`, name)
		if err != nil {
			return err
		}
		rooms, _ = res.RowsAffected()
		if _, err := tx.Exec(`DELETE FROM sessions WHERE name = ?`, name); err != nil {
			return err
		}
		if purge {
			if _, err := tx.Exec(`DELETE FROM messages WHERE to_name = ?`, name); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	return rooms, err
}

// Prune removes sessions whose last_seen predates cutoff (a janitor for crashed
// sessions) along with their room memberships. Relies on the heartbeat that
// register/send/recv/watch refresh, so callers should use a generous cutoff.
// Messages are left intact. Returns the names removed.
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
		rows, err := tx.Query(`SELECT name FROM sessions WHERE last_seen < ? ORDER BY name`, cut)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			removed = append(removed, n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM members WHERE name IN (SELECT name FROM sessions WHERE last_seen < ?)`, cut); err != nil {
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

// RoleSet changes a member's role within a room. Returns whether the member
// existed. (Cooperative/local trust: any member may set roles for now; owner
// enforcement arrives with a networked trust model.)
func (s *Store) RoleSet(room, name, role string) (bool, error) {
	var existed bool
	err := withRetry(func() error {
		res, err := s.db.Exec(`UPDATE members SET role = ? WHERE room = ? AND name = ?`, role, room, name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		existed = n > 0
		return nil
	})
	return existed, err
}

func (s *Store) Sessions() ([]Session, error) {
	rows, err := s.db.Query(`SELECT name, created_at, last_seen FROM sessions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var name, created, seen string
		if err := rows.Scan(&name, &created, &seen); err != nil {
			return nil, err
		}
		c, _ := time.Parse(timeFmt, created)
		l, _ := time.Parse(timeFmt, seen)
		out = append(out, Session{Name: name, CreatedAt: c, LastSeen: l})
	}
	return out, rows.Err()
}

func (s *Store) Rooms() ([]Room, error) {
	rows, err := s.db.Query(`
SELECT r.name, r.owner, r.created_at, COUNT(m.name)
FROM rooms r LEFT JOIN members m ON m.room = r.name
GROUP BY r.name, r.owner, r.created_at ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		var name, owner, created string
		var count int
		if err := rows.Scan(&name, &owner, &created, &count); err != nil {
			return nil, err
		}
		c, _ := time.Parse(timeFmt, created)
		out = append(out, Room{Name: name, Owner: owner, CreatedAt: c, Members: count})
	}
	return out, rows.Err()
}

func (s *Store) Members(room string) ([]Member, error) {
	rows, err := s.db.Query(
		`SELECT room, name, role, joined_at FROM members WHERE room = ? ORDER BY name`, room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var r, n, role, joined string
		if err := rows.Scan(&r, &n, &role, &joined); err != nil {
			return nil, err
		}
		j, _ := time.Parse(timeFmt, joined)
		out = append(out, Member{Room: r, Name: n, Role: role, JoinedAt: j})
	}
	return out, rows.Err()
}

// resolveRecipients turns a --to value into concrete recipient names within a
// room. "@role" fans out to every member of that room holding the role;
// "all"/"*" to every member; anything else is a single literal recipient (even
// if not a member — a name can still be messaged directly). The sender is
// always excluded from fan-out.
func (s *Store) resolveRecipients(room, from, to string) ([]string, error) {
	switch {
	case strings.HasPrefix(to, "@"):
		return s.queryNames(
			`SELECT name FROM members WHERE room = ? AND role = ? AND name != ? ORDER BY name`,
			room, strings.TrimPrefix(to, "@"), from)
	case to == "all" || to == "*":
		return s.queryNames(
			`SELECT name FROM members WHERE room = ? AND name != ? ORDER BY name`, room, from)
	default:
		return []string{to}, nil
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

// Send delivers body from->to within room, with the given kind and optional
// ref (linking a grant/deny back to its request). Returns the concrete
// recipients so the caller can warn on an empty broadcast/@role (skeleton
// honesty: delivering to nobody must not look like success).
func (s *Store) Send(room, from, to, body, kind string, ref *int64) ([]string, error) {
	if kind == "" {
		kind = msgKind
	}
	recipients, err := s.resolveRecipients(room, from, to)
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
				`INSERT INTO messages (room, from_name, to_name, kind, body, ref_id, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				room, from, r, kind, body, ref, now); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	s.Touch(from)
	return recipients, nil
}

// Grant answers a request message (by id) with a secret payload delivered to
// the original requester. The secret is a consume-once row (recv hard-deletes
// it after one read). The request row is marked read so it stops re-appearing.
// Returns the requester's name and the room the exchange happened in.
func (s *Store) Grant(reqID int64, granter, secret string) (requester, room string, err error) {
	return s.answerRequest(reqID, granter, secretKind, secret)
}

// Deny answers a request with a plain reason (no secret). Same bookkeeping as
// Grant but the reply is normal, non-consumed chatter.
func (s *Store) Deny(reqID int64, granter, reason string) (requester, room string, err error) {
	return s.answerRequest(reqID, granter, denyKind, reason)
}

func (s *Store) answerRequest(reqID int64, granter, replyKind, body string) (requester, room string, err error) {
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
			`SELECT room, from_name, kind FROM messages WHERE id = ?`, reqID).Scan(&room, &requester, &kind); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("no request with id %d", reqID)
			}
			return err
		}
		if kind != requestKind {
			return fmt.Errorf("message %d is not a request (kind=%s)", reqID, kind)
		}
		if _, err := tx.Exec(
			`INSERT INTO messages (room, from_name, to_name, kind, body, ref_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			room, granter, requester, replyKind, body, reqID, now); err != nil {
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
	s.Touch(granter)
	return requester, room, nil
}

// Recv returns unread messages for name and consumes them in one transaction.
// Normal messages are marked read; secret payloads are hard-deleted so a key is
// gone after a single read. When room is non-empty, only that room is drained;
// otherwise the session's inbox across all rooms is returned at once.
func (s *Store) Recv(name, room string) ([]Message, error) {
	var out []Message
	err := withRetry(func() error {
		out = nil // reset on retry so a partial claim isn't returned twice
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		q := `SELECT id, room, from_name, to_name, kind, body, ref_id, created_at
		      FROM messages WHERE to_name = ? AND read_at IS NULL`
		args := []any{name}
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
				delIDs = append(delIDs, m.ID) // consume-once: hard-delete secrets
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
	s.Touch(name)
	return out, nil
}

// Inbox returns messages for name without consuming them. When all is false
// only unread rows are shown. room optionally scopes to a single room. Secret
// bodies are masked here — they are only revealed by recv (which consumes
// them), so a non-consuming peek can't leak a key.
func (s *Store) Inbox(name, room string, all bool) ([]Message, error) {
	q := `SELECT id, room, from_name, to_name, kind, body, ref_id, created_at, read_at
	      FROM messages WHERE to_name = ?`
	args := []any{name}
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
