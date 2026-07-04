// Command lorewire is a tiny message bus that lets multiple agent sessions
// (Claude Code, other agents, or plain scripts — separate processes) talk to
// each other over a shared SQLite file.
//
// Identity is split into a stable user (userId + username) that owns many
// sessions (one per terminal). A project-local .lorewire.jsonc supplies the
// default identity/room/role so terminals self-configure; env vars and flags
// override. Messages are room-scoped; a request/grant flow delivers secrets
// consume-once.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "user":
		err = cmdUser(args)
	case "init":
		err = cmdInit(args)
	case "import":
		err = cmdImport(args)
	case "whoami":
		err = cmdWhoami(args)
	case "register":
		err = cmdRegister(args)
	case "join":
		err = cmdJoin(args)
	case "leave":
		err = cmdLeave(args)
	case "prune":
		err = cmdPrune(args)
	case "reset":
		err = cmdReset(args)
	case "rooms":
		err = cmdRooms(args)
	case "members":
		err = cmdMembers(args)
	case "role":
		err = cmdRole(args)
	case "sessions":
		err = cmdSessions(args)
	case "send":
		err = cmdSend(args)
	case "request":
		err = cmdRequest(args)
	case "grant":
		err = cmdGrant(args)
	case "deny":
		err = cmdDeny(args)
	case "recv":
		err = cmdRecv(args)
	case "inbox":
		err = cmdInbox(args)
	case "watch":
		err = cmdWatch(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`lorewire — a message bus for talking between agent/Claude Code sessions

lorewire lets separate terminal sessions (you, other AI agents, or scripts)
message each other through a shared local database. If you are an AI agent, this
is how you coordinate with other sessions: ask questions, hand off work, request
a secret. Everything below is what you need to use it.

WHAT YOU ARE
  When you run lorewire you act as a SESSION — this one terminal — owned by a
  USER (a stable identity: userId + human username, e.g. "bob"). One user can
  have many sessions (one per terminal). Your identity, default room, and role
  are read from a .lorewire.jsonc in the current folder (override with env/flags).
  Run  lorewire whoami  to see exactly who you are, your session id, room, role.

HOW MESSAGING WORKS
  Messages live in a ROOM (default "main"; rooms just scope conversations).
  You address a message with --to:
    --to bob        every one of user bob's terminals in the room
    --to @cto       everyone holding the "cto" role in the room
    --to all        every member of the room
    --to bob~a1f    one specific terminal (a session id)
  To RECEIVE, you must be a member of the room — recv/watch/register do this for
  you automatically. Read your inbox with:
    lorewire recv     read AND consume new messages (they're marked read/removed)
    lorewire inbox    show messages without consuming (--all includes read)
    lorewire watch    block and stream new messages as they arrive
  An incoming message prints as  "<room>/<sender>~<hash> → <you>: <text>".
  To REPLY, take the sender's username (the part before "~") and:
    lorewire send --to <sender> "your reply"        (or --to @<their-role>)

COMMANDS  (🌐 GLOBAL = works from ANY directory · 📁 SESSION = acts as you, uses this folder's identity)

  SETUP — claim or point an identity (writes ./.lorewire.jsonc):
    lorewire user create NAME [--id usr_…] [--room R] [--role X]   claim a username; mint/import a userId
    lorewire init --username NAME | --user usr_…                   point this dir at an existing identity
    lorewire import [NAME]                                         re-create this dir's .lorewire.jsonc identity in the DB (fresh machine)

  🌐 GLOBAL — inspect/manage everything, from any directory:
    lorewire sessions [--me] [--json]          list live sessions (all, or --me = just yours)
    lorewire user list [--json]                list all users + session counts
    lorewire user rename OLD NEW               rename a username (userId unchanged)
    lorewire rooms [--me] [--json]             list rooms (all, or --me = only ones you're in)
    lorewire members [--room ROOM] [--json]    list a room's members and roles
    lorewire role set NAME|SESSION ROLE [--room ROOM]   change a member's role
    lorewire prune [--older-than 30m]          remove sessions not seen since the cutoff
    lorewire reset sessions [--user NAME|--me] [--yes]   delete sessions (all, or one user's)
    lorewire reset messages|all [--yes]        delete all messages / wipe everything

  📁 SESSION — act as you (uses the current folder's identity/room/role):
    lorewire whoami [--json]                    who am I here + this terminal's session detail
    lorewire register [--new]                   put this terminal on the wire and join the room
    lorewire join --room ROOM [--role ROLE]     join/create a room with a role
    lorewire leave [--room ROOM] [--purge]      leave one room
    lorewire leave --all [--purge]              remove this terminal's session from every room
    lorewire send [--room ROOM] --to NAME|@ROLE|all|SESSION MSG   send a message
    lorewire recv  [--room ROOM] [--json]       read + consume unread messages
    lorewire inbox [--room ROOM] [--all] [--json]   show messages without consuming
    lorewire watch [--room ROOM] [--interval 2s]    stream new messages
    lorewire request [--room ROOM] --to @ROLE|NAME MSG   ask a role-holder for something (e.g. a key)
    lorewire grant ID --secret VALUE            answer a request with a consume-once secret
    lorewire deny  ID REASON                     decline a request

TYPICAL AGENT FLOW
    lorewire whoami                         # confirm who I am and my room
    lorewire recv                           # read new messages (who asked what)
    lorewire send --to alice "on it"        # reply to a specific user
    lorewire send --to @dev "PR is up"      # message everyone with a role
    lorewire request --to @cto "OpenAI API key"   # ask; the cto runs: grant <id> --secret …
    lorewire sessions                       # see who else is online (global)

RESOLUTION
  Identity: --user/--name  >  $LOREWIRE_USER_ID / $LOREWIRE_NAME  >  .lorewire.jsonc userId
  Room:     --room  >  $LOREWIRE_ROOM  >  .lorewire.jsonc room  >  "main"
  Role:     --role  >  $LOREWIRE_ROLE  >  .lorewire.jsonc role  >  "guest"
  Session:  $LOREWIRE_SESSION (full id)  >  a token from $LOREWIRE_SESSION_TOKEN /
            $LOREWIRE_SESSION_ENV (name an agent's session-id var) / a known agent
            session id  >  the controlling tty. One agent session = one lorewire session.
  Database (shared by all sessions): $LOREWIRE_DB, else ~/.lorewire/lorewire.db
  Add --json to any listing/read command for machine-readable output.
`)
}

