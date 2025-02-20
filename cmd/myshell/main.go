package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"
)

var Handlers = make(map[string]func(args []string) error)
var input *os.File = os.Stdin
var output *os.File = os.Stdout
var errors *os.File = os.Stderr

func handleExit(args []string) error {
	var (
		exitCode int
		err      error
	)
	if len(args) == 1 {
		exitCode, err = strconv.Atoi(args[0])
		if err != nil {
			return err
		}
	}
	os.Exit(exitCode)
	return nil
}

func locateCmd(cmd string) (string, bool) {
	paths := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	paths = removeDuplicates(paths)
	for _, path := range paths {
		fp := filepath.Join(path, cmd)
		if _, err := os.Stat(fp); err == nil {
			return fp, true
		}
	}
	return "", false
}

func handleEcho(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(output)
		return nil
	}
	for i := 0; i < len(args)-1; i++ {
		fmt.Fprintf(output, "%s ", args[i])
	}
	fmt.Fprintln(output, args[len(args)-1])
	return nil
}

func handleType(args []string) error {
	if len(args) != 1 {
		return nil
	}
	cmd := args[0]
	if _, ok := Handlers[cmd]; ok {
		fmt.Fprintf(output, "%s is a shell builtin\n\r", cmd)
		return nil
	}
	if path, ok := locateCmd(cmd); ok {
		fmt.Fprintf(output, "%s is %s\n\r", cmd, path)
		return nil
	}
	fmt.Fprintf(errors, "%s: not found\n", cmd)
	return nil
}

func handleFileOpening(name string, flag int, perm os.FileMode, def *os.File) *os.File {
	file, err := os.OpenFile(name, flag, perm)
	if err == nil {
		return file
	} else {
		fmt.Fprintf(errors, "Error opening output file: %v\n", err)
		return def
	}
}

func handlePwd(args []string) error {
	mydir, err := os.Getwd()
	if err != nil {
		return err
	}
	fmt.Fprintln(output, mydir)
	return nil
}

func handleCd(args []string) error {
	if args[0] == "~" {
		args[0], _ = os.UserHomeDir()
	}
	if err := os.Chdir(args[0]); err != nil {
		fmt.Fprintf(output, "%s: No such file or directory\n\r", args[0])
	}
	return nil
}

func handleCat(args []string) error {
	result := ""
	for i := 0; i < len(args); i++ {
		data, err := os.ReadFile(args[i])
		if err != nil {
			fmt.Fprintln(errors, "Error reading file:", args[i])
			continue
		}
		result = result + string(data)
	}
	fmt.Fprint(output, result+"\n")
	output.Sync()
	return nil
}

func parseInput(cmd string) []string {
	cmd = strings.Trim(cmd, "\n\r")
	var parts []string
	var currentString string = ""
	var isSingleQuoted bool = false
	var isDoubleQuoted bool = false
	for i := 0; i < len(cmd); i++ {
		if !isDoubleQuoted && !isSingleQuoted && cmd[i] == '\\' {
			if i+1 < len(cmd) {
				currentString += string(cmd[i+1])
			}
			i++
			continue
		} else if isSingleQuoted {
			if cmd[i] == '\'' {
				isSingleQuoted = false
			} else {
				currentString += string(cmd[i])
			}
			continue
		} else if isDoubleQuoted {
			if cmd[i] == '"' {
				isDoubleQuoted = false
			} else {
				if cmd[i] == '\\' {
					if i+1 < len(cmd) && (cmd[i+1] == '\\' || cmd[i+1] == '$' || cmd[i+1] == '"') {
						currentString += string(cmd[i+1])
						i++
						continue
					}
				}
				currentString += string(cmd[i])
			}
			continue
		} else if cmd[i] == ' ' {
			if len(currentString) > 0 {
				parts = append(parts, currentString)
				currentString = ""
			}
			continue
		}
		if cmd[i] == '\'' {
			isSingleQuoted = true
		} else if cmd[i] == '"' {
			isDoubleQuoted = true
		} else {
			currentString += string(cmd[i])
		}
	}
	if len(currentString) > 0 {
		parts = append(parts, currentString)
	}
	return parts
}

func readInput(reader io.Reader) (input string) {
	prevState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintln(errors, "Error Reading Input")
		panic(err)
	}
	defer term.Restore(int(os.Stdin.Fd()), prevState)

	r := bufio.NewReader(reader)
