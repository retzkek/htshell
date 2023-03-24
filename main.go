package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"time"
)

var (
	// how often to refresh token
	RefreshInterval = 10 * time.Minute
	// whether to set BEARER_TOKEN with contents of BEARER_TOKEN_FILE
	ExportBearerToken = true
	// if true, the refresher log is displayed and truncated before each shell prompt
	LogAtPrompt = false
	// prefix displayed on each log line and the prompt
	LogPrefix = "[htshell] "
)

func init() {
	var err error
	if r, ok := os.LookupEnv("HTSHELL_REFRESH_INTERVAL"); ok {
		RefreshInterval, err = time.ParseDuration(r)
		if err != nil {
			panic(err)
		}
	}

	if r, ok := os.LookupEnv("HTSHELL_EXPORT_BEARER_TOKEN"); ok {
		ExportBearerToken = boolish(r)
	}

	if r, ok := os.LookupEnv("HTSHELL_LOG_AT_PROMPT"); ok {
		LogAtPrompt = boolish(r)
	}

	if r, ok := os.LookupEnv("HTSHELL_PREFIX"); ok {
		LogPrefix = r
	}
	log.SetPrefix(LogPrefix)
}

func boolish(s string) bool {
	switch strings.ToLower(s) {
	case "no", "false", "0":
		return false
	}
	return true
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
		log.Fatalf("unable to create token file: %s", err)
	}
	defer os.Remove(tok.Name()) // delete it when we leave
	os.Setenv("BEARER_TOKEN_FILE", tok.Name())
	if ExportBearerToken {
		os.Setenv("PROMPT_COMMAND", fmt.Sprintf("export BEARER_TOKEN=$(cat %s);%s",
			tok.Name(), os.Getenv("PROMPT_COMMAND")))
	}

	// init Refresher
	rlog, err := os.Create(fmt.Sprintf("%s.log", tok.Name()))
	if err != nil {
		log.Fatalf("unable to create refresher log file: %s", err)
	}
	defer os.Remove(rlog.Name()) // delete it when we leave
	r := Refresher{
		TokenFile: tok.Name(),
		Log:       log.New(rlog, LogPrefix, log.Ldate|log.Ltime),
	}
	if err != nil {
		log.Fatalf("unable to create refresher: %s", err)
	}

	// get initial token
	// TODO: maybe we should do token discovery first?
	if err := r.Refresh(true); err != nil {
		log.Fatalf("unable to get initial token: %s", err)
	}

	if err := r.Start(RefreshInterval); err != nil {
		log.Fatalf("unable to start refresher: %s", err)
	}
	defer r.Stop()

	// get the user's current or login shell
	sh, err := Getsh(u, "/bin/bash")
	if err != nil {
		log.Printf("unable to get login shell, using default (%s): %s", sh, err)
	}

	// create shell command.
	// TODO: what flags?
	cmd := exec.Command(sh)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf(`PS1=%s%s`, LogPrefix, os.Getenv("PS1")))
	if LogAtPrompt {
		// show new log entries at prompt
		// TODO: what if the shell isn't bash?
		// TODO: maybe better as a function?
		// TODO: probably better ways to pass messages from the refresher to the user
		cmd.Env = append(cmd.Env, fmt.Sprintf(`PROMPT_COMMAND=cat %s && truncate -s0 %s;%s`,
			rlog.Name(), rlog.Name(), os.Getenv("PROMPT_COMMAND")))
	} else {
		log.Printf("refresher and htgettoken logs in %s", rlog.Name())
	}

	// run shell
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	cmd.Wait()
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

// Refresher manages refreshing a bearer token.
type Refresher struct {
	TokenFile string
	Log       *log.Logger
	wg        sync.WaitGroup
	cancel    context.CancelFunc
}

// Start a refresher goroutine.
func (r *Refresher) Start(interval time.Duration) error {
	if r.Log != nil {
		r.Log.Printf("refreshing token (%s) every %s", r.TokenFile, interval)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(1)
	go func(ctx context.Context) {
		defer r.wg.Done()
		for {
			select {
			case <-time.After(interval):
				err := r.Refresh(false)
				if err != nil && r.Log != nil {
					r.Log.Printf("error refreshing token: %s", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}(ctx)
	return nil
}

// Stop the refresher goroutine.
func (r *Refresher) Stop() {
	log.Println("stopping the token refresher...")
	r.cancel()
	r.wg.Wait()
}

// Refresh the bearer token. If interactive is true it pipes input and
// output to the parent shell, otherwise logs output to its own log file.
func (r *Refresher) Refresh(interactive bool) error {
	if r.Log != nil {
		r.Log.Printf("refeshing bearer token (%s)", r.TokenFile)
	}
	cmd := exec.Command("htgettoken", os.Args[1:]...)
	if interactive {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else if r.Log != nil {
		cmd.Stdout = r.Log.Writer()
		cmd.Stderr = r.Log.Writer()
	}
	return cmd.Run()
}