// ── Identity resolution ─────────────────────────────────────────────────────

// ident is the resolved context for a command: who we are (user + session),
// which room, and what role — plus where each value came from (for whoami).
type ident struct {
	userID, username, sessionID, room, role string
	srcUser, srcRoom, srcRole               string
}

// pick returns the first non-empty value from the given (value, source) pairs.
type srcVal struct{ val, src string }

func pick(pairs ...srcVal) (string, string) {
	for _, p := range pairs {
		if p.val != "" {
			return p.val, p.src
		}
	}
	return "", srcDefault
}

// resolveIdentity computes the effective identity WITHOUT writing to the DB.
// Precedence per value: flag > env > .lorewire.jsonc > default. userId (if
// present anywhere) is authoritative and must exist in the DB; otherwise we
// fall back to quick-mode username (auto-created). ensure=true creates the user
// for quick-mode; ensure=false (read-only, e.g. whoami) leaves it unresolved.
func resolveIdentity(st *Store, fUser, fName, fRoom, fRole string, ensure bool) (ident, error) {
	cfg, err := loadConfig()
	if err != nil {
		return ident{}, err
	}
	var id ident

	// Identity resolves as a whole across userId and username, so a flag or env
	// beats a lower layer regardless of which *kind* it is. Order (first wins):
	//   1. --user (userId)   2. --name (username)
	//   3. $LOREWIRE_USER_ID 4. $LOREWIRE_NAME
	//   5. config userId
	// This way an explicit $LOREWIRE_NAME=dave overrides a dir's committed
	// userId, instead of the config silently winning.
	byID := func(uid, src string) error {
		name, ok, err := st.UserByID(uid)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf(
				"userId %q is not in this database — run `lorewire user create <name> --id %s` to import it, or fix %s",
				uid, uid, cfg.Path)
		}
		id.userID, id.username, id.srcUser = uid, name, src
		return nil
	}
	byName := func(name, src string) error {
		if ensure {
			uid2, err := st.EnsureUser(name)
			if err != nil {
				return err
			}
			id.userID = uid2
		}
		id.username, id.srcUser = name, src
		return nil
	}
	switch {
	case fUser != "":
		if err := byID(fUser, srcFlag); err != nil {
			return ident{}, err
		}
	case fName != "":
		if err := byName(fName, srcFlag); err != nil {
			return ident{}, err
		}
	case os.Getenv(envUserID) != "":
		if err := byID(os.Getenv(envUserID), srcEnv); err != nil {
			return ident{}, err
		}
	case os.Getenv(envName) != "":
		if err := byName(os.Getenv(envName), srcEnv); err != nil {
			return ident{}, err
		}
	case cfg.UserID != "":
		if err := byID(cfg.UserID, srcConfig); err != nil {
			return ident{}, err
		}
	default:
		return ident{}, fmt.Errorf(
			"no identity: run `lorewire user create <name>` (writes .lorewire.jsonc), or set $LOREWIRE_NAME")
	}

	id.sessionID, _ = pick(
		srcVal{os.Getenv(envSession), srcEnv},
		srcVal{sessionID(id.username), srcAuto},
	)
	id.room, id.srcRoom = pick(
		srcVal{fRoom, srcFlag},
		srcVal{os.Getenv(envRoom), srcEnv},
		srcVal{cfg.Room, srcConfig},
		srcVal{defaultRoom, srcDefault},
	)
	id.role, id.srcRole = pick(
		srcVal{fRole, srcFlag},
		srcVal{os.Getenv(envRole), srcEnv},
		srcVal{cfg.Role, srcConfig},
		srcVal{roleGuest, srcDefault},
	)
	return id, nil
}

