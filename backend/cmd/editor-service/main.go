package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"collaborative-editor/internal/editor"
)

func main() {
	// Parse flags
	var (
		port = flag.String("port", "8080", "Port to listen on")
		env  = flag.String("env", "dev", "Environment (dev, staging, prod)")
	)
	flag.Parse()

	// Create editor config
	editorConfig := &editor.Config{
		MaxMessageSize: 512 * 1024,        // 512KB
		WriteTimeout:   10 * time.Second,
		ReadTimeout:    60 * time.Second,
		PingInterval:   30 * time.Second,
		MaxClients:     1000,
	}

	// Initialize the editor service
	service := editor.NewService(editorConfig)
	
	// Start the service
	if err := service.Start(); err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}
	
	// Set up HTTP routes
	mux := http.NewServeMux()
	
	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("healthy"))
	})
	
	// WebSocket endpoint
	mux.HandleFunc("/ws", service.HandleWebSocket)
	
	// Static files (in development only)
	if *env == "dev" {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "../frontend/public/index.html")
		})
	}

	// Start server
	server := &http.Server{
		Addr:    ":" + *port,
		Handler: mux,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")
		service.Shutdown()
		server.Close()
	}()

	log.Printf("Editor service starting on port %s (env: %s)", *port, *env)
	log.Println("Open http://localhost:" + *port + "/?doc=test-doc in multiple browsers")
	
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}
