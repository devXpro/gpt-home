package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// securityCmd is the macOS Keychain tool; tests replace it with a fake.
var securityCmd = "/usr/bin/security"

// keychainStore saves secret as the password of the generic Keychain item (service, account), replacing an existing
// one. The secret reaches `security` on stdin (its interactive mode), never in any process's argument list.
func keychainStore(service, account, secret string) error {
	if runtime.GOOS != "darwin" && securityCmd == "/usr/bin/security" {
		return errors.New("--keychain works only on macOS")
	}
	for _, s := range []string{service, account, secret} {
		if s == "" || strings.ContainsAny(s, "\"\\\n\r") {
			return fmt.Errorf("cannot pass %q to the Keychain tool (empty, or has quotes, backslashes or newlines)",
				strings.ReplaceAll(s, secret, "[key]"))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, securityCmd, "-i")
	cmd.Stdin = strings.NewReader(`add-generic-password -U -s "` + service + `" -a "` + account + `" -w "` + secret + "\"\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(strings.ReplaceAll(out.String(), secret, "[key]"))
		return fmt.Errorf("save to Keychain item %q: %w: %s", service, err, msg)
	}
	return nil
}