// captureContext gathers this terminal/session/agent's detail into a Session.
// full=true also runs the subprocess probes (tty, git) used by register/watch;
// false skips them for lightweight commands, and RegisterSession preserves any
// value a fuller earlier capture stored.
func captureContext(full bool) Session {
	s := Session{
		PID:         os.Getppid(),
		Client:      clientKind(),
		OSUser:      osUser(),
		OS:          goOS(),
		Arch:        goArch(),
		Shell:       filepath.Base(os.Getenv("SHELL")),
		TermProgram: os.Getenv("TERM_PROGRAM"),
		Version:     lorewireVersion(),
		SSH:         os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "",
		Tmux:        os.Getenv("TMUX") != "",
	}
	s.CWD, _ = os.Getwd()
	s.Host, _ = os.Hostname()
	// Record where the session id was derived from (for debugging). An explicit
	// full id ($LOREWIRE_SESSION) short-circuits the token entirely.
	if os.Getenv(envSession) != "" {
		s.IDSource = "env:" + envSession
	} else {
		_, s.IDSource = terminalTokenSourced()
	}
	if full {
		s.TTY = ttyName()
		s.GitBranch = gitOut("rev-parse", "--abbrev-ref", "HEAD")
		if top := gitOut("rev-parse", "--show-toplevel"); top != "" {
			s.GitRepo = filepath.Base(top)
		}
	}
	return s
}

// currentUserID resolves "me" for --me filters: the userId of the identity in
// the current folder's config / env, WITHOUT creating anything. Errors clearly
// when there is no identity here, or the resolved username has never registered.
func currentUserID(st *Store) (string, string, error) {
	id, err := resolveIdentity(st, "", "", "", "", false)
	if err != nil {
		return "", "", err
	}
	if id.userID != "" {
		return id.userID, id.username, nil
	}
	// Quick mode (username via $LOREWIRE_NAME) doesn't resolve a userId until
	// the user exists — look it up; a not-yet-created user has no sessions/rooms.
	uid, ok, err := st.UserByName(id.username)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("no registered user %q yet — run `lorewire register` first", id.username)
	}
	return uid, id.username, nil
}

// ctx resolves identity and ensures this terminal's session row exists (upsert
// with best-effort context).
//
// joinRoom controls whether the session is also made a MEMBER of the resolved
// room. This matters because delivery is room-scoped: `--to <user>`/`@role`/
// `all` only reach sessions that are members of that room. A command that
// receives (recv/watch/inbox) or participates (send/request) must therefore
// join, or a terminal that only ever runs `watch` would silently receive
// nothing (it registered a session but was never in the room). register/join
// pass joinRoom=false because they call Store.Join explicitly to report
// created/owner.
func ctx(st *Store, fUser, fName, fRoom, fRole string, fullContext, joinRoom bool) (ident, error) {
	id, err := resolveIdentity(st, fUser, fName, fRoom, fRole, true)
	if err != nil {
		return ident{}, err
	}
	sess := captureContext(fullContext)
	sess.ID = id.sessionID
	sess.OwnerID = id.userID
	if err := st.RegisterSession(sess); err != nil {
		return ident{}, err
	}
	if joinRoom {
		// EnsureMember, NOT Join: incidental presence must not overwrite a role
		// set by an explicit `join --role`/`role set`.
		if err := st.EnsureMember(id.room, id.sessionID, id.userID, id.role); err != nil {
			return ident{}, err
		}
	}
	return id, nil
}

// ── Identity commands ───────────────────────────────────────────────────────

func cmdUser(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lorewire user create|list|rename …")
	}
	switch args[0] {
	case "create":
		return cmdUserCreate(args[1:])
	case "list":
		return cmdUserList(args[1:])
	case "rename":
		return cmdUserRename(args[1:])
	default:
		return fmt.Errorf("unknown user subcommand %q (create|list|rename)", args[0])
	}
}

func cmdUserCreate(args []string) error {
	// NAME is the leading positional; Go's flag parser stops at the first
	// non-flag token, so take the name off before parsing the flags.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: lorewire user create NAME [--id usr_…] [--room R] [--role X]")
	}
	name := args[0]
	fs := flag.NewFlagSet("user create", flag.ExitOnError)
	id := fs.String("id", "", "reuse an existing userId (import) instead of minting one")
	noWrite := fs.Bool("no-write", false, "do not write .lorewire.jsonc")
	room := fs.String("room", "", "room to seed in the written config")
	role := fs.String("role", "", "role to seed in the written config")
	fs.Parse(args[1:])
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	userID, err := st.CreateUser(name, *id)
	if err != nil {
		return err
	}
	fmt.Printf("user %q → %s\n", name, userID)
	if !*noWrite {
		path, err := writeConfig("", userID, name, *room, *role)
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", path)
	}
	return nil
}

