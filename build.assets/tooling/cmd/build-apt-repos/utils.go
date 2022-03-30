package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
)

func GetSubdirectories(basePath ...string) ([]string, error) {

	files, err := ioutil.ReadDir(filepath.Join(basePath...))
	if err != nil {
		return nil, err
	}

	subdirs := []string{}

	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		subdirs = append(subdirs, f.Name())
	}

	return subdirs, nil
}

func EnsureDirectoryExists(path string) {
	err := os.MkdirAll(path, 0660)
	if err != nil {
		handleFatalError(err, "Failed to create directory \"%s\"", path)
	}
}

func HandleFatalCommandError(err error, message string, command *exec.Cmd, params ...interface{}) {
	commandRan := strings.Join(command.Args, " ")
	handleFatalError(err, fmt.Sprintf("%s. Command ran: \"%s\"", message, commandRan), params...)
}

func handleFatalError(err error, message string, params ...interface{}) {
	HandleFatalIssue(fmt.Sprintf("%s. Error: %v\n", message, err), params...)
}

func HandleFatalIssue(message string, params ...interface{}) {
	fmt.Printf(message, params...)
	fmt.Println()
	debug.PrintStack()
	os.Exit(1)
}