loop:
	for {
		ch, _, err := r.ReadRune()
		if err != nil {
			fmt.Println(err)
			continue
		}
		switch ch {
		case '\x1b': //disable arrow keys while input
			r.ReadRune() // to extract "[A"
			r.ReadRune()
		case '\x03': // Ctrl + C
			os.Exit(0)
		case '\x7F': //backspace
			if length := len(input); length > 0 {
				input = input[:length-1]
				fmt.Fprint(os.Stdout, "\b \b")
			}
		case '\r', '\n': //enter
			fmt.Fprint(os.Stdout, "\r\n")
			break loop
		case '\t': //tab
			matches := autoComplete(input)
			if len(matches) <= 1 {
				if len(matches) != 0 {
					input += matches[0] + " "
					fmt.Fprint(os.Stdout, matches[0]+" ")
				} else {
					fmt.Print("\a")
				}
			} else {
				partialMatch := longestCommonPrefix(matches)
				if partialMatch == "" {
					fmt.Print("\a")
					next, _, err := r.ReadRune()
					if err != nil {
						fmt.Println(err)
						continue
					}
					if next == '\t' {
						slices.Sort(matches)
						fmt.Fprint(os.Stdout, "\r\n")
						for _, match := range matches {
							fmt.Fprint(os.Stdout, input+match+"  ")
						}
						fmt.Fprint(os.Stdout, "\r\n$ ")
						fmt.Fprint(os.Stdout, input)
					}
				} else {
					fmt.Fprint(os.Stdout, "\r$ "+input+partialMatch)
					input += partialMatch
				}
			}
		default:
			input += string(ch)
			fmt.Fprint(os.Stdout, string(ch))
		}
	}
	return
}

func autoComplete(prefix string) []string {
	for key := range Handlers {
		if strings.HasPrefix(key, prefix) {
			return strings.Split(key[len(prefix):], " ")
		}
	}
	var matches []string
	paths := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	paths = removeDuplicates(paths)
	for _, path := range paths {
		files, err := os.ReadDir(path)
		if err == nil {
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				if strings.HasPrefix(file.Name(), prefix) {
					matches = append(matches, file.Name()[len(prefix):])
				}
			}
		}
	}
	return matches
}

func removeDuplicates(input []string) (result []string) {
	seen := make(map[string]bool)
	for _, val := range input {
		if !seen[val] {
			seen[val] = true
			result = append(result, val)
		}
	}
	return
}

func longestCommonPrefix(matches []string) string {
	prefix := matches[0]
	for _, str := range matches[1:] {
		for !strings.HasPrefix(str, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

func main() {
	Handlers["exit"] = handleExit
	Handlers["echo"] = handleEcho
	Handlers["type"] = handleType
	Handlers["pwd"] = handlePwd
	Handlers["cd"] = handleCd
	Handlers["cat"] = handleCat

	for {
		fmt.Fprint(output, "$ ")
		cmd := readInput(input)

		parts := parseInput(cmd)
		if len(parts) == 0 {
			continue
		}
		cmd = parts[0]
		var args []string
		if len(parts) > 1 {
			args = parts[1:]
		}
		for idx := 0; idx < len(args); idx++ {
			if idx+1 == len(args) {
				break
			}
			var isUsed bool = true
			switch args[idx] {
			case ">", "1>":
				output = handleFileOpening(args[idx+1], os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666, os.Stdout)
			case ">>", "1>>":
				output = handleFileOpening(args[idx+1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666, os.Stdout)
			case "2>":
				errors = handleFileOpening(args[idx+1], os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666, os.Stderr)
			case "2>>":
				errors = handleFileOpening(args[idx+1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666, os.Stderr)
			default:
				isUsed = false
			}
			if isUsed {
				args = append(args[:idx], args[idx+2:]...)
				idx--
			}
		}
		if fn, ok := Handlers[cmd]; ok {
			err := fn(args)
			if err != nil {
				fmt.Fprintln(errors, err)
			}
		} else if _, ok := locateCmd(cmd); ok {
			command := exec.Command(cmd, args...)
			command.Stdout = output
			command.Stderr = errors
			_ = command.Run()
		} else {
			fmt.Fprintf(errors, "%s: command not found\n\r", cmd)
		}
		if output != nil && output != os.Stdout {
			output.Close()
		}
		if errors != nil && errors != os.Stderr {
			errors.Close()
		}
		output = os.Stdout
		errors = os.Stderr
	}
}
