package protocol

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"time"
)

// Client dials one recorder.
type Client struct {
	// Addr is host:port. The control port is 5801.
	Addr string
}

// Session is one logged-in TCP connection.
// The recorder allows one playback claim per session. List may be called
// more than once. Download opens a second connection and should be the
// last call: the claim handle is spent afterwards.
type Session struct {
	addr   string
	handle uint32
	device string

	conn net.Conn
	stop func() bool // stops the context.AfterFunc that closes conn
}

// Device is the model string from the login response, such as "DVR-9804@C0023".
func (s *Session) Device() string { return s.device }

// Close releases the login connection. It is safe to call more than once.
func (s *Session) Close() error {
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// Login says hello, answers the challenge, and returns a session.
// The caller must Close the session.
func (c *Client) Login(ctx context.Context, user, password string) (*Session, error) {
	if user == "" || password == "" {
		return nil, &Error{Op: "login", Err: errors.New("user and password are required")}
	}
	if len(user) >= 32 {
		return nil, &Error{Op: "login", Err: errors.New("user name longer than 31 bytes")}
	}

	conn, stop, err := dial(ctx, c.Addr)
	if err != nil {
		return nil, &Error{Op: "login", Err: err}
	}
	sess := &Session{addr: c.Addr, conn: conn, stop: stop}
	ok := false
	defer func() {
		if !ok {
			sess.Close()
		}
	}()

	hello := make([]byte, helloPacketLen-headerLen)
	copy(hello[0:6], []byte{1, 1, 2, 1, 3, 1})
	binary.LittleEndian.PutUint32(hello[0x18:0x1C], 3)
	if err := send(conn, ctx, cmdHello, hello); err != nil {
		return nil, &Error{Op: "hello", Err: err}
	}
	helloResp, err := recv(conn, ctx)
	if err != nil {
		return nil, &Error{Op: "hello", Err: err}
	}
	token := cString(helloResp, offToken)
	if token == "" {
		return nil, &Error{Op: "hello", Err: errors.New("recorder sent no challenge token")}
	}

	login := make([]byte, loginPacketLen-headerLen)
	copy(login, user)
	copy(login[0x20:0x40], LoginDigest(token, password))
	copy(login[0x218:0x21D], "C0023")
	if err := send(conn, ctx, cmdLogin, login); err != nil {
		return nil, &Error{Op: "login", Err: err}
	}
	loginResp, err := recv(conn, ctx)
	if err != nil {
		return nil, &Error{Op: "login", Err: err}
	}
	if st := loginResp[offStatus]; st != 0 {
		return nil, &Error{Op: "login", Status: st}
	}
	if len(loginResp) < offHandle+4 {
		return nil, &Error{Op: "login", Err: errors.New("login response has no handle")}
	}
	sess.handle = binary.LittleEndian.Uint32(loginResp[offHandle:])
	sess.device = cString(loginResp, offDevice)

	keep := make([]byte, 32)
	for i := range keep {
		keep[i] = '0'
	}
	if err := send(conn, ctx, cmdKeepalive, keep); err != nil {
		return nil, &Error{Op: "keepalive", Err: err}
	}
	if _, err := recvCmd(conn, ctx, cmdKeepalive); err != nil {
		return nil, &Error{Op: "keepalive", Err: err}
	}

	ok = true
	return sess, nil
}

// List returns recordings that overlap [start, end] on channel.
// Channel 1 is what the uCloud Cam app sends. Channel 0 makes this
// recorder ignore the time range and return an unrelated page of files.
func (s *Session) List(ctx context.Context, start, end time.Time, channel int) ([]Recording, error) {
	if channel < 0 || channel > 255 {
		return nil, &Error{Op: "search", Err: fmt.Errorf("channel %d out of range", channel)}
	}
	if end.Before(start) {
		return nil, &Error{Op: "search", Err: errors.New("end is before start")}
	}
	body := make([]byte, searchBodyLen)
	binary.LittleEndian.PutUint32(body[0:4], s.handle)
	binary.LittleEndian.PutUint32(body[8:12], uint32(channel))
	copy(body[0x14:0x1C], packClock(start))
	copy(body[0x20:0x28], packClock(end))
	if err := send(s.conn, ctx, cmdSearch, body); err != nil {
		return nil, &Error{Op: "search", Err: err}
	}
	resp, err := recvCmd(s.conn, ctx, cmdSearch)
	if err != nil {
		return nil, &Error{Op: "search", Err: err}
	}
	if st := resp[offStatus]; st != 0 {
		return nil, &Error{Op: "search", Status: st}
	}

	ids := recordingIDs(resp)
	out := make([]Recording, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		rec, err := ParseRecording(id)
		if err != nil {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b Recording) int {
		return a.Start.Compare(b.Start)
	})
	return out, nil
}

// Download writes the recording's H.264 byte stream (Annex B) to w.
// It opens a second TCP connection. s must stay open until Download returns:
// the read commands carry the handle from the login response.
//
// The first bytes written are the sequence-parameter set (a keyframe).
// Leading predicted frames, which cannot be decoded alone, are dropped.
func (s *Session) Download(ctx context.Context, id string, w io.Writer) error {
	rec, err := ParseRecording(id)
	if err != nil {
		return &Error{Op: "download", Err: err}
	}
	conn, stop, err := dial(ctx, s.addr)
	if err != nil {
		return &Error{Op: "download", Err: err}
	}
	defer stop()
	defer conn.Close()

	claim := make([]byte, claimBodyLen)
	binary.LittleEndian.PutUint32(claim[0:4], s.handle)
	if 4+len(id) > len(claim) {
		return &Error{Op: "download", Err: errors.New("recording id does not fit in a claim packet")}
	}
	copy(claim[4:], id)
	if err := send(conn, ctx, cmdClaim, claim); err != nil {
		return &Error{Op: "claim", Err: err}
	}
	claimResp, err := recvCmd(conn, ctx, cmdClaim)
	if err != nil {
		return &Error{Op: "claim", Err: err}
	}
	if st := claimResp[offStatus]; st != 0 {
		return &Error{Op: "claim", Status: st}
	}
	if len(claimResp) < offClaimTag+4 {
		return &Error{Op: "claim", Err: errors.New("claim response has no file tag")}
	}
	// The tag is per claim. Replaying a tag from an older capture
	// comes back with status 2 and no video.
	tag := binary.LittleEndian.Uint32(claimResp[offClaimTag:])

	started := false
	for seq := uint32(1); seq < 8000; seq++ {
		body := make([]byte, readBodyLen)
		binary.LittleEndian.PutUint32(body[0:4], s.handle)
		binary.LittleEndian.PutUint32(body[4:8], tag)
		binary.LittleEndian.PutUint32(body[8:12], seq)
		binary.LittleEndian.PutUint32(body[12:16], 0x100)
		if err := send(conn, ctx, cmdRead, body); err != nil {
			if started && (ctx.Err() == nil) && isEOF(err) {
				break
			}
			return endOrErr(ctx, started, "read", err)
		}
		msg, err := recvCmd(conn, ctx, cmdRead)
		if err != nil {
			return endOrErr(ctx, started, "read", err)
		}
		if st := msg[offStatus]; st != 0 {
			if started {
				break
			}
			return &Error{Op: "read", Status: st}
		}
		payload, ok := videoPayload(msg)
		if !ok {
			continue
		}
		if !started {
			i := bytes.Index(payload, []byte{0, 0, 0, 1, 0x67})
			if i < 0 {
				continue
			}
			payload = payload[i:]
			started = true
		}
		if _, err := w.Write(payload); err != nil {
			return &Error{Op: "download", Err: err}
		}
		if when, ok := unpackClock(msg[offFrameTime:]); ok && when.After(rec.End) {
			break
		}
	}
	if !started {
		return &Error{Op: "download", Err: errors.New("recorder sent no video frames")}
	}

	// Best-effort stop. The recorder often closes the socket itself at EOF.
	stopBody := make([]byte, stopBodyLen)
	binary.LittleEndian.PutUint32(stopBody[0:4], s.handle)
	binary.LittleEndian.PutUint32(stopBody[4:8], tag)
	binary.LittleEndian.PutUint32(stopBody[8:12], 1)
	_ = send(conn, ctx, cmdStop, stopBody)
	return nil
}

func endOrErr(ctx context.Context, started bool, op string, err error) error {
	if ctx.Err() != nil {
		return &Error{Op: op, Err: contextErr(ctx)}
	}
	if started && isEOF(err) {
		return nil
	}
	return &Error{Op: op, Err: err}
}

func contextErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("context canceled")
}

