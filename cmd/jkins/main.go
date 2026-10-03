package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/masonhuemmer/jkins/internal/cli"
	"golang.org/x/term"
)

type terminal struct{ reader *bufio.Reader }

func (t terminal) IsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func (t terminal) ReadLine(prompt string) (string, error) {
	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return "", err
	}
	line, err := t.reader.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func (t terminal) ReadPassword(prompt string) (string, error) {
	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return "", err
	}
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(value), err
}

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Deps{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Terminal: terminal{reader: bufio.NewReader(os.Stdin)},
	}))
}
