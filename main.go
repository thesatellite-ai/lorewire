// Command lorewire is a tiny message bus that lets multiple agent sessions
// (Claude Code, other agents, or plain scripts — separate processes) talk to
// each other. Each session registers a name, then sends and receives messages
// through a shared SQLite file. Messages are scoped to rooms (default "main",
// so rooms are optional), members carry roles, and a request/grant flow lets an
// agent ask a role-holder for a secret (e.g. an API key). It is pull-based (a
// session reads its inbox when it wants); `lorewire watch` provides a blocking
// poll loop that the push/hook layer builds on.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
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
	case "register":
		err = cmdRegister(args)
	case "join":
		err = cmdJoin(args)
	case "leave":
		err = cmdLeave(args)
	case "prune":
		err = cmdPrune(args)
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
	fmt.Print(`lorewire — message bus for talking between agent/Claude Code sessions

Presence & rooms:
  lorewire register --name NAME                register (joins default room "main")
  lorewire join --room ROOM [--role ROLE]      join/create a room with a role
  lorewire leave [--room ROOM] [--purge]       leave one room (--purge drops its inbox)
  lorewire leave --all [--purge]               unregister from every room + session
  lorewire prune [--older-than 30m]            remove sessions not seen since the cutoff
  lorewire rooms [--json]                      list rooms and member counts
  lorewire members [--room ROOM] [--json]      list a room's members and roles
  lorewire role set NAME ROLE [--room ROOM]    change a member's role
  lorewire sessions [--json]                   list all live sessions

Messaging (room resolves: --room flag > $LOREWIRE_ROOM > "main"):
  lorewire send [--room ROOM] --to NAME|@ROLE|all MSG    send a message
  lorewire recv [--room ROOM] [--json]         read + consume unread messages
  lorewire inbox [--room ROOM] [--all] [--json]  show messages without consuming
  lorewire watch [--room ROOM] [--interval 2s] [--json]  stream new messages

Requesting secrets (e.g. an API key from whoever holds a role):
  lorewire request [--room ROOM] --to @ROLE|NAME MSG     ask; recipients see [request#ID]
  lorewire grant ID --secret VALUE             answer a request with a consume-once secret
  lorewire deny  ID REASON                      decline a request

Identity resolves from --name/--from, else $LOREWIRE_NAME.
Database: $LOREWIRE_DB, else ~/.lorewire/lorewire.db

Examples:
  export LOREWIRE_NAME=alice && lorewire register
  lorewire send --to bob "can you take the frontend?"          # in "main"
  lorewire join --room project-x --role cto
  lorewire send --room project-x --to @dev "standup in 5"      # address a role
  lorewire request --room project-x --to @cto "OpenAI API key"
  lorewire grant 12 --secret "sk-..."
`)
}

// resolveName picks the identity from an explicit flag value or $LOREWIRE_NAME,
// erroring if neither is set so a mistyped command can't act as the wrong peer.
func resolveName(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if env := os.Getenv("LOREWIRE_NAME"); env != "" {
		return env, nil
	}
	return "", fmt.Errorf("no session name: pass --name/--from or set $LOREWIRE_NAME")
}

// resolveRoom picks the room from --room, else $LOREWIRE_ROOM, else the default
// room. This is what makes rooms optional — omit everything and you're in "main".
func resolveRoom(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("LOREWIRE_ROOM"); env != "" {
		return env
	}
	return defaultRoom
}

func cmdRegister(args []string) error {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	name := fs.String("name", "", "session name")
	fs.Parse(args)
	n, err := resolveName(*name)
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Register(n); err != nil {
		return err
	}
	fmt.Printf("registered %q (in room %q)\n", n, defaultRoom)
	return nil
}

func cmdJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	name := fs.String("name", "", "session name")
	room := fs.String("room", "", "room to join (or $LOREWIRE_ROOM)")
	role := fs.String("role", "", "your role in the room (default: guest)")
	fs.Parse(args)
	n, err := resolveName(*name)
	if err != nil {
		return err
	}
	r := resolveRoom(*room)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	created, owner, err := st.Join(r, n, *role)
	if err != nil {
		return err
	}
	roleShown := *role
	if roleShown == "" {
		roleShown = roleGuest
	}
	if created {
		fmt.Printf("created room %q and joined as %q (role %s, you are owner)\n", r, n, roleShown)
	} else {
		fmt.Printf("joined room %q as %q (role %s, owner %s)\n", r, n, roleShown, owner)
	}
	return nil
}

func cmdLeave(args []string) error {
	fs := flag.NewFlagSet("leave", flag.ExitOnError)
	name := fs.String("name", "", "session name to unregister")
	room := fs.String("room", "", "room to leave (or $LOREWIRE_ROOM)")
	all := fs.Bool("all", false, "leave every room and remove the session entirely")
	purge := fs.Bool("purge", false, "also delete this session's inbox")
	fs.Parse(args)
	n, err := resolveName(*name)
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	if *all {
		rooms, err := st.LeaveAll(n, *purge)
		if err != nil {
			return err
		}
		fmt.Printf("unregistered %q (left %d room(s), session removed%s)\n", n, rooms, purgeNote(*purge))
		return nil
	}

	r := resolveRoom(*room)
	existed, purged, err := st.Leave(r, n, *purge)
	if err != nil {
		return err
	}
	if !existed {
		fmt.Printf("%q was not a member of %q (nothing to leave)\n", n, r)
		return nil
	}
	if *purge {
		fmt.Printf("left room %q as %q and purged %d message(s)\n", r, n, purged)
	} else {
		fmt.Printf("left room %q as %q (inbox kept)\n", r, n)
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

func cmdRooms(args []string) error {
	fs := flag.NewFlagSet("rooms", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	rooms, err := st.Rooms()
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
	r := resolveRoom(*room)
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
		flag := ""
		if m.Role == roleGuest {
			flag = "   ← needs a role (lorewire role set " + m.Name + " <role>)"
		}
		fmt.Printf("  %-16s  %s%s\n", m.Name, m.Role, flag)
	}
	return nil
}

func cmdRole(args []string) error {
	// Usage: lorewire role set NAME ROLE [--room ROOM]
	if len(args) < 3 || args[0] != "set" {
		return fmt.Errorf("usage: lorewire role set NAME ROLE [--room ROOM]")
	}
	target, role := args[1], args[2]
	fs := flag.NewFlagSet("role", flag.ExitOnError)
	room := fs.String("room", "", "room (or $LOREWIRE_ROOM)")
	fs.Parse(args[3:])
	r := resolveRoom(*room)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	existed, err := st.RoleSet(r, target, role)
	if err != nil {
		return err
	}
	if !existed {
		return fmt.Errorf("%q is not a member of room %q", target, r)
	}
	fmt.Printf("set %q role to %q in room %q\n", target, role, r)
	return nil
}

func cmdSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	sess, err := st.Sessions()
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
	for _, s := range sess {
		fmt.Printf("%-16s  last seen %s\n", s.Name, humanAgo(s.LastSeen))
	}
	return nil
}

func cmdSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	from := fs.String("from", "", "sender name")
	to := fs.String("to", "", "recipient: NAME, @ROLE, or 'all'")
	room := fs.String("room", "", "room (or $LOREWIRE_ROOM)")
	msg := fs.String("msg", "", "message body (or pass positionally)")
	fs.Parse(args)
	f, err := resolveName(*from)
	if err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required (NAME, @ROLE, or 'all')")
	}
	body := *msg
	if body == "" {
		body = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if body == "" {
		return fmt.Errorf("empty message")
	}
	r := resolveRoom(*room)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	recipients, err := st.Send(r, f, *to, body, msgKind, nil)
	if err != nil {
		return err
	}
	warnOrReport(recipients, r, *to)
	return nil
}