func dial(ctx context.Context, addr string) (net.Conn, func() bool, error) {
	var d net.Dialer
	d.Timeout = 8 * time.Second
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	return conn, stop, nil
}

func send(conn net.Conn, ctx context.Context, cmd uint32, body []byte) error {
	if err := arm(conn, ctx); err != nil {
		return err
	}
	pkt := make([]byte, headerLen+len(body))
	copy(pkt[0:4], magic)
	binary.LittleEndian.PutUint32(pkt[4:8], uint32(len(pkt)))
	binary.LittleEndian.PutUint32(pkt[8:12], cmd)
	copy(pkt[headerLen:], body)
	_, err := conn.Write(pkt)
	return err
}

func recvCmd(conn net.Conn, ctx context.Context, want uint32) ([]byte, error) {
	for range 8 {
		pkt, err := recv(conn, ctx)
		if err != nil {
			return nil, err
		}
		if len(pkt) < 12 {
			continue
		}
		got := binary.LittleEndian.Uint32(pkt[8:12]) & 0xFFFF
		if got == want {
			return pkt, nil
		}
	}
	return nil, fmt.Errorf("no response for command %#x", want)
}

func recv(conn net.Conn, ctx context.Context) ([]byte, error) {
	if err := arm(conn, ctx); err != nil {
		return nil, err
	}
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return nil, err
	}
	if string(hdr[:4]) != magic {
		return nil, fmt.Errorf("bad magic %x", hdr[:4])
	}
	total := binary.LittleEndian.Uint32(hdr[4:8])
	if total < 8 || total > maxPacket {
		return nil, fmt.Errorf("packet length %d", total)
	}
	pkt := make([]byte, total)
	copy(pkt, hdr)
	if _, err := io.ReadFull(conn, pkt[8:]); err != nil {
		return nil, err
	}
	return pkt, nil
}

