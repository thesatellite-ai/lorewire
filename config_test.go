package main

import (
	"strings"
	"testing"
)

// TestStripJSONC verifies the JSONC comment stripper leaves valid JSON and, in
// particular, never strips comment markers that appear INSIDE string values —
// the case that silently corrupts a config if gotten wrong.
func TestStripJSONC(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"no comments", `{"a":1}`, `{"a":1}`},
		{"line comment", "{\n// hi\n\"a\":1}", "{\n\n\"a\":1}"},
		{"trailing line comment", `{"a":1} // tail`, `{"a":1} `},
		{"block comment", `{/* x */"a":1}`, `{"a":1}`},
		{"marker inside string kept", `{"url":"http://x"}`, `{"url":"http://x"}`},
		{"slash-star inside string kept", `{"a":"/* not a comment */"}`, `{"a":"/* not a comment */"}`},
		{"escaped quote in string", `{"a":"he said \"hi\" //x"}`, `{"a":"he said \"hi\" //x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := string(stripJSONC([]byte(tc.in))); got != tc.want {
				t.Errorf("stripJSONC(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNewUserID checks the userId wire shape: the prefix plus a body of the
// configured length drawn only from the nanoid alphabet. Uniqueness across
// calls is spot-checked to guard against an accidentally-constant generator.
func TestNewUserID(t *testing.T) {
	id := newUserID()
	if !strings.HasPrefix(id, userIDPrefix) {
		t.Fatalf("newUserID() = %q; missing prefix %q", id, userIDPrefix)
	}
	body := strings.TrimPrefix(id, userIDPrefix)
	if len(body) != userIDNanoID {
		t.Errorf("id body %q length = %d; want %d", body, len(body), userIDNanoID)
	}
	for _, r := range body {
		if !strings.ContainsRune(nanoidAlphabet, r) {
			t.Errorf("id body %q has char %q outside the alphabet", body, r)
		}
	}
	seen := map[string]bool{}
	for range 100 {
		if v := newUserID(); seen[v] {
			t.Fatalf("newUserID() returned a duplicate: %q", v)
		} else {
			seen[v] = true
		}
	}
}

// TestSessionIDShape verifies a session id is "<username><sep><hash>" with the
// configured hash length, and is stable for a fixed terminal token (so every
// command in one terminal maps to the same session).
func TestSessionIDShape(t *testing.T) {
	t.Setenv(envSessionToken, "fixed-token-for-test")
	a := sessionID("bob")
	b := sessionID("bob")
	if a != b {
		t.Errorf("sessionID not stable for a fixed token: %q vs %q", a, b)
	}
	user, hash, found := strings.Cut(a, sessionSep)
	if !found {
		t.Fatalf("sessionID %q has no %q separator", a, sessionSep)
	}
	if user != "bob" {
		t.Errorf("sessionID username part = %q; want bob", user)
	}
	if len(hash) != sessionHashLen {
		t.Errorf("sessionID hash %q length = %d; want %d", hash, len(hash), sessionHashLen)
	}
	if sessionID("carol") == a {
		t.Errorf("different usernames produced the same session id %q", a)
	}
}
