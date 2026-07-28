package runinpopup

import "strings"

// PinentryUserData is the parsed form of the PINENTRY_USER_DATA environment
// variable — the wire format gpg-agent forwards verbatim from the invoking
// environment to pinentry. Fields are positional and colon-separated:
//
//	KIND:path:session_id:client_id:session_meta[:rest...]
//
// e.g. "TMUX_POPUP:/usr/bin/tmux:$1:%1:/run/user/1000/tmux-1000/default,111,0".
//
// Every field is optional: a short value simply leaves the trailing fields
// empty, and callers validate the fields they actually need.
type PinentryUserData struct {
	// Kind names the host program, "TMUX_POPUP" or "ZELLIJ_POPUP". A "_DEBUG"
	// suffix additionally requests debug logging.
	Kind string
	// Path is the multiplexer binary to invoke (tmux / zellij).
	Path string
	// SessionId is the multiplexer session hosting the popup.
	SessionId string
	// ClientId is the multiplexer client to display the popup on. tmux only —
	// zellij cannot target a client.
	ClientId string
	// SessionMeta is the multiplexer's $TMUX value,
	// "socket_path,server_pid,session_index".
	SessionMeta string
	// Rest holds any further colon-separated fields, kept so an unrecognized
	// tail is visible to the caller instead of silently dropped.
	Rest []string
}

// Debug reports whether the kind asks for debug logging, i.e. carries the
// "_DEBUG" suffix ("TMUX_POPUP_DEBUG"). It looks at the kind and nothing else:
// callers that honor further switches — an environment variable, a flag — must
// OR them in themselves.
func (p PinentryUserData) Debug() bool {
	return strings.HasSuffix(strings.ToUpper(strings.TrimSpace(p.Kind)), "_DEBUG")
}

// ParsePinentryUserData parses the PINENTRY_USER_DATA wire format. It takes the
// value as an argument and never reads the environment itself, so the caller
// decides where the string comes from (env, flag, test fixture).
func ParsePinentryUserData(pinentryUserData string) PinentryUserData {
	var p PinentryUserData
	s := strings.Split(strings.TrimSpace(pinentryUserData), ":")
	if len(s) > 0 {
		p.Kind = s[0]
	}
	if len(s) > 1 {
		p.Path = s[1]
	}
	if len(s) > 2 {
		p.SessionId = s[2]
	}
	if len(s) > 3 {
		p.ClientId = s[3]
	}
	if len(s) > 4 {
		p.SessionMeta = s[4]
	}
	if len(s) > 5 {
		p.Rest = s[5:]
	}
	return p
}
