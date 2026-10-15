package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
	"returnsrecon/internal/web"
)

func serveCommand(command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	runtimeRoot := filepath.Join(os.Getenv("LOCALAPPDATA"), "dutucijp-vibecoding-final")
	dbPath := flags.String("db", filepath.Join(runtimeRoot, "state", "business.sqlite"), "business database outside source")
	project := flags.String("project", ".", "project source directory for data worker")
	secretDir := flags.String("secrets", "", "private authentication directory (default beside database)")
	renew := flags.String("renew", "", "renew bootstrap only for an untouched new account")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *secretDir == "" {
		*secretDir = filepath.Join(filepath.Dir(*dbPath), "private-auth")
	}
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02")
	if command == "init-auth" && (day == "10-22" || day == "11-17") {
		return fmt.Errorf("reserved day: no initialization or first security configuration")
	}
	if command == "serve" && (day == "10-22" || day == "11-17") {
		if _, err := os.Stat(filepath.Join(*secretDir, "master.key")); err != nil {
			return fmt.Errorf("reserved day requires a preconfigured frozen runtime")
		}
	}
	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.EnsureQueryIndexes(context.Background()); err != nil {
		return err
	}
	security, err := auth.New(s.DB, auth.Config{SecretDir: *secretDir, Origin: "http://localhost:8080"})
	if err != nil {
		return err
	}
	if command == "init-auth" {
		tokens := map[string]string{}
		if *renew != "" {
			token, e := security.RenewBootstrap(context.Background(), *renew)
			if e != nil {
				return e
			}
			tokens[*renew] = token
		} else {
			tokens, err = security.Initialize(context.Background(), []auth.Identity{{ID: "finance", Role: "finance"}, {ID: "warehouse", Role: "warehouse"}, {ID: "admin", Role: "admin"}})
			if err != nil {
				return err
			}
		}
		if len(tokens) == 0 {
			fmt.Println("Existing accounts retained. New untouched accounts can receive a fresh local token with init-auth --renew ID.")
			return nil
		}
		links := map[string]string{}
		for id, token := range tokens {
			links[id] = "http://localhost:8080/#bootstrap=" + token
		}
		data, _ := json.MarshalIndent(map[string]any{"expires_at": time.Now().Add(15 * time.Minute).Format(time.RFC3339), "links": links, "notice": "Private one-use initialization links. Do not upload, log, screenshot or share; passwords and real factors must be configured by the user."}, "", "  ")
		path := filepath.Join(*secretDir, "initialization-"+time.Now().Format("20060102-150405.000000000")+".json")
		if err = auth.PrivateWrite(path, data); err != nil {
			return err
		}
		fmt.Println("Initialization links saved privately: " + path)
		return nil
	}
	jobs, err := web.NewJobs(s, *project, runtimeRoot)
	if err != nil {
		return err
	}
	defer jobs.Close()
	handler := (&web.Server{Store: s, Auth: security, Origin: "http://localhost:8080", Jobs: jobs}).Handler()
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	fmt.Println("Local application: http://localhost:8080 (all roles require complete security configuration)")
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