func cmdUserList(args []string) error {
	fs := flag.NewFlagSet("user list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	users, err := st.ListUsers()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(users)
	}
	if len(users) == 0 {
		fmt.Println("(no users)")
		return nil
	}
	for _, u := range users {
		fmt.Printf("%-16s  %s  %d session(s)\n", u.Username, u.ID, u.Sessions)
	}
	return nil
}

func cmdUserRename(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: lorewire user rename OLD NEW")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.RenameUser(args[0], args[1]); err != nil {
		return err
	}
	fmt.Printf("renamed %q → %q\n", args[0], args[1])
	return nil
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	username := fs.String("username", "", "existing username to point this dir at")
	user := fs.String("user", "", "existing userId to point this dir at")
	room := fs.String("room", "", "default room for this dir")
	role := fs.String("role", "", "your role in this dir")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	userID := *user
	if userID == "" && *username != "" {
		id, ok, err := st.UserByName(*username)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no user named %q — create it with `lorewire user create %s`", *username, *username)
		}
		userID = id
	}
	if userID == "" {
		return fmt.Errorf("provide --username NAME or --user usr_… of an existing identity")
	}
	uname, ok, err := st.UserByID(userID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no user with id %q", userID)
	}
	path, err := writeConfig("", userID, uname, *room, *role)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (userId %s, username %s)\n", path, userID, uname)
	return nil
}

// cmdImport re-creates the identity described by the local .lorewire.jsonc in
// this machine's database — the fresh-machine / post-wipe path. It reads the
// userId (and username hint) from the config and, if that userId isn't already
// present, registers it. Idempotent: a no-op when the identity already exists.
func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	fs.Parse(args)

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.Path == "" {
		return fmt.Errorf("no .lorewire.jsonc found here (or in a parent dir) to import")
	}
	if cfg.UserID == "" {
		return fmt.Errorf("%s has no userId to import", cfg.Path)
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// Already present → nothing to do (report the current username).
	if name, ok, err := st.UserByID(cfg.UserID); err != nil {
		return err
	} else if ok {
		fmt.Printf("already imported: %s is %q (from %s)\n", cfg.UserID, name, cfg.Path)
		return nil
	}

	// Not present → need a username to create it. Prefer the optional positional
	// arg, else the config's username hint.
	name := cfg.Username
	if fs.NArg() > 0 {
		name = fs.Arg(0)
	}
	if name == "" {
		return fmt.Errorf("%s has no username to import under — run `lorewire import <name>`", cfg.Path)
	}
	if _, err := st.CreateUser(name, cfg.UserID); err != nil {
		return err
	}
	// Refresh the config so a missing username hint gets written for next time.
	writeConfig(filepath.Dir(cfg.Path), cfg.UserID, name, cfg.Room, cfg.Role)
	fmt.Printf("imported %q (%s) from %s — ready to use here\n", name, cfg.UserID, cfg.Path)
	return nil
}

func cmdWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := resolveIdentity(st, "", "", "", "", false)
	if err != nil {
		return err
	}
	cfg, _ := loadConfig()

	// The current terminal's stored session row + its room memberships (nil /
	// empty until this terminal has registered).
	sess, registered, err := st.SessionByID(id.sessionID)
	if err != nil {
		return err
	}
	memberships, err := st.MembershipsForSession(id.sessionID)
	if err != nil {
		return err
	}

	if *asJSON {
		// Everything about "me" in one object: resolved identity + where each
		// value came from + this terminal's full session row + memberships.
		return printJSON(map[string]any{
			"userId":     id.userID,
			"username":   id.username,
			"session":    id.sessionID,
			"registered": registered,
			"room":       id.room,
			"role":       id.role,
			"config":     cfg.Path,
			"sources": map[string]string{
				"identity": id.srcUser, "room": id.srcRoom, "role": id.srcRole,
			},
			"sessionDetail": sess, // null until registered
			"memberships":   memberships,
		})
	}

	fmt.Printf("username : %s (%s)\n", id.username, id.srcUser)
	if id.userID != "" {
		fmt.Printf("userId   : %s\n", id.userID)
	} else {
		fmt.Printf("userId   : (not yet created — quick mode; run `lorewire register` to claim)\n")
	}
	fmt.Printf("session  : %s\n", id.sessionID)
	fmt.Printf("room     : %s (%s)\n", id.room, id.srcRoom)
	fmt.Printf("role     : %s (%s)\n", id.role, id.srcRole)
	if cfg.Path != "" {
		fmt.Printf("config   : %s\n", cfg.Path)
	} else {
		fmt.Printf("config   : (none found)\n")
	}

	fmt.Println("\nsession (this terminal):")
	if !registered {
		fmt.Println("  (not registered yet — run `lorewire register`)")
		return nil
	}
	fmt.Printf("  cwd      : %s\n", orDash(sess.CWD))
	if sess.GitRepo != "" || sess.GitBranch != "" {
		fmt.Printf("  git      : %s @ %s\n", orDash(sess.GitRepo), orDash(sess.GitBranch))
	}
	fmt.Printf("  client   : %s\n", orDash(sess.Client))
	fmt.Printf("  terminal : %s\n", orDash(sess.TermProgram))
	fmt.Printf("  tty      : %s\n", orDash(sess.TTY))
	fmt.Printf("  id from  : %s\n", orDash(sess.IDSource))
	fmt.Printf("  os user  : %s\n", orDash(sess.OSUser))
	fmt.Printf("  platform : %s/%s  (%s)\n", orDash(sess.OS), orDash(sess.Arch), orDash(sess.Shell))
	fmt.Printf("  host     : %s\n", orDash(sess.Host))
	fmt.Printf("  pid      : %s\n", orDashInt(sess.PID))
	if flags := sessionFlags(sess); flags != "" {
		fmt.Printf("  flags    : %s\n", flags)
	}
	fmt.Printf("  version  : %s\n", orDash(sess.Version))
	fmt.Printf("  started  : %s\n", sess.CreatedAt.Local().Format("15:04:05"))
	fmt.Printf("  last seen: %s\n", humanAgo(sess.LastSeen))
	if len(memberships) == 0 {
		fmt.Printf("  member of: (no rooms)\n")
	} else {
		parts := make([]string, len(memberships))
		for i, m := range memberships {
			parts[i] = fmt.Sprintf("%s (%s)", m.Room, m.Role)
		}
		fmt.Printf("  member of: %s\n", strings.Join(parts, ", "))
	}
	return nil
}

// orDash renders "?" for an empty string so a missing session field is obvious.
func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func orDashInt(n int) string {
	if n == 0 {
		return "?"
	}
	return strconv.Itoa(n)
}

// sessionFlags renders the boolean environment markers (ssh, tmux) as a short
// comma list, or "" when none apply.
func sessionFlags(s *Session) string {
	var f []string
	if s.SSH {
		f = append(f, "ssh")
	}
	if s.Tmux {
		f = append(f, "tmux")
	}
	return strings.Join(f, ", ")
}

// ── Presence & rooms ────────────────────────────────────────────────────────

func cmdRegister(args []string) error {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("name", "", "username override (quick mode)")
	fRoom := fs.String("room", "", "room override")
	fRole := fs.String("role", "", "role override")
	newSess := fs.Bool("new", false, "force a fresh session handle for this terminal")
	fs.Parse(args)
	if *newSess {
		// Rotate the session token so this terminal gets a distinct handle.
		os.Setenv(envSessionToken, terminalToken()+"-"+nanoID(rotateTokenLen))
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, *fRoom, *fRole, true, false)
	if err != nil {
		return err
	}
	created, owner, err := st.Join(id.room, id.sessionID, id.userID, id.role)
	if err != nil {
		return err
	}
	_ = owner
	verb := "joined"
	if created {
		verb = "created + joined"
	}
	fmt.Printf("registered session %s (user %s) — %s room %q as %s\n",
		id.sessionID, id.username, verb, id.room, id.role)
	return nil
}

func cmdJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("name", "", "username override")
	fRoom := fs.String("room", "", "room to join")
	fRole := fs.String("role", "", "role in the room")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, *fRoom, *fRole, true, false)
	if err != nil {
		return err
	}
	created, owner, err := st.Join(id.room, id.sessionID, id.userID, id.role)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("created room %q and joined as %s (role %s, you are owner)\n", id.room, id.username, id.role)
	} else {
		fmt.Printf("joined room %q as %s (role %s, owner %s)\n", id.room, id.username, id.role, owner)
	}
	return nil
}

