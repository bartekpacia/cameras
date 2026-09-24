// Command dvrserver is an HTTP facade for a Kenik/Qualvision recorder.
//
//	dvrserver -listen :8080
//
// Address, user, and password come from flags or from DVR_ADDRESS,
// DVR_USER, and DVR_PASSWORD. A .env file in the current directory fills
// those variables when they are not already set. DVR_PORT is the RTSP
// port used by the other tool in this repo; this server always speaks
// the control protocol on port 5801 unless -dvr-port says otherwise.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bartekpacia/cameras/server"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
	host := flag.String("dvr-host", "", "recorder IP (default $DVR_ADDRESS or 192.168.1.3)")
	port := flag.Int("dvr-port", 5801, "recorder control port")
	user := flag.String("user", "", "recorder user (default $DVR_USER)")
	password := flag.String("password", "", "recorder password (default $DVR_PASSWORD)")
	channel := flag.Int("channel", 1, "search channel when the request omits one")
	flag.Parse()

	loadDotEnv(".env")
	if *host == "" {
		*host = os.Getenv("DVR_ADDRESS")
		if *host == "" {
			*host = "192.168.1.3"
		}
	}
	if *user == "" {
		*user = os.Getenv("DVR_USER")
	}
	if *password == "" {
		*password = os.Getenv("DVR_PASSWORD")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	api, err := server.New(server.Config{
		Addr:     net.JoinHostPort(*host, fmt.Sprintf("%d", *port)),
		User:     *user,
		Password: *password,
		Channel:  *channel,
	}, log)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()

	log.Info("listening", "addr", *listen, "dvr", net.JoinHostPort(*host, fmt.Sprintf("%d", *port)))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

// loadDotEnv sets unset variables from a KEY=VALUE file.
// It does not override the process environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, strings.TrimSpace(val))
	}
}
