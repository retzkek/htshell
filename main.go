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
	RefreshInterval = 10 * time.Second
)

func main() {
	u, err := user.Current()
	if err != nil {
		log.Fatalf("unable to determine current user: %s", err)
	}

	sh, err := Getsh(u, "/bin/bash")
	if err != nil {
		log.Printf("unable to get login shell, using default (%s): %s", sh, err)
	}

	cmd := exec.Command(sh, "-i", "-l")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	tok, err := os.CreateTemp("", fmt.Sprintf("bt_u%s", u.Uid))
	if err != nil {
		log.Fatalf("unable to create token file (%s): %s", tok.Name(), err)
	}
	defer os.Remove(tok.Name())

	Refresh(tok.Name(), true)

	log.Printf("refreshing token (%s) every %s", tok.Name(), RefreshInterval.String())

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go func(ctx context.Context) {
	refresh:
		for {
			select {
			case <-time.After(RefreshInterval):
				err := Refresh(tok.Name(), false)
				if err != nil {
					log.Printf("error refreshing token: %s", err)
				}
			case <-ctx.Done():
				break refresh
			}
		}
		wg.Done()
	}(ctx)

	cmd.Env = append(cmd.Env, fmt.Sprintf("BEARER_TOKEN_FILE=%s", tok.Name()))

	if err := cmd.Start(); err != nil {
		panic(err)
	}
	cmd.Wait()
	cancel()
	log.Println("waiting for token refresher to exit...")
	wg.Wait()
}

// Getsh returns the user's login shell, or fallback if there's an error.
func Getsh(u *user.User, fallback string) (string, error) {

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
	cmd.Env = append(cmd.Env, fmt.Sprintf("BEARER_TOKEN_FILE=%s", f))
	if interactive {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}
