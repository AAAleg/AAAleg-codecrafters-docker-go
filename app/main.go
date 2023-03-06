package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"syscall"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	cmd := exec.Command(command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	chrootDir := os.TempDir()

	if err := copyExecutableIntoDir(chrootDir, command); err != nil {
		fmt.Printf("copy executable err: %v", err)
		os.Exit(1)
	}

	if err := createDevNull(chrootDir); err != nil {
		fmt.Printf("error creating /dev/null: %v", err)
		os.Exit(1)
	}

	if err := syscall.Chroot(chrootDir); err != nil {
		fmt.Printf("chroot err: %v", err)
		os.Exit(1)
	}

	err := cmd.Run()
	if exitError, ok := err.(*exec.ExitError); ok {
		os.Exit(exitError.ExitCode())
	} else if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(1)
	}

}

func copyExecutableIntoDir(chrootDir string, executablePath string) error {
	executablePathInChrootDir := path.Join(chrootDir, executablePath)

	if err := os.MkdirAll(path.Dir(executablePathInChrootDir), 0750); err != nil {
		return err
	}

	return CopyFile(executablePath, executablePathInChrootDir)
}

func CopyFile(sourceFilePath string, destinationFilePath string) error {
	sourceFileStat, err := os.Stat(sourceFilePath)
	if err != nil {
		return err
	}

	sourceFile, err := os.Open(sourceFilePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.OpenFile(destinationFilePath, os.O_RDWR|os.O_CREATE, sourceFileStat.Mode())
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	_, err = io.Copy(destinationFile, sourceFile)
	return err
}

func createDevNull(chrootDir string) error {
	if err := os.MkdirAll(path.Join(chrootDir, "dev"), 0750); err != nil {
		return err
	}

	return os.WriteFile(path.Join(chrootDir, "dev", "null"), []byte{}, 0644)
}