func cmdLeave(args []string) error {
	fs := flag.NewFlagSet("leave", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("name", "", "username override")
	fRoom := fs.String("room", "", "room to leave")
	all := fs.Bool("all", false, "remove this session from every room")
	purge := fs.Bool("purge", false, "also delete this session's inbox")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := resolveIdentity(st, *fUser, *fName, *fRoom, "", true)
	if err != nil {
		return err
	}
	if *all {
		rooms, err := st.LeaveSession(id.sessionID, *purge)
		if err != nil {
			return err
		}
		fmt.Printf("removed session %s (left %d room(s)%s)\n", id.sessionID, rooms, purgeNote(*purge))
		return nil
	}
	existed, purged, err := st.LeaveRoom(id.room, id.sessionID, *purge)
	if err != nil {
		return err
	}
	if !existed {
		fmt.Printf("session %s was not a member of %q (nothing to leave)\n", id.sessionID, id.room)
		return nil
	}
	if *purge {
		fmt.Printf("left room %q and purged %d message(s)\n", id.room, purged)
	} else {
		fmt.Printf("left room %q (inbox kept)\n", id.room)
	}
	return nil
}

func purgeNote(purge bool) string {
	if purge {
		return ", inbox purged"
	}
	return ""
}

func cmdPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	olderThan := fs.Duration("older-than", 30*time.Minute, "remove sessions not seen within this window")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	removed, err := st.Prune(time.Now().Add(-*olderThan))
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		fmt.Printf("(nothing to prune — no sessions idle longer than %s)\n", *olderThan)
		return nil
	}
	fmt.Printf("pruned %d stale session(s): %s\n", len(removed), strings.Join(removed, ", "))
	return nil
}

// resetScope names what `reset` deletes. Closed set → constants (commandment #1).
const (
	resetSessions = "sessions"
	resetMessages = "messages"
	resetAll      = "all"
)

func cmdReset(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lorewire reset sessions|messages|all [--yes]")
	}
	scope := args[0]
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	yes := fs.Bool("yes", false, "actually delete (without this, only a preview is shown)")
	user := fs.String("user", "", "for `reset sessions`: only this username's sessions")
	me := fs.Bool("me", false, "for `reset sessions`: only my sessions (current folder's identity)")
	fs.Parse(args[1:])

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	users, sessions, rooms, messages, err := st.Counts()
	if err != nil {
		return err
	}

	// Resolve an optional session target (--user / --me), which scopes
	// `reset sessions` to one user instead of everyone.
	targetID, targetName := "", ""
	if *me {
		id, name, err := currentUserID(st)
		if err != nil {
			return err
		}
		targetID, targetName = id, name
	} else if *user != "" {
		id, ok, err := st.UserByName(*user)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no user named %q", *user)
		}
		targetID, targetName = id, *user
	}
	if targetID != "" && scope != resetSessions {
		return fmt.Errorf("--user/--me only apply to `reset sessions`")
	}

	// Describe the blast radius for this scope, so the preview and the action
	// agree on exactly what will be removed.
	var summary string
	switch scope {
	case resetSessions:
		if targetID != "" {
			mine, err := st.Sessions(targetID)
			if err != nil {
				return err
			}
			summary = fmt.Sprintf("%d session(s) belonging to %q (everything else kept)", len(mine), targetName)
		} else {
			summary = fmt.Sprintf("%d session(s) and their room memberships (users, rooms, messages kept)", sessions)
		}
	case resetMessages:
		summary = fmt.Sprintf("%d message(s) (users, rooms, sessions kept)", messages)
	case resetAll:
		summary = fmt.Sprintf("EVERYTHING — %d user(s), %d room(s), %d session(s), %d message(s)",
			users, rooms, sessions, messages)
	default:
		return fmt.Errorf("unknown reset scope %q (use sessions|messages|all)", scope)
	}

	// Confirmation gate: destructive, so require --yes; otherwise just preview.
	if !*yes {
		fmt.Printf("would delete: %s\n", summary)
		hint := "lorewire reset " + scope
		if targetName != "" {
			hint += " --user " + targetName
		}
		fmt.Printf("re-run with --yes to proceed: %s --yes\n", hint)
		return nil
	}

	switch scope {
	case resetSessions:
		if targetID != "" {
			n, err := st.DeleteSessionsForOwner(targetID)
			if err != nil {
				return err
			}
			fmt.Printf("deleted %d session(s) of %q\n", n, targetName)
			return nil
		}
		n, err := st.DeleteAllSessions()
		if err != nil {
			return err
		}
		fmt.Printf("deleted %d session(s)\n", n)
	case resetMessages:
		n, err := st.DeleteAllMessages()
		if err != nil {
			return err
		}
		fmt.Printf("deleted %d message(s)\n", n)
	case resetAll:
		if err := st.ResetAll(); err != nil {
			return err
		}
		fmt.Printf("reset complete — database wiped (%s)\n", summary)
	}
	return nil
}

func cmdRooms(args []string) error {
	fs := flag.NewFlagSet("rooms", flag.ExitOnError)
	me := fs.Bool("me", false, "only rooms I'm a member of (the current folder's identity)")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	owner := ""
	if *me {
		uid, _, err := currentUserID(st)
		if err != nil {
			return err
		}
		owner = uid
	}
	rooms, err := st.Rooms(owner)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(rooms)
	}
	for _, r := range rooms {
		owner := r.Owner
		if owner == "" {
			owner = "(system)"
		}
		fmt.Printf("%-16s  %d member(s)  owner %s\n", r.Name, r.Members, owner)
	}
	return nil
}

