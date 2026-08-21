package runinpopup

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// ipcMessage is what both directions of a test exchange carry.
type ipcMessage struct {
	Value int `json:"value"`
}

func marshalIpcMessage(t *testing.T, v ipcMessage) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling %+v: %v", v, err)
	}
	return string(b)
}

// collectIpcResults drains the exchange and hands back everything it carried,
// with the terminal error behind it.
func collectIpcResults[In, Out any](conn *JsonIpcConn[In, Out]) ([]Out, error) {
	var got []Out
	for v := range conn.Results() {
		got = append(got, v)
	}
	return got, conn.Wait()
}

// Launch-time input travels in the popup's command line, so the payload here is
// the argv AddPayload built: it echoes the value straight back.
func TestJsonIpcLauncher_Exec_launchTimeInput(t *testing.T) {
	backend := &shellBackend{}
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup: &PopupLauncher{Backend: backend},
		AddPayload: func(v ipcMessage, spec PopupSpec) PopupSpec {
			spec.Command = []string{"printf", "%s", marshalIpcMessage(t, v)}
			return spec
		},
	}

	conn, err := launcher.Exec(t.Context(), ipcMessage{Value: 7})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	got, err := collectIpcResults(conn)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if want := []ipcMessage{{Value: 7}}; !slices.Equal(got, want) {
		t.Errorf("results = %+v, want %+v", got, want)
	}
	// An exchange with nothing to stream leaves the payload's stdin where the
	// popup put it: on its terminal.
	if spec := backend.launched[0]; spec.Stdin != nil {
		t.Error("no input is streamed, so no stdin may be allocated for it")
	}
}

// A launcher without AddPayload streams its input instead, one JSON line per
// value, and the payload answers each of them.
func TestJsonIpcConn_Send(t *testing.T) {
	backend := &shellBackend{}
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup: &PopupLauncher{Backend: backend},
		// The payload echoes the two lines it is sent and leaves, which is what
		// ends the exchange.
		PartialSpec: PopupSpec{Command: []string{"head", "-n", "2"}},
	}

	conn, err := launcher.Exec(t.Context(), ipcMessage{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	for _, v := range []ipcMessage{{Value: 1}, {Value: 2}} {
		if err := conn.Send(t.Context(), v); err != nil {
			t.Fatalf("Send(%+v): %v", v, err)
		}
	}

	got, err := collectIpcResults(conn)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if want := []ipcMessage{{Value: 1}, {Value: 2}}; !slices.Equal(got, want) {
		t.Errorf("results = %+v, want %+v: every value must reach the payload", got, want)
	}
	if spec := backend.launched[0]; spec.Stdin == nil {
		t.Error("a streamed exchange must have a stdin to stream over")
	}
}

// The input went into the command line, so there is no stream to send on and
// saying so beats writing into a stdin the payload never got.
func TestJsonIpcConn_Send_withoutAnInputStream(t *testing.T) {
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup: &PopupLauncher{Backend: &shellBackend{}},
		AddPayload: func(v ipcMessage, spec PopupSpec) PopupSpec {
			spec.Command = []string{"printf", "%s", marshalIpcMessage(t, v)}
			return spec
		},
	}

	conn, err := launcher.Exec(t.Context(), ipcMessage{Value: 1})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	sendErr := conn.Send(t.Context(), ipcMessage{Value: 2})
	if _, err := collectIpcResults(conn); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if sendErr == nil || !strings.Contains(sendErr.Error(), "no input stream") {
		t.Fatalf("Send = %v, want it to report that the exchange streams nothing", sendErr)
	}
}

// Output the payload writes but no Out can be made of ends the exchange, and
// Wait is where that shows up.
func TestJsonIpcConn_Wait_reportsWhatEndedTheExchange(t *testing.T) {
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup:       &PopupLauncher{Backend: &shellBackend{}},
		PartialSpec: PopupSpec{Script: `printf 'not json at all'`},
	}

	conn, err := launcher.Exec(t.Context(), ipcMessage{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	got, err := collectIpcResults(conn)
	if len(got) != 0 {
		t.Errorf("results = %+v, want nothing decodable", got)
	}
	if err == nil || !strings.Contains(err.Error(), "reading the popup payload's output") {
		t.Fatalf("Wait = %v, want the decoding failure reported", err)
	}
}

// The payload keeps answering after a launcher that returns early, so the
// exchange must not end with it.
func TestJsonIpcConn_launcherExitsFirst(t *testing.T) {
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup:       &PopupLauncher{Backend: &detachedBackend{shellBackend: &shellBackend{}}},
		PartialSpec: PopupSpec{Script: `sleep 0.2; printf '{"value":3}'`},
	}

	conn, err := launcher.Exec(t.Context(), ipcMessage{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	got, err := collectIpcResults(conn)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if want := []ipcMessage{{Value: 3}}; !slices.Equal(got, want) {
		t.Errorf("results = %+v, want %+v", got, want)
	}
}

// A popup that never appears is not a rendezvous to wait out: the launcher's
// failure ends the exchange well before the startup bound would.
func TestJsonIpcConn_launcherFailureEndsTheRendezvous(t *testing.T) {
	launchErr := errors.New("no pane could be opened")
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup: &PopupLauncher{
			Backend:        &failingLauncherBackend{shellBackend: &shellBackend{}, err: launchErr},
			StartupTimeout: time.Minute,
		},
		PartialSpec: PopupSpec{Script: "true"},
	}

	conn, err := launcher.Exec(t.Context(), ipcMessage{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	start := time.Now()
	_, err = collectIpcResults(conn)
	elapsed := time.Since(start)

	if !errors.Is(err, launchErr) {
		t.Fatalf("Wait = %v, want it to wrap %v", err, launchErr)
	}
	if elapsed > 10*time.Second {
		t.Errorf("the exchange waited %v, want the launcher's failure to end it", elapsed)
	}
}

func TestJsonIpcLauncher_Exec_prepareFailure(t *testing.T) {
	prepareErr := errors.New("de-zoom failed")
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup:       &PopupLauncher{Backend: &shellBackend{prepareErr: prepareErr}},
		PartialSpec: PopupSpec{Script: "true"},
	}

	if _, err := launcher.Exec(t.Context(), ipcMessage{}); !errors.Is(err, prepareErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, prepareErr)
	}
}

func TestJsonIpcLauncher_Exec_launchFailure(t *testing.T) {
	launchErr := errors.New("no such multiplexer")
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{
		Popup:       &PopupLauncher{Backend: &shellBackend{launchErr: launchErr}},
		PartialSpec: PopupSpec{Script: "true"},
	}

	if _, err := launcher.Exec(t.Context(), ipcMessage{}); !errors.Is(err, launchErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, launchErr)
	}
}

func TestJsonIpcLauncher_Exec_popupIsRequired(t *testing.T) {
	launcher := &JsonIpcLauncher[ipcMessage, ipcMessage]{}
	if _, err := launcher.Exec(context.Background(), ipcMessage{}); err == nil {
		t.Fatal("an exchange without a popup launcher must be rejected")
	}
}
