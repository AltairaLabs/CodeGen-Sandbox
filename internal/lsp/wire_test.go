package lsp

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestWriteMessage_FramesPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMessage(&buf, map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "Content-Length: ") {
		t.Fatalf("no Content-Length prefix: %q", got)
	}
	if !strings.Contains(got, "\r\n\r\n") {
		t.Fatalf("missing header terminator: %q", got)
	}
	if !strings.Contains(got, `{"hello":"world"}`) {
		t.Fatalf("missing payload: %q", got)
	}
}

func TestReadMessage_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMessage(&buf, map[string]int{"n": 42}); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	got, err := readMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(got) != `{"n":42}` {
		t.Fatalf("unexpected payload: %q", got)
	}
}

func TestReadMessage_MissingContentLength(t *testing.T) {
	buf := bytes.NewBufferString("\r\n")
	if _, err := readMessage(bufio.NewReader(buf)); err == nil {
		t.Fatal("expected error for missing Content-Length")
	}
}

func TestReadMessage_IgnoresOtherHeaders(t *testing.T) {
	payload := `{"ok":true}`
	frame := "Content-Type: application/json\r\nContent-Length: " +
		itoa(len(payload)) + "\r\n\r\n" + payload
	got, err := readMessage(bufio.NewReader(strings.NewReader(frame)))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("payload mismatch: %q", got)
	}
}

// itoa: avoid pulling strconv just for tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
