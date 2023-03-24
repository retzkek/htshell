package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"sync"
	"time"
)

var (
	RefreshInterval = 10 * time.Minute
)

func init() {
	var err error
	if r, ok := os.LookupEnv("HTSHELL_REFRESH_INTERVAL"); ok {
		RefreshInterval, err = time.ParseDuration(r)
		if err != nil {
			panic(err)
		}
	}
}

func main() {
	// get user info
	u, err := user.Current()
	if err != nil {
		log.Fatalf("unable to determine current user: %s", err)
	}

	// create temporary token file
	tok, err := os.CreateTemp("", fmt.Sprintf("bt_u%s_", u.Uid))
	if err != nil {
		log.Fatalf("unable to create token file (%s): %s", tok.Name(), err)
	}
	defer os.Remove(tok.Name()) // delete it when we leave
	os.Setenv("BEARER_TOKEN_FILE", tok.Name())

	// get initial token
	// TODO: maybe we should do token discovery first?
	if Refresh(tok.Name(), true) != nil {
		log.Fatalf("unable to get initial token: %s", err)
	}

	// start refresher in goroutine
	log.Printf("refreshing token (%s) every %s", tok.Name(), RefreshInterval.String())
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		for {
			select {
			case <-time.After(RefreshInterval):
				err := Refresh(tok.Name(), false)
				if err != nil {
					// TODO log the output somewhere?
					log.Printf("error refreshing token: %s", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}(ctx)

	// get the user's current or login shell
	sh, err := Getsh(u, "/bin/bash")
	if err != nil {
		log.Printf("unable to get login shell, using default (%s): %s", sh, err)
	}

	// create shell command.
	// TODO: what flags?
	cmd := exec.Command(sh, "-i", "-l")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	// TODO: users will hate us taking over their prompt
	cmd.Env = append(cmd.Env, `PS1=[htshell:\w]\$ `)

	// run shell
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	cmd.Wait()

	// clean up
	cancel()
	log.Println("waiting for token refresher to exit...")
	wg.Wait()
}

// Getsh returns the user's current shell (from SHELL), or login shell (from
// passwd), or fallback if there's an error.
func Getsh(u *user.User, fallback string) (string, error) {
	if sh, ok := os.LookupEnv("SHELL"); ok {
		return sh, nil
	}

	out, err := exec.Command("getent", "passwd", u.Username).Output()
	if err != nil {
		return fallback, err
	}
	if len(out) == 0 {
		return fallback, fmt.Errorf("empty output from getent")
	}
	loc := bytes.LastIndex(out, []byte(":"))
	if loc <= 0 {
		return fallback, fmt.Errorf("bad output from getent: %s", string(out))
	}
	return string(out[loc+1 : len(out)-1]), nil
}

// Refresh refreshes the bearer token in file f.
func Refresh(f string, interactive bool) error {
	cmd := exec.Command("htgettoken", os.Args[1:]...)
	if interactive {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}
