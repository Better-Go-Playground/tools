//go:build !wasm

// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gocommand is a helper for calling the go command.
package gocommand

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// runCmdContext is like exec.CommandContext except it sends os.Interrupt
// before os.Kill.
func runCmdContext(ctx context.Context, cmd *exec.Cmd) (err error) {
	// If cmd.Stdout is not an *os.File, the exec package will create a pipe and
	// copy it to the Writer in a goroutine until the process has finished and
	// either the pipe reaches EOF or command's WaitDelay expires.
	//
	// However, the output from 'go list' can be quite large, and we don't want to
	// keep reading (and allocating buffers) if we've already decided we don't
	// care about the output. We don't want to wait for the process to finish, and
	// we don't wait to wait for the WaitDelay to expire either.
	//
	// Instead, if cmd.Stdout requires a copying goroutine we explicitly replace
	// it with a pipe (which is an *os.File), which we can close in order to stop
	// copying output as soon as we realize we don't care about it.
	var stdoutW *os.File
	if cmd.Stdout != nil {
		if _, ok := cmd.Stdout.(*os.File); !ok {
			var stdoutR *os.File
			stdoutR, stdoutW, err = os.Pipe()
			if err != nil {
				return err
			}
			prevStdout := cmd.Stdout
			cmd.Stdout = stdoutW

			stdoutErr := make(chan error, 1)
			go func() {
				_, err := io.Copy(prevStdout, stdoutR)
				if err != nil {
					err = fmt.Errorf("copying stdout: %w", err)
				}
				stdoutErr <- err
			}()
			defer func() {
				// We started a goroutine to copy a stdout pipe.
				// Wait for it to finish, or terminate it if need be.
				var err2 error
				select {
				case err2 = <-stdoutErr:
					stdoutR.Close()
				case <-ctx.Done():
					stdoutR.Close()
					// Per https://pkg.go.dev/os#File.Close, the call to stdoutR.Close
					// should cause the Read call in io.Copy to unblock and return
					// immediately, but we still need to receive from stdoutErr to confirm
					// that it has happened.
					<-stdoutErr
					err2 = ctx.Err()
				}
				if err == nil {
					err = err2
				}
			}()

			// Per https://pkg.go.dev/os/exec#Cmd, “If Stdout and Stderr are the
			// same writer, and have a type that can be compared with ==, at most
			// one goroutine at a time will call Write.”
			//
			// Since we're starting a goroutine that writes to cmd.Stdout, we must
			// also update cmd.Stderr so that it still holds.
			func() {
				defer func() { recover() }()
				if cmd.Stderr == prevStdout {
					cmd.Stderr = cmd.Stdout
				}
			}()
		}
	}

	startTime := time.Now()
	err = cmd.Start()
	if stdoutW != nil {
		// The child process has inherited the pipe file,
		// so close the copy held in this process.
		stdoutW.Close()
		stdoutW = nil
	}
	if err != nil {
		return err
	}

	resChan := make(chan error, 1)
	go func() {
		resChan <- cmd.Wait()
	}()

	// If we're interested in debugging hanging Go commands, stop waiting after a
	// minute and panic with interesting information.
	debug := DebugHangingGoCommands
	if debug {
		timer := time.NewTimer(1 * time.Minute)
		defer timer.Stop()
		select {
		case err := <-resChan:
			return err
		case <-timer.C:
			// HandleHangingGoCommand terminates this process.
			// Pass off resChan in case we can collect the command error.
			handleHangingGoCommand(startTime, cmd, resChan)
		case <-ctx.Done():
		}
	} else {
		select {
		case err := <-resChan:
			return err
		case <-ctx.Done():
		}
	}

	// Cancelled. Interrupt and see if it ends voluntarily.
	if err := cmd.Process.Signal(os.Interrupt); err == nil {
		// (We used to wait only 1s but this proved
		// fragile on loaded builder machines.)
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case err := <-resChan:
			return err
		case <-timer.C:
		}
	}

	// Didn't shut down in response to interrupt. Kill it hard.
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && debug {
		log.Printf("error killing the Go command: %v", err)
	}

	return <-resChan
}

// handleHangingGoCommand outputs debugging information to help diagnose the
// cause of a hanging Go command, and then exits with log.Fatalf.
func handleHangingGoCommand(start time.Time, cmd *exec.Cmd, resChan chan error) {
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd", "netbsd", "openbsd":
		fmt.Fprintln(os.Stderr, `DETECTED A HANGING GO COMMAND

			The gopls test runner has detected a hanging go command. In order to debug
			this, the output of ps and lsof/fstat is printed below.

			See golang/go#54461 for more details.`)

		fmt.Fprintln(os.Stderr, "\nps axo ppid,pid,command:")
		fmt.Fprintln(os.Stderr, "-------------------------")
		psCmd := exec.Command("ps", "axo", "ppid,pid,command")
		psCmd.Stdout = os.Stderr
		psCmd.Stderr = os.Stderr
		if err := psCmd.Run(); err != nil {
			log.Printf("Handling hanging Go command: running ps: %v", err)
		}

		listFiles := "lsof"
		if runtime.GOOS == "freebsd" || runtime.GOOS == "netbsd" {
			listFiles = "fstat"
		}

		fmt.Fprintln(os.Stderr, "\n"+listFiles+":")
		fmt.Fprintln(os.Stderr, "-----")
		listFilesCmd := exec.Command(listFiles)
		listFilesCmd.Stdout = os.Stderr
		listFilesCmd.Stderr = os.Stderr
		if err := listFilesCmd.Run(); err != nil {
			log.Printf("Handling hanging Go command: running %s: %v", listFiles, err)
		}
		// Try to extract information about the slow go process by issuing a SIGQUIT.
		if err := cmd.Process.Signal(sigStuckProcess); err == nil {
			select {
			case err := <-resChan:
				stderr := "not a bytes.Buffer"
				if buf, _ := cmd.Stderr.(*bytes.Buffer); buf != nil {
					stderr = buf.String()
				}
				log.Printf("Quit hanging go command:\n\terr:%v\n\tstderr:\n%v\n\n", err, stderr)
			case <-time.After(5 * time.Second):
			}
		} else {
			log.Printf("Sending signal %d to hanging go command: %v", sigStuckProcess, err)
		}
	}
	log.Fatalf("detected hanging go command (golang/go#54461); waited %s\n\tcommand:%s\n\tpid:%d", time.Since(start), cmd, cmd.Process.Pid)
}
