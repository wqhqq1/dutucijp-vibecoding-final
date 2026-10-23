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
	port := flags.Int("port", 8080, "loopback HTTP port")
	listenHost := flags.String("listen-host", "127.0.0.1", "IP interface to bind (explicit opt-in for LAN)")
	python := flags.String("python", "", "explicit bundled Python for data jobs")
	runtimeID := flags.String("runtime-id", "", "public instance identity for the launcher")
	renew := flags.String("renew", "", "renew bootstrap only for an untouched new account")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *secretDir == "" {
		*secretDir = filepath.Join(filepath.Dir(*dbPath), "private-auth")
	}
	if *port < 1024 || *port > 65535 {
		return fmt.Errorf("port must be between 1024 and 65535")
	}
	if net.ParseIP(*listenHost) == nil {
		return fmt.Errorf("listen-host must be an IPv4 or IPv6 interface address")
	}
	if *runtimeID == "" {
		absoluteDB, e := filepath.Abs(*dbPath)
		if e != nil {
			return e
		}
		// A stable public namespace, never an authentication credential or path.
		*runtimeID = "local-" + store.ID(filepath.Clean(absoluteDB))[:24]
	}
	origin := fmt.Sprintf("http://localhost:%d", *port)

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.EnsureQueryIndexes(context.Background()); err != nil {
		return err
	}
	security, err := auth.New(s.DB, auth.Config{SecretDir: *secretDir, Origin: origin})
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
			fmt.Println("Existing accounts retained. Open " + origin + " to select an account without a password, or log in to an existing account.")
			return nil
		}
		links := map[string]string{}
		for id, token := range tokens {
			links[id] = origin + "/#bootstrap=" + token
		}
		data, _ := json.MarshalIndent(map[string]any{"expires_at": nil, "links": links, "notice": "Optional private one-use links without an initialization deadline. Open the app to select an account without entering links. Do not share this file; passwords and real factors must be configured by the user."}, "", "  ")
		path := filepath.Join(*secretDir, "initialization-"+time.Now().Format("20060102-150405.000000000")+".json")
		if err = auth.PrivateWrite(path, data); err != nil {
			return err
		}
		fmt.Println("Open " + origin + " and select an account to set its password. Optional compatibility links saved privately: " + path)
		return nil
	}
	jobs, err := web.NewJobs(s, *project, runtimeRoot)
	if err != nil {
		return err
	}
	defer jobs.Close()
	jobs.Python = *python
	sessionCookie := "recon_session"
	if *port != 8080 {
		sessionCookie = fmt.Sprintf("recon_session_%d", *port)
	}
	handler := (&web.Server{Store: s, Auth: security, Origin: origin, Jobs: jobs, CookieName: sessionCookie, RuntimeID: *runtimeID}).Handler()
	listener, err := net.Listen("tcp", net.JoinHostPort(*listenHost, fmt.Sprint(*port)))
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
	fmt.Printf("Local application: %s (all roles require complete security configuration)\n", origin)
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
