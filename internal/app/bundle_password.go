package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Passwords come from terminal/stdin, never argv, environment or config. Keep one
// buffered reader for both export prompts so piped confirmation is not lost.
func readBundlePasswords(in io.Reader, out io.Writer, confirm bool) (string, string, error) {
	reader := bufio.NewReaderSize(in, maxPasswordBytes+2)
	read := func(prompt string) (string, error) {
		if _, err := fmt.Fprint(out, prompt); err != nil {
			return "", err
		}
		if file, ok := in.(*os.File); ok && term.IsTerminal(file.Fd()) {
			raw, err := term.ReadPassword(file.Fd())
			fmt.Fprintln(out)
			if err != nil {
				return "", errors.New("cannot read password")
			}
			value := string(raw)
			clear(raw)
			return value, validateBundlePassword(value)
		}
		raw, err := reader.ReadSlice('\n')
		if err != nil && err != io.EOF {
			return "", errors.New("cannot read password or password is too long")
		}
		value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		return value, validateBundlePassword(value)
	}
	password, err := read("Password: ")
	if err != nil {
		return "", "", err
	}
	if !confirm {
		return password, "", nil
	}
	confirmation, err := read("Confirm password: ")
	if err != nil {
		return "", "", err
	}
	if password != confirmation {
		return "", "", errors.New("passwords do not match")
	}
	return password, confirmation, nil
}
