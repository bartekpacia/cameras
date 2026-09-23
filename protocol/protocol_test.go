package protocol

import (
	"bytes"
	"testing"
	"time"
)

func TestSofiaHash(t *testing.T) {
	// Checked against a live login on the Kenik at 192.168.1.3.
	if got := SofiaHash("admin1410"); got != "ziHmEF4y" {
		t.Fatalf("SofiaHash = %q", got)
	}
}

func TestLoginDigest(t *testing.T) {
	// First (token, hash) pair captured from the iOS app.
	const token = "LTEwNjI3MzEzMzY1NTYxMDQ5MTQ4ODg0Mw=="
	const want = "d9e0f507db4451949dcba3b910e0e039"
	if got := LoginDigest(token, "admin1410"); got != want {
		t.Fatalf("LoginDigest = %s", got)
	}
}

func TestParseRecording(t *testing.T) {
	id := "qvfs_0_0_0_6_376_34_26_9_22_0_0_8_26_9_22_0_0_39"
	rec, err := ParseRecording(id)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 22, 0, 0, 8, 0, time.Local)
	end := time.Date(2026, 9, 22, 0, 0, 39, 0, time.Local)
	if !rec.Start.Equal(start) || !rec.End.Equal(end) {
		t.Fatalf("got %s .. %s", rec.Start, rec.End)
	}
	if rec.Duration() != 31*time.Second {
		t.Fatalf("duration %s", rec.Duration())
	}
}

func TestPackClockRoundTrip(t *testing.T) {
	in := time.Date(2026, 9, 16, 0, 2, 14, 0, time.Local)
	raw := packClock(in)
	want := []byte{0xea, 0x07, 0x09, 0x00, 0x10, 0x00, 0x02, 0x0e}
	if !bytes.Equal(raw, want) {
		t.Fatalf("pack = %x", raw)
	}
	out, ok := unpackClock(raw)
	if !ok || !out.Equal(in) {
		t.Fatalf("unpack %s ok=%v", out, ok)
	}
}

func TestParseRecordingRejectsGarbage(t *testing.T) {
	if _, err := ParseRecording("not-a-file"); err == nil {
		t.Fatal("expected error")
	}
}