func cmdMembers(args []string) error {
	fs := flag.NewFlagSet("members", flag.ExitOnError)
	room := fs.String("room", "", "room (or $LOREWIRE_ROOM)")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	r := resolveRoomFlag(*room)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	members, err := st.Members(r)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(members)
	}
	if len(members) == 0 {
		fmt.Printf("(room %q has no members)\n", r)
		return nil
	}
	fmt.Printf("room %q:\n", r)
	for _, m := range members {
		flagStr := ""
		if m.Role == roleGuest {
			flagStr = "   ← needs a role"
		}
		fmt.Printf("  %-18s  %-8s  (%s)%s\n", m.SessionID, m.Role, m.Username, flagStr)
	}
	return nil
}

func cmdRole(args []string) error {
	if len(args) < 3 || args[0] != "set" {
		return fmt.Errorf("usage: lorewire role set NAME|SESSION ROLE [--room ROOM]")
	}
	target, role := args[1], args[2]
	fs := flag.NewFlagSet("role", flag.ExitOnError)
	room := fs.String("room", "", "room (or $LOREWIRE_ROOM)")
	fs.Parse(args[3:])
	r := resolveRoomFlag(*room)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	// target may be a session id (contains ~) or a username; for a username,
	// set the role on all that user's sessions in the room.
	sessions, err := targetSessions(st, r, target)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no member %q in room %q", target, r)
	}
	for _, sid := range sessions {
		if _, err := st.RoleSet(r, sid, role); err != nil {
			return err
		}
	}
	fmt.Printf("set role %q for %d session(s) of %q in room %q\n", role, len(sessions), target, r)
	return nil
}

// targetSessions maps a role-set target (session id or username) to session ids
// that are members of the room.
func targetSessions(st *Store, room, target string) ([]string, error) {
	if strings.Contains(target, "~") {
		return []string{target}, nil
	}
	members, err := st.Members(room)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range members {
		if m.Username == target {
			out = append(out, m.SessionID)
		}
	}
	return out, nil
}

func cmdSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	me := fs.Bool("me", false, "only MY sessions (the current folder's identity)")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	owner := ""
	if *me {
		uid, _, err := currentUserID(st)
		if err != nil {
			return err
		}
		owner = uid
	}
	sess, err := st.Sessions(owner)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(sess)
	}
	if len(sess) == 0 {
		fmt.Println("(no sessions registered)")
		return nil
	}
	// Group by user for a readable tree.
	var lastUser string
	for _, s := range sess {
		if s.Username != lastUser {
			fmt.Printf("%s (%s)\n", s.Username, s.OwnerID)
			lastUser = s.Username
		}
		loc := orDash(s.CWD)
		if s.GitBranch != "" {
			loc += " @" + s.GitBranch // show the branch inline — high-value for agents
		}
		fmt.Printf("  %-18s  %s  %s  seen %s\n", s.ID, loc, orDash(s.Client), humanAgo(s.LastSeen))
	}
	return nil
}

// ── Messaging ───────────────────────────────────────────────────────────────

func cmdSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("from", "", "sender username override")
	to := fs.String("to", "", "recipient: NAME, @ROLE, 'all', or a session id")
	room := fs.String("room", "", "room (or $LOREWIRE_ROOM)")
	msg := fs.String("msg", "", "message body (or pass positionally)")
	fs.Parse(args)
	if *to == "" {
		return fmt.Errorf("--to is required (NAME, @ROLE, 'all', or a session id)")
	}
	body := *msg
	if body == "" {
		body = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if body == "" {
		return fmt.Errorf("empty message")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, *room, "", false, true)
	if err != nil {
		return err
	}
	recipients, err := st.Send(id.room, id.sessionID, *to, body, msgKind, nil)
	if err != nil {
		return err
	}
	warnOrReport(recipients, id.room, *to)
	return nil
}

