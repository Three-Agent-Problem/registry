// Command registry runs an A2A agent registry with a card checker.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/three-agent-problem/registry/server"
)

func main() {
	addr := flag.String("addr", ":7070", "Listen address. Port 7000 is avoided because macOS AirPlay uses it.")
	interval := flag.Duration("interval", 30*time.Second, "How often registered agents are re-checked.")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := server.New(nil)
	go reg.Run(ctx, *interval)

	srv := &http.Server{Addr: *addr, Handler: reg.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	log.Printf("registry listening on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