func cmdRequest(args []string) error {
	fs := flag.NewFlagSet("request", flag.ExitOnError)
	from := fs.String("from", "", "requester name")
	to := fs.String("to", "", "who to ask: @ROLE or NAME")
	room := fs.String("room", "", "room (or $LOREWIRE_ROOM)")
	msg := fs.String("msg", "", "what you need (or pass positionally)")
	fs.Parse(args)
	f, err := resolveName(*from)
	if err != nil {
		return err
	}
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
	r := resolveRoom(*room)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	recipients, err := st.Send(r, f, *to, body, requestKind, nil)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		fmt.Fprintf(os.Stderr, "WARN: no recipient for %q in room %q — nobody to ask\n", *to, r)
		return nil
	}
	fmt.Printf("requested from %s in room %q — they'll see it as [request#ID]; they answer with `lorewire grant ID --secret ...`\n", strings.Join(recipients, ", "), r)
	return nil
}

func cmdGrant(args []string) error {
	// The request id is the leading positional arg; Go's flag parser stops at
	// the first non-flag token, so pull the id off before parsing flags.
	id, rest, err := parseIDArg(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("grant", flag.ExitOnError)
	from := fs.String("from", "", "granter name")
	secret := fs.String("secret", "", "the secret value to deliver (consume-once)")
	fs.Parse(rest)
	f, err := resolveName(*from)
	if err != nil {
		return err
	}
	val := *secret
	if val == "" {
		val = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if val == "" {
		return fmt.Errorf("provide the secret via --secret VALUE (or positionally after the id)")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	requester, room, err := st.Grant(id, f, val)
	if err != nil {
		return err
	}
	fmt.Printf("granted request #%d — secret delivered to %q in room %q (consume-once)\n", id, requester, room)
	return nil
}

func cmdDeny(args []string) error {
	id, rest, err := parseIDArg(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("deny", flag.ExitOnError)
	from := fs.String("from", "", "granter name")
	fs.Parse(rest)
	f, err := resolveName(*from)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if reason == "" {
		reason = "(no reason given)"
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	requester, room, err := st.Deny(id, f, reason)
	if err != nil {
		return err
	}
	fmt.Printf("denied request #%d — %q notified in room %q\n", id, requester, room)
	return nil
}

// parseIDArg pulls a leading numeric request id off the positional args and
// returns the rest (used by grant/deny where the id comes before the payload).
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
	name := fs.String("name", "", "your session name")
	room := fs.String("room", "", "limit to one room (default: all your rooms)")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	n, err := resolveName(*name)
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	// Empty room string means "all rooms"; --room narrows it.
	msgs, err := st.Recv(n, *room)
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
	name := fs.String("name", "", "your session name")
	room := fs.String("room", "", "limit to one room (default: all your rooms)")
	all := fs.Bool("all", false, "include already-read messages")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	n, err := resolveName(*name)
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	msgs, err := st.Inbox(n, *room, *all)
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
	name := fs.String("name", "", "your session name")
	room := fs.String("room", "", "limit to one room (default: all your rooms)")
	interval := fs.Duration("interval", 2*time.Second, "poll interval")
	asJSON := fs.Bool("json", false, "output JSON per message")
	fs.Parse(args)
	n, err := resolveName(*name)
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	scope := "all rooms"
	if *room != "" {
		scope = "room " + *room
	}
	fmt.Fprintf(os.Stderr, "watching %s for %q every %s (Ctrl-C to stop)\n", scope, n, *interval)
	for {
		msgs, err := st.Recv(n, *room)
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

// warnOrReport prints delivery results, warning loudly on an empty fan-out so a
// broadcast/@role to nobody doesn't masquerade as success.
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

// formatMessage renders one message as:
//
//	[15:04] room/alice → bob: body
//
// with a [kind#id] tag for non-plain messages (request/grant/deny) and a
// (read) suffix for already-consumed rows shown by inbox.
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

// humanAgo renders a coarse "how long ago" for the sessions list.
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