func cmdRequest(args []string) error {
	fs := flag.NewFlagSet("request", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("from", "", "requester username override")
	to := fs.String("to", "", "who to ask: @ROLE or NAME")
	room := fs.String("room", "", "room (or $LOREWIRE_ROOM)")
	msg := fs.String("msg", "", "what you need (or pass positionally)")
	fs.Parse(args)
	if *to == "" {
		return fmt.Errorf("--to is required (@ROLE or NAME to ask)")
	}
	body := *msg
	if body == "" {
		body = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if body == "" {
		return fmt.Errorf("empty request")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, *room, "", false, true)
	if err != nil {
		return err
	}
	recipients, err := st.Send(id.room, id.sessionID, *to, body, requestKind, nil)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		fmt.Fprintf(os.Stderr, "WARN: no recipient for %q in room %q — nobody to ask\n", *to, id.room)
		return nil
	}
	fmt.Printf("requested from %s in room %q — they answer with `lorewire grant ID --secret ...`\n",
		strings.Join(recipients, ", "), id.room)
	return nil
}

func cmdGrant(args []string) error {
	reqID, rest, err := parseIDArg(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("grant", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("from", "", "granter username override")
	secret := fs.String("secret", "", "the secret value to deliver (consume-once)")
	fs.Parse(rest)
	val := *secret
	if val == "" {
		val = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if val == "" {
		return fmt.Errorf("provide the secret via --secret VALUE")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, "", "", false, false)
	if err != nil {
		return err
	}
	requester, room, err := st.Grant(reqID, id.sessionID, val)
	if err != nil {
		return err
	}
	fmt.Printf("granted request #%d — secret delivered to %s in room %q (consume-once)\n", reqID, requester, room)
	return nil
}

func cmdDeny(args []string) error {
	reqID, rest, err := parseIDArg(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("deny", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("from", "", "granter username override")
	fs.Parse(rest)
	reason := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if reason == "" {
		reason = "(no reason given)"
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, "", "", false, false)
	if err != nil {
		return err
	}
	requester, room, err := st.Deny(reqID, id.sessionID, reason)
	if err != nil {
		return err
	}
	fmt.Printf("denied request #%d — %s notified in room %q\n", reqID, requester, room)
	return nil
}

func parseIDArg(args []string) (int64, []string, error) {
	if len(args) == 0 {
		return 0, nil, fmt.Errorf("missing request id (e.g. `lorewire grant 12 --secret ...`)")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid request id %q", args[0])
	}
	return id, args[1:], nil
}

func cmdRecv(args []string) error {
	fs := flag.NewFlagSet("recv", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("name", "", "username override")
	room := fs.String("room", "", "limit to one room (default: all your rooms)")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, "", "", false, true)
	if err != nil {
		return err
	}
	msgs, err := st.Recv(id.sessionID, *room)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(msgs)
	}
	printMessages(msgs, "(no new messages)")
	return nil
}

func cmdInbox(args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("name", "", "username override")
	room := fs.String("room", "", "limit to one room (default: all your rooms)")
	all := fs.Bool("all", false, "include already-read messages")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, "", "", false, true)
	if err != nil {
		return err
	}
	msgs, err := st.Inbox(id.sessionID, *room, *all)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(msgs)
	}
	printMessages(msgs, "(inbox empty)")
	return nil
}

func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	fUser := fs.String("user", "", "userId override")
	fName := fs.String("name", "", "username override")
	room := fs.String("room", "", "limit to one room (default: all your rooms)")
	interval := fs.Duration("interval", 2*time.Second, "poll interval")
	asJSON := fs.Bool("json", false, "output JSON per message")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := ctx(st, *fUser, *fName, "", "", true, true)
	if err != nil {
		return err
	}
	scope := "all rooms"
	if *room != "" {
		scope = "room " + *room
	}
	fmt.Fprintf(os.Stderr, "watching %s for %s every %s (Ctrl-C to stop)\n", scope, id.sessionID, *interval)
	for {
		msgs, err := st.Recv(id.sessionID, *room)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if *asJSON {
				printJSON(m)
			} else {
				fmt.Println(formatMessage(m))
			}
		}
		time.Sleep(*interval)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// resolveRoomFlag resolves a room for read-only commands (no identity needed):
// flag > $LOREWIRE_ROOM > .lorewire.jsonc > default.
func resolveRoomFlag(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv(envRoom); env != "" {
		return env
	}
	if cfg, err := loadConfig(); err == nil && cfg.Room != "" {
		return cfg.Room
	}
	return defaultRoom
}

func warnOrReport(recipients []string, room, to string) {
	if len(recipients) == 0 {
		fmt.Fprintf(os.Stderr, "WARN: no recipients for %q in room %q — message delivered to nobody\n", to, room)
		return
	}
	fmt.Printf("sent to %s in room %q\n", strings.Join(recipients, ", "), room)
}

func printMessages(msgs []Message, emptyMsg string) {
	if len(msgs) == 0 {
		fmt.Println(emptyMsg)
		return
	}
	for _, m := range msgs {
		fmt.Println(formatMessage(m))
	}
}

func formatMessage(m Message) string {
	tag := ""
	switch m.Kind {
	case requestKind:
		tag = fmt.Sprintf(" [request#%d]", m.ID)
	case secretKind:
		tag = " [secret]"
	case denyKind:
		tag = " [denied]"
	case grantKind:
		tag = " [grant]"
	}
	suffix := ""
	if m.ReadAt != nil {
		suffix = " (read)"
	}
	return fmt.Sprintf("[%s] %s/%s → %s%s: %s%s",
		m.CreatedAt.Local().Format("15:04:05"), m.Room, m.From, m.To, tag, m.Body, suffix)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func humanAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
