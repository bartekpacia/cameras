// Package server is an HTTP facade over the recorder protocol.
//
//	GET /healthz
//	GET /recordings?date=YYYY-MM-DD
//	GET /recordings?from=...&to=...&channel=1
//	GET /recordings/{id}
//	GET /recordings/{id}/video
//
// /video pulls the clip off the recorder and returns an MP4 attachment.
// One download runs at a time: the recorder spends the login session on
// the first playback claim.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/bartekpacia/cameras/protocol"
)

// Config is everything the facade needs to reach the recorder.
type Config struct {
	// Addr is host:port of the recorder control port, usually :5801.
	Addr     string
	User     string
	Password string
	// Channel is the search channel used when the request does not set one.
	// The uCloud Cam app sends 1. Channel 0 ignores the time range.
	Channel int
}

// Server serves the REST API. A zero Server is not usable; call New.
type Server struct {
	cfg Config
	dvr protocol.Client
	log *slog.Logger

	// mu serializes recorder sessions. A session can claim one file.
	mu sync.Mutex
}

// New checks cfg and returns a server. It does not dial the recorder.
func New(cfg Config, log *slog.Logger) (*Server, error) {
	if cfg.Addr == "" || cfg.User == "" || cfg.Password == "" {
		return nil, errors.New("dvr address, user, and password are required")
	}
	if cfg.Channel < 0 || cfg.Channel > 255 {
		return nil, fmt.Errorf("channel %d out of range", cfg.Channel)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg: cfg,
		dvr: protocol.Client{Addr: cfg.Addr},
		log: log,
	}, nil
}

// Handler is the REST API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /recordings", s.list)
	mux.HandleFunc("GET /recordings/{id}", s.one)
	mux.HandleFunc("GET /recordings/{id}/video", s.video)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	start, end, err := timeRange(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	channel, err := s.channel(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	sess, err := s.dvr.Login(ctx, s.cfg.User, s.cfg.Password)
	if err != nil {
		s.log.Error("login", "op", "list", "err", err)
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	defer sess.Close()

	recs, err := sess.List(ctx, start, end, channel)
	if err != nil {
		s.log.Error("list", "err", err)
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	out := make([]recordingJSON, 0, len(recs))
	for _, rec := range recs {
		out = append(out, toJSON(rec))
	}
	writeJSON(w, http.StatusOK, listResponse{Recordings: out})
}

func (s *Server) one(w http.ResponseWriter, r *http.Request) {
	rec, err := protocol.ParseRecording(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toJSON(rec))
}

func (s *Server) video(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := protocol.ParseRecording(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		writeErr(w, http.StatusInternalServerError, errors.New("ffmpeg is not installed"))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.fetchMP4(r.Context(), id)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		s.log.Error("download", "id", id, "err", err)
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	defer os.Remove(path)

	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	name := rec.Start.Format("2006-01-02_150405") + ".mp4"
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// fetchMP4 logs in, saves the Annex B stream, and remuxes it to a temp MP4.
// The caller deletes the returned path.
func (s *Server) fetchMP4(ctx context.Context, id string) (string, error) {
	h264, err := os.CreateTemp("", "dvr-*.h264")
	if err != nil {
		return "", err
	}
	h264Name := h264.Name()
	defer os.Remove(h264Name)

	sess, err := s.dvr.Login(ctx, s.cfg.User, s.cfg.Password)
	if err != nil {
		h264.Close()
		return "", err
	}
	defer sess.Close()

	dlErr := sess.Download(ctx, id, h264)
	closeErr := h264.Close()
	if dlErr != nil {
		return "", dlErr
	}
	if closeErr != nil {
		return "", closeErr
	}

	mp4, err := os.CreateTemp("", "dvr-*.mp4")
	if err != nil {
		return "", err
	}
	mp4Name := mp4.Name()
	if err := mp4.Close(); err != nil {
		os.Remove(mp4Name)
		return "", err
	}

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-v", "error",
		"-i", h264Name,
		"-c", "copy",
		mp4Name,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(mp4Name)
		msg := stderr.String()
		if msg == "" {
			return "", fmt.Errorf("ffmpeg: %w", err)
		}
		return "", fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return mp4Name, nil
}

type listResponse struct {
	Recordings []recordingJSON `json:"recordings"`
}

type recordingJSON struct {
	ID              string    `json:"id"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	DurationSeconds int       `json:"duration_seconds"`
}

func toJSON(rec protocol.Recording) recordingJSON {
	sec := int(rec.Duration() / time.Second)
	return recordingJSON{
		ID:              rec.ID,
		Start:           rec.Start,
		End:             rec.End,
		DurationSeconds: sec,
	}
}

type errorBody struct {
	Error string `json:"error"`
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, errorBody{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) channel(r *http.Request) (int, error) {
	q := r.URL.Query().Get("channel")
	if q == "" {
		return s.cfg.Channel, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 0 || n > 255 {
		return 0, fmt.Errorf("channel must be 0..255")
	}
	return n, nil
}

func timeRange(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	if date := q.Get("date"); date != "" {
		day, err := time.ParseInLocation("2006-01-02", date, time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("date must be YYYY-MM-DD")
		}
		return day, day.Add(24*time.Hour - time.Second), nil
	}
	from := q.Get("from")
	to := q.Get("to")
	if from == "" && to == "" {
		now := time.Now()
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		return day, day.Add(24*time.Hour - time.Second), nil
	}
	if from == "" || to == "" {
		return time.Time{}, time.Time{}, errors.New("from and to must be set together")
	}
	start, err := parseTimestamp(from, false)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
	}
	end, err := parseTimestamp(to, true)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("to is before from")
	}
	return start, end, nil
}

func parseTimestamp(s string, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local); err == nil {
		return t, nil
	}
	day, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, errors.New("must be RFC3339, YYYY-MM-DDTHH:MM:SS, or YYYY-MM-DD")
	}
	if endOfDay {
		return day.Add(24*time.Hour - time.Second), nil
	}
	return day, nil
}
