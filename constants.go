package main

// Rule (Go commandment #1 / no bare strings): every value with a closed set of
// choices — a structural delimiter, an id prefix, an environment-variable name,
// an addressing token, a value-source label, a client kind — is defined once
// here and referenced everywhere. A typo becomes a compile error and a rename
// is a single edit. Free-form text (message bodies, usernames) is NOT here.

// Identity & session-id format. The session id is `username + sessionSep +
// <sessionHashLen hex of the terminal-token hash>` (e.g. "bob~a1f2"), so these
// four values jointly define the wire shape of an id — changing one without the
// others silently breaks id parsing/derivation. Kept together for that reason.
const (
	userIDPrefix   = "usr_" // prefix on every userId: usr_<nanoid>
	userIDNanoID   = 7      // nanoid body length for a userId
	sessionSep     = "~"    // separates username from terminal hash in a session id
	sessionHashLen = 4      // hex chars of the terminal-token hash kept in a session id
	rotateTokenLen = 4      // extra nanoid chars appended by `register --new`
)

// Environment-variable names. Referenced from both config.go and main.go; a
// constant keeps the two files from drifting on a typo. (LOREWIRE_BIN is used
// only by the shell hooks, never by Go, so it is intentionally absent.)
const (
	envDB           = "LOREWIRE_DB"
	envUserID       = "LOREWIRE_USER_ID"
	envName         = "LOREWIRE_NAME"
	envRoom         = "LOREWIRE_ROOM"
	envRole         = "LOREWIRE_ROLE"
	envSession      = "LOREWIRE_SESSION"
	envSessionToken = "LOREWIRE_SESSION_TOKEN"
	envClient       = "LOREWIRE_CLIENT"
)

// Addressing tokens for `--to` (see Store.resolveRecipients). A recipient that
// equals addrAll/addrAllStar broadcasts; one prefixed with addrRolePrefix is a
// role; one containing sessionSep is a literal session id; anything else is a
// username.
const (
	addrAll        = "all"
	addrAllStar    = "*"
	addrRolePrefix = "@"
)

// Value-source labels reported by `whoami` so a user can see WHERE each
// effective value came from. Closed set → constants.
const (
	srcFlag    = "flag"
	srcEnv     = "env"
	srcConfig  = "config"
	srcDefault = "default"
	srcAuto    = "auto"
)

// Client kinds recorded on a session (best-effort detection of what launched
// it). Stored for display only.
const (
	clientClaudeCode = "claude-code"
	clientShell      = "shell"
)
