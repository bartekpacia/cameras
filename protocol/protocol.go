// Package protocol speaks the Kenik/Qualvision control protocol used by
// the uCloud Cam app: TCP port 5801, frames starting with ee ee ff ff.
//
// This is not the older Sofia/DVRIP JSON protocol on port 34567.
// That port accepts a connection on this recorder and then never replies.
package protocol

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	magic = "\xee\xee\xff\xff"

	cmdHello     = 0x0101
	cmdLogin     = 0x0105
	cmdKeepalive = 0x0001
	cmdSearch    = 0x0301
	cmdClaim     = 0x0311
	cmdRead      = 0x0313
	cmdStop      = 0x0314

	headerLen = 0x14

	// Full packet sizes observed from the iOS app.
	helloPacketLen = 112
	loginPacketLen = 561
	searchBodyLen  = 68 - headerLen
	claimBodyLen   = 384 - headerLen
	readBodyLen    = 64 - headerLen
	stopBodyLen    = 32 - headerLen

	offStatus    = 0x0B
	offToken     = 0x30
	offHandle    = 0x13C
	offDevice    = 0x16C
	offClaimTag  = 0xA0
	offFrameTime = 0x30
	offPayloadN  = 0x3C
	offPayload   = 0x40

	maxPacket = 4 << 20
)

// sofiaAlphabet is the base62 alphabet used by Sofia-derived password hashes.
const sofiaAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Recording is one file stored on the DVR.
// Start and End are the recorder's wall clock, with no zone attached
// beyond the Local location of the machine that parsed the name.
type Recording struct {
	ID    string
	Start time.Time
	End   time.Time
}

// Duration is End minus Start. It is zero when the name could not be parsed.
func (r Recording) Duration() time.Duration {
	if r.End.Before(r.Start) {
		return 0
	}
	return r.End.Sub(r.Start)
}

// Error is a protocol failure. Status is the DVR's result byte when the
// recorder answered and refused the command. Status is zero when the
// failure happened before a status byte existed.
type Error struct {
	Op     string
	Status byte
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	if e.Status != 0 {
		return fmt.Sprintf("%s: recorder status %d", e.Op, e.Status)
	}
	return e.Op
}

func (e *Error) Unwrap() error { return e.Err }

// SofiaHash is the 8-character password hash used by Sofia-family recorders:
// MD5(password), then each byte pair summed modulo 62 into sofiaAlphabet.
func SofiaHash(password string) string {
	sum := md5.Sum([]byte(password))
	out := make([]byte, 8)
	for i := range out {
		out[i] = sofiaAlphabet[(int(sum[2*i])+int(sum[2*i+1]))%62]
	}
	return string(out)
}

// LoginDigest is the hash the recorder checks:
// MD5(helloToken + ":" + SofiaHash(password)), as 32 lowercase hex characters.
func LoginDigest(token, password string) string {
	sum := md5.Sum([]byte(token + ":" + SofiaHash(password)))
	return hex.EncodeToString(sum[:])
}

// ParseRecording reads the start and end clock out of a qvfs file name.
// The trailing twelve numbers are YY,M,D,h,m,s,YY,M,D,h,m,s.
func ParseRecording(id string) (Recording, error) {
	parts := splitUnderscore(id)
	if len(parts) < 13 || parts[0] != "qvfs" {
		return Recording{}, fmt.Errorf("not a qvfs recording id")
	}
	nums := parts[len(parts)-12:]
	start, err := clockFrom(nums[:6])
	if err != nil {
		return Recording{}, fmt.Errorf("recording start: %w", err)
	}
	end, err := clockFrom(nums[6:])
	if err != nil {
		return Recording{}, fmt.Errorf("recording end: %w", err)
	}
	return Recording{ID: id, Start: start, End: end}, nil
}

func clockFrom(parts []string) (time.Time, error) {
	n := make([]int, len(parts))
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return time.Time{}, err
		}
		n[i] = v
	}
	year := 2000 + n[0]
	t := time.Date(year, time.Month(n[1]), n[2], n[3], n[4], n[5], 0, time.Local)
	if t.Year() != year || int(t.Month()) != n[1] || t.Day() != n[2] {
		return time.Time{}, errors.New("clock fields are not a real date")
	}
	return t, nil
}

func splitUnderscore(s string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '_' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return parts
}

func packClock(t time.Time) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint16(buf[0:2], uint16(t.Year()))
	binary.LittleEndian.PutUint16(buf[2:4], uint16(t.Month()))
	buf[4] = byte(t.Day())
	buf[5] = byte(t.Hour())
	buf[6] = byte(t.Minute())
	buf[7] = byte(t.Second())
	return buf[:]
}

func unpackClock(raw []byte) (time.Time, bool) {
	if len(raw) < 8 {
		return time.Time{}, false
	}
	year := int(binary.LittleEndian.Uint16(raw[0:2]))
	month := int(binary.LittleEndian.Uint16(raw[2:4]))
	day := int(raw[4])
	hour := int(raw[5])
	min := int(raw[6])
	sec := int(raw[7])
	t := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.Local)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}
