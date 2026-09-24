package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{
		Addr:     "127.0.0.1:1",
		User:     "admin",
		Password: "secret",
		Channel:  1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHealthz(t *testing.T) {
	rr := httptest.NewRecorder()
	testServer(t).Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestListRejectsBadDate(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/recordings?date=yesterday", nil)
	testServer(t).Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rr.Code, rr.Body)
	}
}

func TestOneParsesIDWithoutDialing(t *testing.T) {
	rr := httptest.NewRecorder()
	id := "qvfs_0_0_0_6_376_34_26_9_22_0_0_8_26_9_22_0_0_39"
	req := httptest.NewRequest(http.MethodGet, "/recordings/"+id, nil)
	testServer(t).Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body)
	}
	var got struct {
		DurationSeconds int `json:"duration_seconds"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DurationSeconds != 31 {
		t.Fatalf("duration %d", got.DurationSeconds)
	}
}

func TestVideoRejectsBadID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/recordings/nope/video", nil)
	testServer(t).Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Fatal("expected error")
	}
}