func arm(conn net.Conn, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return conn.SetDeadline(deadline)
}

func isEOF(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func cString(pkt []byte, off int) string {
	if off >= len(pkt) {
		return ""
	}
	end := off
	for end < len(pkt) && pkt[end] != 0 {
		end++
	}
	return string(pkt[off:end])
}

func recordingIDs(pkt []byte) []string {
	var ids []string
	rest := pkt
	needle := []byte("qvfs_")
	for {
		i := bytes.Index(rest, needle)
		if i < 0 {
			return ids
		}
		rest = rest[i:]
		j := len(needle)
		for j < len(rest) && (rest[j] == '_' || (rest[j] >= '0' && rest[j] <= '9')) {
			j++
		}
		ids = append(ids, string(rest[:j]))
		rest = rest[j:]
	}
}

func videoPayload(msg []byte) ([]byte, bool) {
	if len(msg) < offPayload+4 {
		return nil, false
	}
	if msg[offPayload] != 0 || msg[offPayload+1] != 0 || msg[offPayload+2] != 0 || msg[offPayload+3] != 1 {
		return nil, false
	}
	n := binary.LittleEndian.Uint32(msg[offPayloadN:])
	end := offPayload + int(n)
	if n < 5 || end > len(msg) {
		return nil, false
	}
	return msg[offPayload:end], true
}
