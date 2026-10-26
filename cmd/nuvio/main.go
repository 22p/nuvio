// Command nuvio runs the Nuvio media server.
package main

import (
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nuvio/internal/server"
	webassets "nuvio/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", env("NUVIO_ADDR", ":8096"), "HTTP listen address")
	data := flag.String("data", env("NUVIO_DATA", "./data"), "data directory")
	demo := flag.Bool("demo", env("NUVIO_DEMO", "1") != "0", "include the built-in demo library on first setup")
	ffmpeg := flag.String("ffmpeg", "", "path to the ffmpeg binary (optional)")
	ffprobe := flag.String("ffprobe", "", "path to the ffprobe binary (optional)")
	flag.Parse()

	dist, err := fs.Sub(webassets.Assets, "dist")
	if err != nil {
		log.Fatalf("embedded web assets: %v", err)
	}
	srv, err := server.New(server.Config{
		Addr:    *addr,
		DataDir: *data,
		Demo:    *demo,
		FFmpeg:  *ffmpeg,
		FFprobe: *ffprobe,
		WebFS:   dist,
	})
	if err != nil {
		log.Fatalf("startup: %v", err)
	}
	defer srv.Close()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	log.Printf("Nuvio started: http://localhost:%d", port)
	if srv.Store.SetupComplete() {
		srv.ScanIfNeeded()
	} else {
		log.Printf("Waiting for first-time setup…")
	}

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-stop
	log.Printf("Shutting down…")
	_ = httpSrv.Close()
	srv.Close()
}
