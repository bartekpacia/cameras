// Command dvrserver is an HTTP facade for a Kenik/Qualvision recorder.
//
//	dvrserver -listen :8080
//
// Address, user, and password come from flags or from DVR_ADDRESS,
// DVR_USER, and DVR_PASSWORD. The process environment must already
// contain them; this program does not read a file. DVR_PORT is the RTSP
// port used by the other tool in this repo; this server always speaks
// the control protocol on port 5801 unless -dvr-port says otherwise.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bartekpacia/cameras/server"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
	host := flag.String("dvr-host", "", "recorder IP (default $DVR_ADDRESS)")
	port := flag.Int("dvr-port", 5801, "recorder control port")
	user := flag.String("user", "", "recorder user (default $DVR_USER)")
	password := flag.String("password", "", "recorder password (default $DVR_PASSWORD)")
	channel := flag.Int("channel", 1, "search channel when the request omits one")
	flag.Parse()

	if *host == "" {
		*host = os.Getenv("DVR_ADDRESS")
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
